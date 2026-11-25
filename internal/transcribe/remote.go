package transcribe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// Fae: the fleet's remote speech service, an OpenAI-style POST <voice.remote_url>/v1/audio/
// transcriptions (multipart file, model, optional language, response_format=verbose_json). It
// is tried first when voice.remote_url is set, so no local worker loads in normal operation.
// A connect failure, a 5xx answer or a timeout falls back to the local worker for that one job
// (service.go); any other answer (a rejected file, an unreadable one) is the job's result.

// remoteConnectTimeout bounds only the TCP connect; a slow or hanging answer is caught by the
// context deadline the caller sets (timeoutFor, as for the local engine).
const remoteConnectTimeout = 2 * time.Second

// remoteMaxAnswer bounds a verbose_json answer read into memory.
const remoteMaxAnswer = 8 << 20

var remoteClient = &http.Client{
	Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: remoteConnectTimeout}).DialContext},
}

// remoteURL is config voice.remote_url, trimmed ("" = Fae is not configured).
func (s *Service) remoteURL() string { return strings.TrimSpace(s.o.Settings().RemoteURL) }

// errRemote marks a failure that falls back to the local worker: a connect failure, a 5xx
// answer or a timeout. Any other error from remoteTranscribe is the job's final answer.
type errRemote struct{ msg string }

func (e errRemote) Error() string { return e.msg }

// classifyRemoteErr turns a failed Do() or body read into the right kind of error: rctx's own
// deadline firing (not the parent's) is a Fae timeout, fallback-eligible; the parent being done
// first (the caller went away, or the daemon stops) is that cancellation as-is, never a Fae
// problem and never a reason to run the job again on the local worker; anything else is an
// ordinary connect/stream failure, also fallback-eligible.
func classifyRemoteErr(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return errRemote{"Fae did not answer in time"}
	}
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	return errRemote{"Fae is unreachable: " + err.Error()}
}

type remoteSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type remoteAnswer struct {
	Text     string          `json:"text"`
	Language string          `json:"language"`
	Segments []remoteSegment `json:"segments"`
}

// remoteTranscribe POSTs j.audio to url's /v1/audio/transcriptions. name is "fae/<model>" for
// the transcript's model field and the log.
func (s *Service) remoteTranscribe(ctx context.Context, url, remoteModel string, j *job, lang string) (Result, string, error) {
	name := "fae/" + remoteModel
	f, err := os.Open(j.audio)
	if err != nil {
		return Result{}, name, err
	}
	defer f.Close()

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fw, err := w.CreateFormFile("file", filepath.Base(j.audio))
	if err != nil {
		return Result{}, name, err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return Result{}, name, err
	}
	_ = w.WriteField("model", remoteModel)
	if lang != "" && lang != "auto" {
		_ = w.WriteField("language", lang)
	}
	_ = w.WriteField("response_format", "verbose_json")
	if err := w.Close(); err != nil {
		return Result{}, name, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(url, "/")+"/v1/audio/transcriptions", &body)
	if err != nil {
		return Result{}, name, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := remoteClient.Do(req)
	if err != nil {
		return Result{}, name, classifyRemoteErr(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		// Return at once: Fae's own body may be stalled too, and draining it to reuse the
		// connection is not worth delaying the fallback below.
		return Result{}, name, errRemote{"Fae answered " + resp.Status}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, remoteMaxAnswer+1))
	if err != nil {
		// Headers can arrive before the deadline even when the body then stalls or the
		// connection drops mid-stream; that is still Fae's problem, not the job's.
		return Result{}, name, classifyRemoteErr(ctx, err)
	}
	if len(b) > remoteMaxAnswer {
		return Result{}, name, errors.New("Fae's answer is too large")
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(b))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		switch resp.StatusCode {
		case http.StatusUnprocessableEntity:
			return Result{}, name, ErrNoSpeech
		default:
			return Result{}, name, fmt.Errorf("%w: Fae rejected the audio (%s): %s", ErrInvalid, resp.Status, msg)
		}
	}
	var a remoteAnswer
	if json.Unmarshal(b, &a) != nil {
		return Result{}, name, fmt.Errorf("Fae's answer is not readable")
	}
	r := Result{Language: a.Language, Segments: []model.TranscriptSegment{}}
	for _, seg := range a.Segments {
		if t := strings.Join(strings.Fields(seg.Text), " "); t != "" {
			r.Segments = append(r.Segments, model.TranscriptSegment{FromMS: int64(seg.Start * 1000), ToMS: int64(seg.End * 1000), Text: t})
		}
	}
	r.Text = joinText(r.Segments)
	if r.Text == "" {
		r.Text = strings.Join(strings.Fields(a.Text), " ")
	}
	return r, name, nil
}
