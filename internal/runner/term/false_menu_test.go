package term

import (
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
	"testing"
	"time"
)

// Exact stored false approval from hosta-go, 18 Nov 19:57:15 local time.
const falseIncomingMenu = " Baked for 32s  done 19:57\r\n\u276f [message from bb-sales-lead-o@hostc (other\r\n  machine)]\r\n   Thanks, script received\r\n"

func TestIncomingFleetMessageNeverCreatesTerminalMenu(t *testing.T) {
	for _, text := range []string{falseIncomingMenu, "\u2500\u2500\u2500\u2500\r\n" + falseIncomingMenu, falseIncomingMenu + claudeBox("")} {
		p, _ := newGuardedTestProcess(t)
		p.w.scr.Write([]byte(text))
		if d := p.w.classify(); d.State.Menu() {
			t.Fatalf("message became menu: %+v", d)
		}
		p.w.evaluate()
		p.w.evaluate()
		p.w.evaluate()
		for len(p.events) > 0 {
			if ev := <-p.events; ev.Kind == runner.EvApproval {
				t.Fatalf("false approval: %+v", ev)
			}
		}
	}
}

func TestWithdrawnFalseMenuReturnsToIdlePrompt(t *testing.T) {
	p, _ := newGuardedTestProcess(t)
	p.state = model.StateWaiting
	p.w.pending = &pendingMenu{id: "false-menu", sig: "old false menu"}
	p.w.scr.Write([]byte(falseIncomingMenu + claudeBox("")))
	p.w.idleSince = time.Now().Add(-time.Second)
	for i := 0; i < offMenuSettle; i++ {
		p.w.evaluate()
	}
	if p.state != model.StateIdle || p.w.pending != nil {
		t.Fatalf("state %s pending %+v", p.state, p.w.pending)
	}
	cancelled, idle := false, false
	for len(p.events) > 0 {
		ev := <-p.events
		if ev.Kind == runner.EvApprovalCancelled && ev.Approval.ID == "false-menu" {
			cancelled = true
		}
		if ev.Kind == runner.EvState && ev.State == model.StateIdle {
			idle = true
		}
	}
	if !cancelled || !idle {
		t.Fatalf("withdrawn=%v idle=%v", cancelled, idle)
	}
}
