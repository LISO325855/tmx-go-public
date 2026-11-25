package update

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/accounts"
)

func TestReviewResetStartsAfterIdleCheck(t *testing.T) {
	g := newFakeGitHub(t)
	key, pub := newKey(t)
	g.publish("stable", "fleet-v0.2.0", key, map[string][]byte{"tmxg_fleet-v0.2.0_linux_amd64": []byte("binary fleet-v0.2.0")})
	exe := filepath.Join(t.TempDir(), "bin", "tmx")
	writeExe(t, exe, "fleet-v0.1.1")
	h := newHarness(t, g, pub, "fleet-v0.1.1", exe, "linux", false)
	var mu sync.Mutex
	claimActive := false
	h.busy = func() []string {
		mu.Lock()
		defer mu.Unlock()
		if claimActive {
			return []string{"account reset in progress"}
		}
		return nil
	}
	// A fake claim wins admission after the final idle sample. The final
	// reservation must fail, just as accounts.ReserveRestart does for a live lease.
	h.u.o.PrepareRestart = func() (func(), error) {
		mu.Lock()
		claimActive = true
		mu.Unlock()
		return nil, fmt.Errorf("account reset in progress; restart refused")
	}
	h.u.checkRelease(context.Background(), true)
	select {
	case <-h.restarts:
		t.Fatal("automatic update restarted while a claim admitted after the final idle check was active")
	case <-time.After(time.Second):
	}
	if got := h.u.Status(); got.State != StateError || !strings.Contains(got.Error, "account reset") {
		t.Fatalf("reservation failure not reported: %+v", got)
	}
	if b, _ := os.ReadFile(exe); string(b) != "binary fleet-v0.1.1" {
		t.Fatalf("binary changed: %q", b)
	}
}

func TestResetReservationRequiredForInstallNow(t *testing.T) {
	g := newFakeGitHub(t)
	key, pub := newKey(t)
	g.publish("stable", "fleet-v0.2.0", key, map[string][]byte{"tmxg_fleet-v0.2.0_linux_amd64": []byte("binary fleet-v0.2.0")})
	exe := filepath.Join(t.TempDir(), "bin", "tmx")
	writeExe(t, exe, "fleet-v0.1.1")
	h := newHarness(t, g, pub, "fleet-v0.1.1", exe, "linux", false)
	h.busy = func() []string { return []string{"account reset in progress"} }
	h.u.o.PrepareRestart = func() (func(), error) { return nil, fmt.Errorf("account reset in progress; restart refused") }
	if s := h.u.Check(context.Background()); s.State != StateAvailable {
		t.Fatalf("check: %+v", s)
	}
	h.u.Install(true)
	s := waitState(t, h.u, StateError)
	if !strings.Contains(s.Error, "account reset") {
		t.Fatal(s.Error)
	}
	if b, _ := os.ReadFile(exe); string(b) != "binary fleet-v0.1.1" {
		t.Fatalf("forced update changed binary: %q", b)
	}
	select {
	case <-h.restarts:
		t.Fatal("forced update restarted during claim")
	default:
	}
}

func TestResetReservationReleasedOnAbandonedUpdate(t *testing.T) {
	g := newFakeGitHub(t)
	key, pub := newKey(t)
	g.publish("stable", "fleet-v0.2.0", key, map[string][]byte{"tmxg_fleet-v0.2.0_linux_amd64": []byte("binary fleet-v0.2.0")})
	exe := filepath.Join(t.TempDir(), "bin", "tmx")
	writeExe(t, exe, "fleet-v0.1.1")
	h := newHarness(t, g, pub, "fleet-v0.1.1", exe, "linux", false)
	svc := &accounts.Service{}
	released := make(chan struct{})
	h.u.o.PrepareRestart = func() (func(), error) {
		release, err := svc.ReserveRestart()
		if err != nil {
			return nil, err
		}
		if unexpected, err := svc.ReserveRestart(); err == nil {
			unexpected()
			t.Error("reservation not held")
		}
		return func() { release(); close(released) }, nil
	}
	if err := os.Remove(h.u.o.Paths.DB()); err != nil {
		t.Fatal(err)
	}
	if s := h.u.Check(context.Background()); s.State != StateAvailable {
		t.Fatalf("check: %+v", s)
	}
	h.u.Install(true)
	s := waitState(t, h.u, StateError)
	if !strings.Contains(s.Error, "cannot prepare rollback") {
		t.Fatal(s.Error)
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("failed checkpoint retained reservation")
	}
	release, err := svc.ReserveRestart()
	if err != nil {
		t.Fatalf("restart reservation leaked: %v", err)
	}
	release()
	select {
	case <-h.restarts:
		t.Fatal("restarted without checkpoint")
	default:
	}
}
