package term

import (
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
	"testing"
	"time"
)

func TestIdleScreenReportsAgainWithoutStateChange(t *testing.T) {
	p, _ := newGuardedTestProcess(t)
	p.w.scr.Write([]byte(claudeBox("")))
	p.w.idleSince = time.Now().Add(-time.Minute)
	p.w.idleReported = time.Now().Add(-3 * time.Second)
	p.w.evaluate()
	select {
	case ev := <-p.events:
		if ev.Kind != runner.EvState || ev.State != model.StateIdle {
			t.Fatal(ev)
		}
	default:
		t.Fatal("idle observation not repeated")
	}
	p.w.evaluate()
	select {
	case ev := <-p.events:
		t.Fatalf("heartbeat flooded: %+v", ev)
	default:
	}
}

func TestWaitingTerminalInputsAreNotCombined(t *testing.T) {
	p, bp := newGuardedTestProcess(t)
	p.done = make(chan struct{})
	defer close(p.done)
	close(bp.release)
	p.w.scr.Write([]byte(claudeBox("")))
	p.w.idleSince = time.Now().Add(-time.Minute)
	p.w.queued = []mailInput{{text: "first", ids: []string{"1"}}, {text: "second", ids: []string{"2"}}}
	p.w.evaluate()
	select {
	case b := <-bp.writes:
		if string(b) != "first" {
			t.Fatalf("combined: %q", b)
		}
	case <-time.After(time.Second):
		t.Fatal("no paste")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.w.queued) != 1 || p.w.queued[0].text != "second" {
		t.Fatal(p.w.queued)
	}
}
