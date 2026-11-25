package term

import (
	"testing"
	"time"
)

func TestInterruptWaitsForInputTransaction(t *testing.T) {
	p, bp := newGuardedTestProcess(t)
	close(bp.release)
	p.w.inputMu.Lock()
	done := make(chan error, 1)
	go func() { done <- p.Interrupt() }()
	select {
	case err := <-done:
		p.w.inputMu.Unlock()
		t.Fatalf("interrupt bypassed paste lock: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	p.w.inputMu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("interrupt stayed locked")
	}
	if got := <-bp.writes; string(got) != "\x1b" {
		t.Fatalf("interrupt bytes %q", got)
	}
}
