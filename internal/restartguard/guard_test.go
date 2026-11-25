package restartguard

import (
	"errors"
	"testing"
	"time"
)

func TestCaptureRefusesRestartUnlessForced(t *testing.T) {
	g := &Guard{}
	if err := g.Capture("phone", true); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Reserve(false, nil); !errors.Is(err, ErrCapturing) {
		t.Fatalf("restart during dictation: %v", err)
	}
	release, err := g.Reserve(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Capture("desktop", true); !errors.Is(err, ErrRestarting) {
		t.Fatalf("capture during handover: %v", err)
	}
	release()
	if err := g.Capture("phone", false); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Reserve(false, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRecordingAndExpiredLeases(t *testing.T) {
	g := &Guard{leases: map[string]time.Time{"closed-tab": time.Now().Add(-time.Second)}}
	if _, err := g.Reserve(false, func() bool { return true }); !errors.Is(err, ErrCapturing) {
		t.Fatalf("native recording: %v", err)
	}
	if _, err := g.Reserve(false, func() bool { return false }); err != nil {
		t.Fatalf("expired tab: %v", err)
	}
	called := false
	if err := g.Admit(func() { called = true }); !errors.Is(err, ErrRestarting) || called {
		t.Fatalf("recording admitted after restart: %v %t", err, called)
	}
}

func TestIndependentWindows(t *testing.T) {
	g := &Guard{}
	_ = g.Capture("phone", true)
	_ = g.Capture("desktop", true)
	_ = g.Capture("phone", false)
	if _, err := g.Reserve(false, nil); !errors.Is(err, ErrCapturing) {
		t.Fatalf("phone stop cleared desktop: %v", err)
	}
}
