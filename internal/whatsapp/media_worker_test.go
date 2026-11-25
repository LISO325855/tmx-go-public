package whatsapp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/whatsapp"
	"github.com/blackcode-ch/tmx-go/internal/whatsapp/watest"
)

func mediaFixtures(t *testing.T, f *watest.Fake) []whatsapp.Incoming {
	t.Helper()
	b, err := os.ReadFile("testdata/media_messages.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		ID       string              `json:"id"`
		Type     model.WaMessageType `json:"type"`
		FromMe   bool                `json:"from_me"`
		Mime     string              `json:"mime"`
		Duration int64               `json:"duration_ms"`
		Data     string              `json:"data"`
	}
	if err := json.Unmarshal(b, &fixtures); err != nil {
		t.Fatal(err)
	}
	var out []whatsapp.Incoming
	for _, x := range fixtures {
		f.Media["/"+x.ID] = []byte(x.Data)
		out = append(out, whatsapp.Incoming{Msg: model.WaMessage{ID: x.ID, ChatJID: anna, SenderJID: anna, FromMe: x.FromMe, Type: x.Type, At: time.Now().Add(-time.Hour).UnixMilli(),
			Media: &model.WaMedia{Mime: x.Mime, DurationMS: x.Duration, Size: int64(len(x.Data))}},
			Ref: &whatsapp.MediaRef{DirectPath: "/" + x.ID, Kind: string(x.Type), Size: int64(len(x.Data))}})
	}
	return out
}

func awaitMedia(t *testing.T, check func() bool) {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("media work did not finish")
}

func TestAutoMediaFixtures(t *testing.T) {
	f := watest.Linked()
	msgs := mediaFixtures(t, f)
	var calls atomic.Int32
	e := newEnv(t, f, func(o *whatsapp.Options) {
		o.AutoMedia = true
		o.Transcribe = func(ctx context.Context, path string) (string, error) {
			if _, err := os.ReadFile(path); err != nil {
				return "", err
			}
			calls.Add(1)
			return "fixture speech", nil
		}
	})
	f.Emit(whatsapp.EvConnected{Account: watest.Self})
	f.Emit(whatsapp.EvHistory{Messages: msgs})
	awaitMedia(t, func() bool {
		for _, m := range msgs {
			r, err := e.st.WaGetMessage(anna, m.Msg.ID)
			if err != nil || r.MediaFile == "" {
				return false
			}
			if (r.Type == model.WaVoice || r.Type == model.WaAudio) && r.Media.Transcript == "" {
				return false
			}
		}
		return true
	})
	for _, m := range msgs {
		r, _ := e.st.WaGetMessage(anna, m.Msg.ID)
		b, err := os.ReadFile(filepath.Join(e.media, r.MediaFile))
		if err != nil || string(b) != string(f.Media["/"+m.Msg.ID]) {
			t.Fatalf("retained %s: %q, %v", m.Msg.ID, b, err)
		}
		if r.Type == model.WaVoice && whatsapp.Describe(r.WaMessage) != "[voice note, 0:35] fixture speech" {
			t.Fatal(whatsapp.Describe(r.WaMessage))
		}
	}
	// A duplicate history/live echo must retain the transcript and downloaded file.
	f.Emit(whatsapp.EvHistory{Messages: msgs})
	r, _ := e.st.WaGetMessage(anna, "outgoing")
	if r.Media.Transcript != "fixture speech" || calls.Load() != 3 {
		t.Fatalf("echo lost transcript or repeated transcription: %+v, %d", r.Media, calls.Load())
	}
}

func TestAutoMediaRetryAndRevoke(t *testing.T) {
	f := watest.Linked()
	msgs := mediaFixtures(t, f)
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	e := newEnv(t, f, func(o *whatsapp.Options) {
		o.AutoMedia = true
		o.Transcribe = func(ctx context.Context, path string) (string, error) {
			if calls.Add(1) == 1 {
				return "", errors.New("worker memory budget unavailable")
			}
			select {
			case <-started:
			default:
				close(started)
			}
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-release:
				return "must not return", nil
			}
		}
	})
	f.Emit(whatsapp.EvConnected{Account: watest.Self})
	f.Emit(whatsapp.EvMessage{In: msgs[0]})
	awaitMedia(t, func() bool {
		r, _ := e.st.WaGetMessage(anna, msgs[0].Msg.ID)
		return r.Media != nil && r.Media.TranscribeRetry != nil
	})
	f.Emit(whatsapp.EvMessage{In: msgs[0]})
	time.Sleep(30 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatal("retry ignored backoff")
	}
	e.clock.add(time.Minute + time.Second)
	f.Emit(whatsapp.EvMessage{In: msgs[0]}) // retry after temporary worker failure
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("retry did not start")
	}
	f.Emit(whatsapp.EvMessage{In: whatsapp.Incoming{Msg: model.WaMessage{ChatJID: anna}, Revoke: msgs[0].Msg.ID}})
	close(release)
	e.svc.Close()
	r, err := e.st.WaGetMessage(anna, msgs[0].Msg.ID)
	if err != nil || !r.Deleted || r.Media != nil || r.MediaFile != "" {
		t.Fatalf("revoked media restored: %+v %v", r, err)
	}
}

func TestAutoMediaRestartBacklog(t *testing.T) {
	f := watest.Linked()
	msgs := mediaFixtures(t, f)
	e := newEnv(t, f) // baseline stored references before automatic media existed
	f.Emit(whatsapp.EvHistory{Messages: msgs})
	e.svc.Close()
	next := watest.Linked()
	mediaFixtures(t, next)
	delete(next.Media, "/audio") // expired reference sorts before the remaining work
	// More than one batch, and an unavailable first item must not starve later ones.
	for i := 0; i < 40; i++ {
		m := msgs[2]
		m.Msg.ID = fmt.Sprintf("photo-%02d", i)
		r, err := e.st.WaGetMessage(anna, "photo")
		if err != nil {
			t.Fatal(err)
		}
		r.ID = m.Msg.ID
		if err := e.st.WaPutMessage(r); err != nil {
			t.Fatal(err)
		}
	}
	on := true
	svc := whatsapp.New(whatsapp.Options{Store: e.st, Client: next, MediaDir: e.media, AutoMedia: true,
		DiskFree:   func(string) (uint64, error) { return 20 << 30, nil },
		Config:     func() *config.WhatsAppConfig { return &config.WhatsAppConfig{Enabled: &on} },
		Transcribe: func(context.Context, string) (string, error) { return "recovered speech", nil }})
	t.Cleanup(svc.Close)
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	awaitMedia(t, func() bool {
		r, err := e.st.WaGetMessage(anna, "photo-00")
		v, verr := e.st.WaGetMessage(anna, "outgoing")
		return err == nil && verr == nil && r.MediaFile != "" && v.Media.Transcript == "recovered speech"
	})
}
