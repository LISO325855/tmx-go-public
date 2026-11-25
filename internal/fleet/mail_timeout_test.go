package fleet

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
)

type diagnosedMailProcess struct {
	runner.Process
	stage string
	since int64
}

func (*diagnosedMailProcess) MailStage(string) string { return "paste_write" }
func (*diagnosedMailProcess) ForgetMailStage(string)  {}
func (p *diagnosedMailProcess) MailProgress(string) (string, int64) {
	stage := p.stage
	if stage == "" {
		stage = "paste_write"
	}
	since := p.since
	if since == 0 {
		since = time.Now().Add(-2 * time.Minute).UnixMilli()
	}
	return stage, since
}

func TestOwnerDispatchTimeoutSurfacesOnceAndAllowsLateReceipt(t *testing.T) {
	e := newEnv(t)
	var logs bytes.Buffer
	f, err := New(Options{Store: e.st, Paths: config.Paths{Home: e.home}, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close(context.Background())
	f.agents["target"] = &agentRec{a: model.Agent{ID: "target"}, proc: &diagnosedMailProcess{}}
	now := time.Now()
	for _, tc := range []struct {
		id, from, receipt string
		age               time.Duration
	}{
		{"old", "owner", "dispatching", 61 * time.Second},
		{"boundary", "owner", "dispatching", 60 * time.Second},
		{"recent", "owner", "dispatching", 59 * time.Second},
		{"submitted", "owner", "submitted", 2 * time.Minute},
		{"agent-mail", "another-agent", "dispatching", 2 * time.Minute},
	} {
		m := model.Message{ID: tc.id, From: tc.from, To: "target", Body: "PRIVATE SENTINEL", State: model.MsgDelivered, Receipt: tc.receipt, ReceiptAt: now.Add(-tc.age).UnixMilli(), CreatedAt: now.Add(-tc.age).UnixMilli()}
		if err := e.st.PutMessage(m); err != nil {
			t.Fatal(err)
		}
	}
	sub := f.Subscribe()
	defer f.Unsubscribe(sub)
	f.surfaceStalledOwnerMail(now)
	for id, want := range map[string]string{"old": "stalled", "boundary": "dispatching", "recent": "dispatching", "submitted": "submitted", "agent-mail": "dispatching"} {
		m, err := e.st.GetMessage(id)
		if err != nil || m.Receipt != want {
			t.Fatalf("%s: %+v %v", id, m, err)
		}
	}
	select {
	case ev := <-sub.C():
		m, ok := ev.Data.(model.Message)
		if ev.Type != model.EvMessageUpdated || !ok || m.ID != "old" || m.Receipt != "stalled" {
			t.Fatalf("event %+v", ev)
		}
	default:
		t.Fatal("chat received no timeout event")
	}
	f.surfaceStalledOwnerMail(now)
	if strings.Count(logs.String(), "owner message dispatch timed out") != 1 || !strings.Contains(logs.String(), "stage=paste_write") || strings.Contains(logs.String(), "PRIVATE SENTINEL") {
		t.Fatalf("diagnostic %s", logs.String())
	}
	f.mu.Lock()
	f.mailReceiptLocked("target", []string{"old"}, "submitted")
	f.mu.Unlock()
	f.surfaceStalledOwnerMail(now)
	got, _ := e.st.GetMessage("old")
	if got.Receipt != "submitted" {
		t.Fatalf("late receipt lost: %+v", got)
	}
	f.agents["target"].proc = nil // diagnostic stub owns no actual process
}

func TestOwnerDispatchWatchdogRunsWithoutDeliveryLoop(t *testing.T) {
	e := newEnv(t)
	f := e.fleet()
	f.agents["absent-process"] = &agentRec{a: model.Agent{ID: "absent-process"}, proc: &diagnosedMailProcess{}}
	defer func() { f.agents["absent-process"].proc = nil }()
	at := time.Now().Add(-61 * time.Second).UnixMilli()
	if err := e.st.PutMessage(model.Message{ID: "watchdog", From: "owner", To: "absent-process", Body: "private", State: model.MsgDelivered, Receipt: "dispatching", ReceiptAt: at, CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	sub := f.Subscribe()
	defer f.Unsubscribe(sub)
	f.startMailLoop()
	select {
	case ev := <-sub.C():
		m, ok := ev.Data.(model.Message)
		if !ok || m.ID != "watchdog" || m.Receipt != "stalled" {
			t.Fatalf("watchdog event %+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no timeout event from independent watchdog")
	}
	m, _ := e.st.GetMessage("watchdog")
	if m.State != model.MsgSending {
		t.Fatal("watchdog requeued the uncertain effect")
	}
	// Durable warning survives receipt recovery, including a later daemon restart.
	if err := e.st.RecoverMailReceipts(); err != nil {
		t.Fatal(err)
	}
	m, _ = e.st.GetMessage("watchdog")
	if m.Receipt != "stalled" {
		t.Fatal("recovery lost timeout")
	}
}

type reviewHeldProcess struct{ runner.Process }

func (*reviewHeldProcess) MailStage(string) string { return "runner_queue" }
func (*reviewHeldProcess) ForgetMailStage(string)  {}
func TestReviewLegitimateRunnerHoldMustNotWarn(t *testing.T) {
	e := newEnv(t)
	f := e.fleet()
	f.agents["held"] = &agentRec{a: model.Agent{ID: "held"}, proc: &reviewHeldProcess{}}
	defer func() { f.agents["held"].proc = nil }()
	now := time.Now()
	at := now.Add(-61 * time.Second).UnixMilli()
	if err := e.st.PutMessage(model.Message{ID: "held-mail", From: "owner", To: "held", Body: "private", State: model.MsgDelivered, Receipt: "dispatching", ReceiptAt: at, CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	f.surfaceStalledOwnerMail(now)
	m, _ := e.st.GetMessage("held-mail")
	if m.Receipt == "stalled" {
		t.Fatal("legitimate runner_queue hold falsely reported not delivered; original remains queued in runner")
	}
}

func TestOwnerTimeoutOnlyForAgedWriteStages(t *testing.T) {
	for _, tc := range []struct {
		stage string
		warn  bool
	}{
		{"runner_queue", false}, {"draft", false}, {"menu", false}, {"busy", false},
		{"runner_handoff", false}, {"submitted", false}, {"submit_delay", false}, {"future_unknown", false},
		{"shell_write", true}, {"input_lock_wait", true}, {"paste_write", true}, {"enter_write", true},
		{"submit_menu_check", true}, {"composer_recheck", true}, {"screen_check", true},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			e := newEnv(t)
			f := e.fleet()
			now := time.Now()
			at := now.Add(-2 * time.Minute).UnixMilli()
			d := &diagnosedMailProcess{stage: tc.stage, since: at}
			f.agents["target"] = &agentRec{a: model.Agent{ID: "target"}, proc: d}
			defer func() { f.agents["target"].proc = nil }()
			if err := e.st.PutMessage(model.Message{ID: "m", From: "owner", To: "target", State: model.MsgDelivered, Receipt: "dispatching", ReceiptAt: at, CreatedAt: at}); err != nil {
				t.Fatal(err)
			}
			f.surfaceStalledOwnerMail(now)
			m, _ := e.st.GetMessage("m")
			if (m.Receipt == "stalled") != tc.warn {
				t.Fatalf("%s receipt=%s", tc.stage, m.Receipt)
			}
		})
	}
}

func TestLongHoldDoesNotAgeTheFollowingWrite(t *testing.T) {
	e := newEnv(t)
	f := e.fleet()
	now := time.Now()
	old := now.Add(-10 * time.Minute).UnixMilli()
	d := &diagnosedMailProcess{stage: "runner_queue", since: old}
	f.agents["target"] = &agentRec{a: model.Agent{ID: "target"}, proc: d}
	defer func() { f.agents["target"].proc = nil }()
	if err := e.st.PutMessage(model.Message{ID: "m", From: "owner", To: "target", State: model.MsgDelivered, Receipt: "dispatching", ReceiptAt: old, CreatedAt: old}); err != nil {
		t.Fatal(err)
	}
	f.surfaceStalledOwnerMail(now)
	d.stage, d.since = "paste_write", now.UnixMilli()
	f.surfaceStalledOwnerMail(now.Add(60 * time.Second))
	m, _ := e.st.GetMessage("m")
	if m.Receipt != "dispatching" {
		t.Fatal("queue age became write timeout")
	}
	f.surfaceStalledOwnerMail(now.Add(61 * time.Second))
	m, _ = e.st.GetMessage("m")
	if m.Receipt != "stalled" {
		t.Fatal("genuinely blocked write was not surfaced")
	}
}
