package whatsapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/store"
)

// Attachments are fetched by the background worker or on demand into whatsapp/media/
// (0600, folder 0700), up to the size cap (config whatsapp.media_max_mb, default 64 MB).
// Files the owner sends are kept there from the start.

// MediaFile is an attachment on disk.
type MediaFile struct {
	Path string
	Mime string
	Name string
	Size int64
}

func encodeRef(r MediaRef) string {
	b, _ := json.Marshal(r)
	return string(b)
}

func (s *Service) mediaPath(name string) string {
	return filepath.Join(s.o.MediaDir, filepath.Base(name))
}

func (s *Service) fileExists(name string) bool {
	if name == "" || s.o.MediaDir == "" {
		return false
	}
	_, err := os.Stat(s.mediaPath(name))
	return err == nil
}

func (s *Service) removeFile(name string) {
	if name != "" && s.o.MediaDir != "" {
		_ = os.Remove(s.mediaPath(name))
	}
}

// fileName is the name of a message's attachment on disk: a hash (no person's name or number
// in the folder) and the type's extension.
func fileName(chat, id, mimeType string) string {
	h := sha256.Sum256([]byte(chat + "\x00" + id))
	ext := ""
	if exts, _ := mime.ExtensionsByType(strings.TrimSpace(strings.Split(mimeType, ";")[0])); len(exts) > 0 {
		ext = exts[0]
	}
	switch strings.Split(mimeType, ";")[0] { // the usual ones, whatever the OS's table says
	case "image/jpeg":
		ext = ".jpg"
	case "audio/ogg":
		ext = ".ogg"
	case "video/mp4":
		ext = ".mp4"
	case "image/webp":
		ext = ".webp"
	}
	return hex.EncodeToString(h[:12]) + ext
}

// Media returns a message's attachment, downloading it first if needed.
func (s *Service) Media(ctx context.Context, chat, id string) (MediaFile, error) {
	return s.media(ctx, chat, id, false)
}

func (s *Service) media(ctx context.Context, chat, id string, automatic bool) (MediaFile, error) {
	if err := s.ready(false); err != nil {
		return MediaFile{}, err
	}
	row, err := s.o.Store.WaGetMessage(chat, id)
	if errors.Is(err, store.ErrNotFound) {
		return MediaFile{}, errorf(http.StatusNotFound, "no message %q in this chat", id)
	}
	if err != nil {
		return MediaFile{}, err
	}
	if row.Media == nil || row.Deleted {
		return MediaFile{}, errorf(http.StatusNotFound, "this message has no attachment")
	}
	out := MediaFile{Mime: firstNonEmpty(row.Media.Mime, "application/octet-stream"), Name: row.Media.Name}
	if out.Name == "" {
		out.Name = fileName(chat, id, out.Mime)
	}
	if row.MediaFile != "" {
		if st, err := os.Stat(s.mediaPath(row.MediaFile)); err == nil {
			out.Path, out.Size = s.mediaPath(row.MediaFile), st.Size()
			return out, nil
		}
	}
	if row.MediaRef == "" {
		return MediaFile{}, errorf(http.StatusNotFound, "WhatsApp did not say where this attachment is; open it on your phone")
	}
	var ref MediaRef
	if json.Unmarshal([]byte(row.MediaRef), &ref) != nil {
		return MediaFile{}, errorf(http.StatusNotFound, "this attachment cannot be fetched; open it on your phone")
	}
	limit := s.o.Config().MediaMax()
	if size := max(ref.Size, row.Media.Size); size > limit {
		return MediaFile{}, errorf(http.StatusRequestEntityTooLarge, "this attachment is %d MB, over tmx's %d MB limit; open it on your phone",
			size>>20, limit>>20)
	}
	if err := s.ready(true); err != nil {
		return MediaFile{}, err
	}
	name, err := s.download(ctx, chat, id, ref, out.Mime, limit, automatic)
	if err != nil {
		return MediaFile{}, err
	}
	signal(s.maintenanceWake)
	st, err := os.Stat(s.mediaPath(name))
	if err != nil {
		return MediaFile{}, err
	}
	out.Path, out.Size = s.mediaPath(name), st.Size()
	s.publishMessage(chat, id) // downloaded: true
	return out, nil
}

// download fetches one attachment once, however many ask at the same time.
func (s *Service) download(ctx context.Context, chat, id string, ref MediaRef, mimeType string, limit int64, automatic bool) (string, error) {
	key := chat + "\x00" + id
	for {
		s.mu.Lock()
		wait, busy := s.downloads[key]
		if !busy {
			done := make(chan struct{})
			s.downloads[key] = done
			s.mu.Unlock()
			defer func() {
				s.mu.Lock()
				delete(s.downloads, key)
				s.mu.Unlock()
				close(done)
			}()
			break
		}
		s.mu.Unlock()
		select {
		case <-wait:
			if row, err := s.o.Store.WaGetMessage(chat, id); err == nil && s.fileExists(row.MediaFile) {
				return row.MediaFile, nil
			}
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	select {
	case s.mediaWrite <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-s.mediaWrite }()
	if err := os.MkdirAll(s.o.MediaDir, 0o700); err != nil {
		return "", err
	}
	name := fileName(chat, id, mimeType)
	budget, err := s.mediaBudget(automatic)
	if err != nil {
		return "", err
	}
	wireLimit := limit
	if len(ref.MediaKey) > 0 {
		wireLimit += 26
	} // AES padding plus HMAC
	wireLimit = min(wireLimit, budget)
	dctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	s.evMu.Lock()
	// A delete while waiting for the writer slot must not create a new partial.
	current, getErr := s.o.Store.WaGetMessage(chat, id)
	if getErr != nil || current.Deleted || current.MediaRef != encodeRef(ref) {
		s.evMu.Unlock()
		return "", errorf(http.StatusNotFound, "this attachment is no longer available")
	}
	tmp, err := os.CreateTemp(s.o.MediaDir, ".download-*")
	if err == nil {
		s.markMedia(filepath.Base(tmp.Name()), true)
		s.mu.Lock()
		s.downloadCancels[key] = cancel
		s.mu.Unlock()
	}
	s.evMu.Unlock()
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name()) // after the rename: nothing to remove
	defer s.markMedia(filepath.Base(tmp.Name()), false)
	defer func() { s.mu.Lock(); delete(s.downloadCancels, key); s.mu.Unlock() }()
	bounded := &limitedMediaFile{file: tmp, limit: wireLimit, cancel: cancel}
	if automatic {
		bounded.free = s.mediaFree
	}
	err = s.o.Client.Download(dctx, ref, bounded)
	if bounded.err != nil {
		err = bounded.err
	}
	if err == nil {
		var st os.FileInfo
		if st, err = tmp.Stat(); err == nil && st.Size() > limit {
			err = errorf(http.StatusRequestEntityTooLarge, "this attachment is over tmx's %d MB limit; open it on your phone", limit>>20)
		}
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		var e *Error
		if errors.As(err, &e) {
			return "", err
		}
		s.log.Warn("whatsapp download", "err", err)
		return "", errorf(http.StatusBadGateway, "WhatsApp did not hand out this attachment (it may be too old); open it on your phone")
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return "", err
	}
	// A revoke, unlink or account change during the download must not restore media.
	s.evMu.Lock()
	defer s.evMu.Unlock()
	cur, err := s.o.Store.WaGetMessage(chat, id)
	if err != nil || cur.Deleted || cur.Media == nil || cur.MediaRef != encodeRef(ref) {
		return "", errorf(http.StatusNotFound, "this attachment is no longer available")
	}
	if err := os.Rename(tmp.Name(), s.mediaPath(name)); err != nil {
		return "", err
	}
	if err := s.o.Store.WaSetMediaFile(chat, id, name); err != nil {
		s.removeFile(name)
		return "", err
	}
	return name, nil
}

// keepFile stores a file the owner sends as the message's attachment.
func (s *Service) keepFile(chat, id, mimeType string, data []byte) string {
	select {
	case s.mediaWrite <- struct{}{}:
	case <-s.ctx.Done():
		return ""
	}
	defer func() { <-s.mediaWrite }()
	if s.o.MediaDir == "" || os.MkdirAll(s.o.MediaDir, 0o700) != nil {
		return ""
	}
	name := fileName(chat, id, mimeType)
	budget, err := s.mediaBudget(false)
	if err != nil || int64(len(data)) > budget || int64(len(data)) > s.o.Config().MediaMax() {
		return ""
	}
	s.evMu.Lock()
	defer s.evMu.Unlock()
	if err := os.WriteFile(s.mediaPath(name), data, 0o600); err != nil {
		s.removeFile(name)
		s.log.Warn("whatsapp keep file", "err", err)
		return ""
	}
	s.markMedia(name, true) // retained until the outbound row is stored
	return name
}

// TicketTTL is how long a media ticket plays.
const TicketTTL = 10 * time.Minute

// Ticket lets an <img>/<audio>/<video> load one attachment without the owner token (it never
// goes in a URL): an HMAC of the chat, the message and an expiry with a key in memory only.
func (s *Service) Ticket(chat, id string) (string, time.Time, error) {
	if err := s.ready(false); err != nil {
		return "", time.Time{}, err
	}
	row, err := s.o.Store.WaGetMessage(chat, id)
	if err != nil || row.Media == nil {
		return "", time.Time{}, errorf(http.StatusNotFound, "no attachment %q in this chat", id)
	}
	exp := s.o.Now().Add(TicketTTL).Truncate(time.Second)
	e := strconv.FormatInt(exp.Unix(), 36)
	return e + "." + s.sign(chat, id, e), exp, nil
}

// CheckTicket reports whether t is a valid, unexpired ticket for this attachment.
func (s *Service) CheckTicket(chat, id, t string) bool {
	e, sig, ok := strings.Cut(t, ".")
	if !ok || e == "" || sig == "" {
		return false
	}
	unix, err := strconv.ParseInt(e, 36, 64)
	if err != nil || !s.o.Now().Before(time.Unix(unix, 0)) {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(s.sign(chat, id, e)))
}

func (s *Service) sign(chat, id, exp string) string {
	m := hmac.New(sha256.New, s.key)
	fmt.Fprintf(m, "tmx-whatsapp-ticket\x00%s\x00%s\x00%s", chat, id, exp)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:18])
}

// MediaKind maps a file's type to how WhatsApp sends it.
func MediaKind(mimeType string) (kind string, typ model.WaMessageType) {
	switch m := strings.ToLower(strings.Split(mimeType, ";")[0]); {
	case m == "image/jpeg" || m == "image/png":
		return "image", model.WaImage
	case m == "video/mp4" || m == "video/3gpp":
		return "video", model.WaVideo
	case strings.HasPrefix(m, "audio/"):
		return "audio", model.WaAudio
	}
	return "document", model.WaDocument
}
