package fleet

import (
	"context"
	"fmt"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
	"github.com/blackcode-ch/tmx-go/internal/store"
	"strings"
	"testing"
)

func TestIdleScreenRepairsMenuAndDeliversOwnerSeparately(t *testing.T) {
	e := newUnguardedEnv(t)
	f := e.started()
	a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: workerProject(t, f), Name: "queue-probe", Harness: model.HarnessCodex, Mode: model.ModeTerminal})
	if err != nil {
		t.Fatal(err)
	}
	p := e.term.Last(a.ID)
	p.Emit(runner.Event{Kind: runner.EvApproval, Approval: &runner.ApprovalRequest{ID: "stale-menu", Tool: model.ToolTerminalMenu}})
	eventually(t, "waiting", func() bool { x, _ := f.Agent(a.ID); return x.State == model.StateWaiting })
	before := len(p.Sent())
	f.mu.Lock()
	f.agents[a.ID].proc = nil // only physical unavailability queues owner mail
	f.mu.Unlock()
	var ids []string
	for _, body := range []string{"first unique owner turn", "second unique owner turn"} {
		m, err := f.SendOwner(a.ID, body, "")
		if err != nil || m.State != model.MsgQueued {
			t.Fatalf("queue: %+v %v", m, err)
		}
		saved, err := f.st.GetMessage(m.ID)
		if err != nil || saved.Body != body || saved.State != model.MsgQueued {
			t.Fatalf("not saved: %+v %v", saved, err)
		}
		ids = append(ids, m.ID)
	}
	if len(p.Sent()) != before {
		t.Fatalf("owner mail bypassed the menu: %q", p.Sent())
	}
	f.mu.Lock()
	f.agents[a.ID].proc = p
	f.mu.Unlock()
	// Cancellation was missed, but a positive screen observation must repair it.
	// Each already queued owner turn is delivered in order after an idle event.
	p.State(model.StateIdle)
	eventually(t, "first owner message", func() bool { return len(p.Sent()) == before+1 })
	p.State(model.StateIdle)
	eventually(t, "both owner messages delivered separately", func() bool { return len(p.Sent()) >= before+2 })
	sent := p.Sent()
	if len(sent) != before+2 ||
		!strings.Contains(sent[before], "first unique") || strings.Contains(sent[before], "second unique") ||
		!strings.Contains(sent[before+1], "second unique") || strings.Contains(sent[before+1], "first unique") {
		t.Fatalf("owner messages combined, reordered or duplicated: %q", sent)
	}
	for _, id := range ids {
		m, err := f.st.GetMessage(id)
		if err != nil || m.State == model.MsgQueued {
			t.Fatalf("owner message still queued: %+v %v", m, err)
		}
	}
	pending, _ := f.Approvals(model.ApprovalPending)
	if len(pending) != 0 {
		t.Fatal(pending)
	}
	p.State(model.StateIdle)
	eventually(t, "idle after delivery", func() bool { x, _ := f.Agent(a.ID); return x.State == model.StateIdle })
	f.tryDeliver(a.ID)
	if len(p.Sent()) != before+2 {
		t.Fatalf("owner messages replayed on idle: %q", p.Sent())
	}
}

func TestOwnerQueueSurvivesRestartAndKeepsOldest(t *testing.T) {
	e := newUnguardedEnv(t)
	f := e.started()
	a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: workerProject(t, f), Name: "persist", Mode: model.ModeTerminal})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.agents[a.ID].a.State = model.StateWaiting
	proc := f.agents[a.ID].proc
	f.agents[a.ID].proc = nil
	f.mu.Unlock()
	for i := 0; i < 35; i++ {
		if _, err := f.SendOwner(a.ID, fmt.Sprintf("owner-%02d", i), ""); err != nil {
			t.Fatal(err)
		}
	}
	f.mu.Lock()
	f.agents[a.ID].proc = proc
	f.mu.Unlock()
	if err := f.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	f = e.fleet()
	messages, err := f.st.ListMessages(store.MessageFilter{To: a.ID, State: model.MsgQueued, Limit: 1, Oldest: true})
	if err != nil || len(messages) != 1 || messages[0].Body != "owner-00" {
		t.Fatalf("oldest after restart: %+v %v", messages, err)
	}
	count, err := f.st.CountQueued(a.ID)
	if err != nil || count != 35 {
		t.Fatalf("queue after restart: %d %v", count, err)
	}
}
