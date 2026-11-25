package fleet

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
)

func TestMailRetryIdentityConcurrentAndPersistent(t *testing.T) {
	e := newUnguardedEnv(t)
	f := e.started()
	a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: workerProject(t, f), Name: "target"})
	if err != nil {
		t.Fatal(err)
	}
	e.chat.Last(a.ID).State(model.StateIdle)
	waitState(t, f, a.ID, model.StateIdle)
	request := SendRequest{To: a.ID, Body: "one effect", RetryID: "request-one"}
	const count = 12
	results := make(chan model.Message, count)
	errors := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); m, err := f.SendMessage(OwnerCaller, request); results <- m; errors <- err }()
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	id := ""
	for m := range results {
		if id == "" {
			id = m.ID
		}
		if m.ID != id {
			t.Fatal("retry created a new message")
		}
	}
	if n := len(e.chat.Last(a.ID).Sent()); n != 1 {
		t.Fatalf("effects %d", n)
	}
	changed := request
	changed.Body = "different"
	if _, err := f.SendMessage(OwnerCaller, changed); KindOf(err) != KindConflict {
		t.Fatalf("changed body: %v", err)
	}
	changed = request
	changed.ThreadID = "different"
	if _, err := f.SendMessage(OwnerCaller, changed); KindOf(err) != KindConflict {
		t.Fatalf("changed thread: %v", err)
	}
	if err := f.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	f = e.fleet()
	m, err := f.SendMessage(OwnerCaller, request)
	if err != nil || m.ID != id || m.Receipt != "accepted" {
		t.Fatalf("restart: %+v %v", m, err)
	}
}

func TestMailRetryScopeReplyAndReceipts(t *testing.T) {
	e := newUnguardedEnv(t)
	f := e.started()
	a, _ := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: workerProject(t, f), Name: "sender"})
	b, _ := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: workerProject(t, f), Name: "receiver", Mode: model.ModeTerminal})
	f.mu.Lock()
	f.agents[b.ID].a.State = model.StateWorking
	f.mu.Unlock()
	req := SendRequest{To: b.ID, Body: "question", RetryID: "shared"}
	m, err := f.SendMessage(AgentCaller(a.ID), req)
	if err != nil || m.Receipt != "accepted" {
		t.Fatalf("queued: %+v %v", m, err)
	}
	owner, err := f.SendMessage(OwnerCaller, req)
	if err != nil || owner.ID == m.ID {
		t.Fatalf("sender scope: %+v %v", owner, err)
	}
	f.mu.Lock()
	f.agents[b.ID].a.State = model.StateIdle
	f.mu.Unlock()
	f.tryDeliver(b.ID)
	again, err := f.SendMessage(AgentCaller(a.ID), req)
	if err != nil || again.ID != m.ID || again.Receipt != "accepted" {
		t.Fatalf("retry after effect: %+v %v", again, err)
	}
	// Two writes, each once: the owner's message was typed at once into the working agent, the
	// agent's mail when it became idle; the retry added none.
	if sent := e.term.Last(b.ID).Sent(); len(sent) != 2 || !strings.Contains(sent[1], "question") || strings.Count(strings.Join(sent, "\n"), "question") != 2 {
		t.Fatalf("writes: %q", sent)
	}
	replyReq := SendRequest{ReplyTo: m.ID, Body: "answer", RetryID: "reply-one"}
	reply, err := f.SendMessage(AgentCaller(b.ID), replyReq)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := f.SendMessage(AgentCaller(b.ID), replyReq)
	if err != nil || repeated.ID != reply.ID {
		t.Fatalf("reply duplicated: %+v %v", repeated, err)
	}
	_, _ = f.Messages(AgentCaller(b.ID), "", 0)
	p := e.term.Last(b.ID)
	f.handleEvent(f.agents[b.ID], p, runner.Event{Kind: runner.EvMailDelivery, MessageIDs: []string{m.ID}, Delivery: "submitted"})
	read, _ := f.message(m.ID)
	if read.Receipt != "read" {
		t.Fatal("late receipt regressed recipient read")
	}
	visible, _ := f.Messages(OwnerCaller, b.ID, 0)
	found := false
	for _, v := range visible {
		if v.ID == m.ID {
			found = v.Receipt == "read" && v.ReceiptAt > 0
		}
	}
	if !found {
		t.Fatal("owner has no receipt")
	}
}

func TestMailUncertainEffectNeverReplayed(t *testing.T) {
	e := newUnguardedEnv(t)
	f := e.started()
	a, _ := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: workerProject(t, f), Name: "target"})
	req := SendRequest{To: a.ID, Body: "effect", RetryID: "uncertain"}
	m, err := f.SendMessage(OwnerCaller, req)
	if err != nil {
		t.Fatal(err)
	}
	f.mailUnconfirmed(f.agents[a.ID], []model.Message{m}, context.Canceled)
	again, err := f.SendMessage(OwnerCaller, req)
	if err != nil || again.ID != m.ID || again.Receipt != "unconfirmed" {
		t.Fatalf("uncertain retry: %+v %v", again, err)
	}
	f.mu.Lock()
	f.agents[a.ID].a.State = model.StateIdle
	f.mu.Unlock()
	f.tryDeliver(a.ID)
	if len(e.chat.Last(a.ID).Sent()) != 1 {
		t.Fatal("uncertain effect replayed")
	}
}

func TestMailProcessExitClosesPendingReceipt(t *testing.T) {
	e := newUnguardedEnv(t)
	f := e.started()
	a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: workerProject(t, f), Name: "target"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := f.SendMessage(OwnerCaller, SendRequest{To: a.ID, Body: "pending", RetryID: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.SetMessageReceipt(m.ID, "dispatching", model.Now()); err != nil {
		t.Fatal(err)
	}
	e.chat.Last(a.ID).Exit(0, nil)
	eventually(t, "uncertain receipt after exit", func() bool { got, _ := f.message(m.ID); return got.Receipt == "unconfirmed" })
}

func TestMenuWaitRequeuedOnProcessExitAndDeliveredAfterStart(t *testing.T) {
	e := newUnguardedEnv(t)
	f := e.started()
	a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: workerProject(t, f), Name: "menu-restart", Harness: model.HarnessClaude, Mode: model.ModeTerminal})
	if err != nil {
		t.Fatal(err)
	}
	// This is the durable receipt emitted when Esc failed before any paste.
	m := model.Message{ID: "unpasted-menu", From: model.Owner, To: a.ID, Body: "preserved owner words", State: model.MsgDelivered, Receipt: "menu_wait", CreatedAt: model.Now(), DeliveredAt: model.Now()}
	if err := e.st.PutMessage(m); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.agents[a.ID].a.Desired = "stopped"
	f.mu.Unlock()
	old := e.term.Last(a.ID)
	old.Exit(0, nil)
	eventually(t, "menu message restored after exit", func() bool {
		got, _ := f.message(m.ID)
		agent, _ := f.Agent(a.ID)
		return got.State == model.MsgQueued && got.Receipt == "queued" && got.DeliveredAt == 0 && agent.Queued == 1
	})
	if _, err := f.StartAgent(OwnerCaller, a.ID); err != nil {
		t.Fatal(err)
	}
	next := e.term.Last(a.ID)
	if next == old {
		t.Fatal("process not restarted")
	}
	next.State(model.StateIdle)
	eventually(t, "recovered menu message delivered", func() bool {
		for _, text := range next.Sent() {
			if strings.Contains(text, m.Body) {
				return true
			}
		}
		return false
	})
	// The text reaches the process before the send is confirmed and the message marked.
	eventually(t, "recovered menu message marked delivered", func() bool {
		got, _ := f.message(m.ID)
		return got.State == model.MsgDelivered
	})
}
