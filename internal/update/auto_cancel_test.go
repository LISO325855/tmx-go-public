package update

import (
	"context"
	"encoding/json"
	"github.com/blackcode-ch/tmx-go/internal/config"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReviewAutoOffCancelsPendingInstall(t *testing.T) {
	g := newFakeGitHub(t)
	key, pub := newKey(t)
	g.publish("stable", "fleet-v0.2.0", key, map[string][]byte{"tmxg_fleet-v0.2.0_windows_amd64.exe": []byte("binary fleet-v0.2.0")})
	exe := filepath.Join(t.TempDir(), "tmxg.exe")
	writeExe(t, exe, "fleet-v0.1.1")
	h := newHarness(t, g, pub, "fleet-v0.1.1", exe, "windows", false)
	var mu sync.Mutex
	busy := true
	h.busy = func() []string {
		mu.Lock()
		defer mu.Unlock()
		if busy {
			return []string{"active-agent"}
		}
		return nil
	}
	h.u.checkRelease(context.Background(), true)
	waitState(t, h.u, StateReady)
	off := false
	if st, err := h.u.Configure(&off, nil); err != nil || st.Auto {
		t.Fatalf("disable: %+v %v", st, err)
	}
	mu.Lock()
	busy = false
	mu.Unlock()
	select {
	case <-h.restarts:
		t.Fatalf("automatic install restarted AFTER auto off; status=%+v", h.u.Status())
	case <-time.After(500 * time.Millisecond):
	}
	if b, _ := os.ReadFile(exe); string(b) != "binary fleet-v0.1.1" {
		t.Fatal("binary changed after auto off")
	}
}
func TestReviewEmptyTrustSurvivesSave(t *testing.T) {
	for _, raw := range []string{`{"fleet_keys":[]}`, `{"require":[]}`} {
		var cfg config.UpdateConfig
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			t.Fatal(err)
		}
		u := &Updater{o: Options{Settings: func() config.UpdateConfig { return cfg }}}
		ks, err := u.trustedKeys()
		if err != nil || !ks.empty() {
			t.Fatalf("initial trust %+v %v", ks, err)
		}
		data, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		cfg = config.UpdateConfig{}
		if err = json.Unmarshal(data, &cfg); err != nil {
			t.Fatal(err)
		}
		ks, err = u.trustedKeys()
		if err != nil || !ks.empty() {
			t.Errorf("%s: save/reload restores compiled key; serialized=%s count=%d err=%v", raw, data, len(ks.keys), err)
		}
	}
}
func TestReviewBootstrapTrustVisible(t *testing.T) {
	u := &Updater{}
	if got := u.Keys(); len(got.FleetKeys) != 1 || got.Origin != "built-in" {
		t.Fatalf("effective bootstrap key missing from trust report: %+v", got)
	}
}

type cancelTestTransport func(*http.Request) (*http.Response, error)

func (f cancelTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAutoOffCancelsDownloadButManualStillInstalls(t *testing.T) {
	g := newFakeGitHub(t)
	key, pub := newKey(t)
	g.publish("stable", "fleet-v0.2.0", key, map[string][]byte{"tmxg_fleet-v0.2.0_windows_amd64.exe": []byte("binary fleet-v0.2.0")})
	exe := filepath.Join(t.TempDir(), "tmxg.exe")
	writeExe(t, exe, "fleet-v0.1.1")
	h := newHarness(t, g, pub, "fleet-v0.1.1", exe, "windows", false)
	started, cancelled := make(chan struct{}), make(chan struct{})
	var once sync.Once
	h.u.o.HTTP = &http.Client{Transport: cancelTestTransport(func(r *http.Request) (*http.Response, error) {
		block := false
		if strings.HasSuffix(r.URL.Path, ".exe") {
			once.Do(func() { block = true })
		}
		if block {
			close(started)
			<-r.Context().Done()
			close(cancelled)
			return nil, r.Context().Err()
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
	h.u.checkRelease(context.Background(), true)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("download never started")
	}
	off := false
	if _, err := h.u.Configure(&off, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("auto-off did not cancel download")
	}
	waitState(t, h.u, StateAvailable)
	select {
	case <-h.restarts:
		t.Fatal("cancelled auto install restarted")
	default:
	}
	h.u.Install(true)
	select {
	case <-h.restarts:
	case <-time.After(5 * time.Second):
		t.Fatalf("manual install blocked by auto off: %+v", h.u.Status())
	}
	if b, _ := os.ReadFile(exe); string(b) != "binary fleet-v0.2.0" {
		t.Fatal("manual did not install")
	}
}

func TestAutoOffDropsQueuedRequest(t *testing.T) {
	_, pub := newKey(t)
	exe := filepath.Join(t.TempDir(), "tmxg.exe")
	writeExe(t, exe, "fleet-v0.1.1")
	on := true
	cfg := config.UpdateConfig{Auto: &on}
	u := New(Options{Paths: config.Paths{Home: t.TempDir()}, Version: "fleet-v0.1.1", Executable: exe, Keys: []string{pub}, GOOS: "windows", GOARCH: "amd64", Settings: func() config.UpdateConfig { return cfg }, Save: func(c config.UpdateConfig) error { cfg = c; return nil }})
	u.queueInstall(installRequest{automatic: true})
	off := false
	if _, err := u.Configure(&off, nil); err != nil {
		t.Fatal(err)
	}
	u.runInstall(context.Background(), <-u.installs)
	if st := u.Status(); st.State != StateIdle {
		t.Fatalf("cancelled queued request ran: %+v", st)
	}
	u.queueInstall(installRequest{now: true})
	u.queueInstall(installRequest{automatic: true})
	if req := <-u.installs; req.automatic || !req.now {
		t.Fatal("automatic request replaced manual")
	}
}

func TestAutoOffAtLastIdleCheckPreventsSwap(t *testing.T) {
	g := newFakeGitHub(t)
	key, pub := newKey(t)
	g.publish("stable", "fleet-v0.2.0", key, map[string][]byte{"tmxg_fleet-v0.2.0_windows_amd64.exe": []byte("binary fleet-v0.2.0")})
	exe := filepath.Join(t.TempDir(), "tmxg.exe")
	writeExe(t, exe, "fleet-v0.1.1")
	h := newHarness(t, g, pub, "fleet-v0.1.1", exe, "windows", false)
	calls := 0
	off := false
	h.busy = func() []string {
		calls++
		if calls == 2 {
			if _, err := h.u.Configure(&off, nil); err != nil {
				panic(err)
			}
		}
		return nil
	}
	h.u.checkRelease(context.Background(), true)
	waitState(t, h.u, StateAvailable)
	select {
	case <-h.restarts:
		t.Fatal("auto-off at idle admission still swapped")
	case <-time.After(100 * time.Millisecond):
	}
	if b, _ := os.ReadFile(exe); string(b) != "binary fleet-v0.1.1" {
		t.Fatal("binary changed")
	}
}

func TestAutoOffDuringHandoverPreventsSwap(t *testing.T) {
	g := newFakeGitHub(t)
	key, pub := newKey(t)
	g.publish("stable", "fleet-v0.2.0", key, map[string][]byte{"tmxg_fleet-v0.2.0_windows_amd64.exe": []byte("binary fleet-v0.2.0")})
	exe := filepath.Join(t.TempDir(), "tmxg.exe")
	writeExe(t, exe, "fleet-v0.1.1")
	h := newHarness(t, g, pub, "fleet-v0.1.1", exe, "windows", false)
	entered, release := make(chan struct{}), make(chan struct{})
	h.u.o.PrepareRestart = func() (func(), error) { close(entered); <-release; return func() {}, nil }
	h.u.checkRelease(context.Background(), true)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no handover")
	}
	off := false
	_, err := h.u.Configure(&off, nil)
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, h.u, StateAvailable)
	select {
	case <-h.restarts:
		t.Fatal("disabled auto swapped after handover")
	default:
	}
	if b, _ := os.ReadFile(exe); string(b) != "binary fleet-v0.1.1" {
		t.Fatal("binary changed")
	}
}
