package api_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/api"
	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
)

func TestAudioCaptureRefusesPlannedRestartAndHandover(t *testing.T) {
	for _, route := range []string{"/api/shutdown", "/api/p2p/restart", "/api/fleet-ops/restart"} {
		t.Run(route, func(t *testing.T) {
			d := apitest.New(t)
			stopped := make(chan struct{}, 1)
			stop := func() { stopped <- struct{}{} }
			srv := httptest.NewServer(api.New(api.Options{Fleet: d.Fleet, OwnerToken: d.OwnerToken, Recorder: d.Recorder, Shutdown: stop, Restart: stop}).Handler())
			defer srv.Close()
			owner := client{t, srv.URL, d.OwnerToken}
			actor := owner
			if route == "/api/fleet-ops/restart" {
				actor.token = d.AgentToken(t, "head")
			}
			if code := owner.do("POST", "/api/audio/capture", map[string]any{"id": "phone-clip", "active": true}, nil); code != 200 {
				t.Fatal(code)
			}
			if code := actor.do("POST", route, nil, nil); code != 409 {
				t.Fatalf("restart while capturing: %d", code)
			}
			select {
			case <-stopped:
				t.Fatal("refused restart called the hook")
			default:
			}
			if code := actor.do("POST", route, map[string]bool{"force": true}, nil); code != 202 {
				t.Fatalf("forced restart: %d", code)
			}
			if code := owner.do("POST", "/api/audio/capture", map[string]any{"id": "new-clip", "active": true}, nil); code != 409 {
				t.Fatalf("new capture during handover: %d", code)
			}
			select {
			case <-stopped:
			case <-time.After(3 * time.Second):
				t.Fatal("forced restart never called hook")
			}
		})
	}
}

func TestRestartNoticeAccessAndReload(t *testing.T) {
	d := apitest.New(t)
	owner := client{t, d.URL, d.OwnerToken}
	head := client{t, d.URL, d.AgentToken(t, "head")}
	var notice struct {
		At int64 `json:"at"`
	}
	if code := owner.do("POST", "/api/restart-notice", map[string]int{"seconds": 60}, &notice); code != 200 {
		t.Fatal(code)
	}
	if delta := notice.At - time.Now().UnixMilli(); delta < 58_000 || delta > 60_000 {
		t.Fatalf("notice deadline: %d", delta)
	}
	var read struct {
		At int64 `json:"at"`
	}
	if code := owner.do("GET", "/api/restart-notice", nil, &read); code != 200 || read.At != notice.At {
		t.Fatalf("reload notice: %d %+v", code, read)
	}
	if code := head.do("POST", "/api/audio/capture", map[string]any{"id": "fake", "active": true}, nil); code != 403 {
		t.Fatalf("agent invented capture: %d", code)
	}
	if code := head.do("POST", "/api/fleet-ops/restart-notice", map[string]int{"seconds": 0}, nil); code != 200 {
		t.Fatalf("installer cancellation: %d", code)
	}
	if code := owner.do("GET", "/api/restart-notice", nil, &read); code != 200 || read.At != 0 {
		t.Fatalf("cancel: %d %+v", code, read)
	}
}

func TestActiveRecordingRefusesHandover(t *testing.T) {
	d := apitest.New(t)
	srv := httptest.NewServer(api.New(api.Options{Fleet: d.Fleet, OwnerToken: d.OwnerToken, Recorder: d.Recorder, Shutdown: func() { t.Error("must not stop") }}).Handler())
	defer srv.Close()
	owner := client{t, srv.URL, d.OwnerToken}
	if code := owner.do("POST", "/api/recordings", map[string]any{"kind": "audio", "mic": true, "mime": "audio/webm"}, nil); code != 201 {
		t.Fatal(code)
	}
	if code := owner.do("POST", "/api/shutdown", nil, nil); code != 409 {
		t.Fatalf("recording handover: %d", code)
	}
}
