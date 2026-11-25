package update

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/codexguard"
	"github.com/blackcode-ch/tmx-go/internal/rollback"
)

func TestWindowsConcurrentInstallsHandOffOnceAfterPinTeardown(t *testing.T) {
	g := newFakeGitHub(t)
	key, pub := newKey(t)
	g.publish("stable", "fleet-v0.2.0", key, map[string][]byte{
		"tmxg_fleet-v0.2.0_windows_amd64.exe": []byte("binary fleet-v0.2.0"),
	})
	exe := filepath.Join(t.TempDir(), "tmxg.exe")
	writeExe(t, exe, "fleet-v0.1.1")
	h := newHarness(t, g, pub, "fleet-v0.1.1", exe, "windows", false)
	pin, err := codexguard.OpenPin(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer pin.Close()
	pending := make(chan func() (string, error), 1)
	allowReturn := make(chan struct{})
	h.u.o.DeferSwap = func(apply func() (string, error)) bool {
		pending <- apply
		<-allowReturn
		return true
	}
	defer close(allowReturn)
	if got := h.u.Check(context.Background()); got.State != StateAvailable {
		t.Fatalf("check: %+v", got)
	}
	h.u.Install(true)
	var apply func() (string, error)
	select {
	case apply = <-pending:
	case <-time.After(5 * time.Second):
		t.Fatalf("swap was never handed off: %+v", h.u.Status())
	}
	second := make(chan struct{})
	go func() {
		h.u.install(context.Background(), installRequest{now: true})
		close(second)
	}()
	select {
	case <-second:
	case <-time.After(5 * time.Second):
		t.Fatal("second install blocked behind a pending handoff")
	}
	if err := pin.Verify(); err != nil {
		t.Fatalf("old executable reached swap before teardown: %v", err)
	}
	if _, ok, err := rollback.OnProbation(h.u.checkpoint()); ok || err != nil {
		t.Fatalf("probation started before the swap: %v, %v", ok, err)
	}
	if err := pin.Close(); err != nil {
		t.Fatal(err)
	}
	applied := make(chan error, 1)
	go func() {
		_, err := apply()
		applied <- err
	}()
	select {
	case err := <-applied:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("applying handoff waited for the submitting install")
	}
	if b, err := os.ReadFile(exe); string(b) != "binary fleet-v0.2.0" || err != nil {
		t.Fatalf("updated executable: %q, %v", b, err)
	}
	p, ok, err := rollback.OnProbation(h.u.checkpoint())
	if !ok || err != nil || p.To != "fleet-v0.2.0" {
		t.Fatalf("updated probation: %+v, %v, %v", p, ok, err)
	}
}
