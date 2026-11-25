package fleet

import (
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
)

func TestUrgentMailInterruptsBusyCodex(t *testing.T) {
	e := newUnguardedEnv(t)
	f := e.started()
	a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: workerProject(t, f), Name: "recipient", Harness: model.HarnessCodex, Mode: model.ModeChat})
	if err != nil {
		t.Fatal(err)
	}
	sender := mustAgent(t, f, "head")
	p := e.chat.Last(a.ID)
	p.State(model.StateWorking)
	waitState(t, f, a.ID, model.StateWorking)
	for _, req := range []SendRequest{{To: a.ID, Body: "ordinary"}, {To: a.ID, Body: "stop the task", Urgent: true}} {
		if _, err := f.SendMessage(AgentCaller(sender.ID), req); err != nil {
			t.Fatal(err)
		}
	}
	if p.Interrupts() != 1 || len(p.Sent()) != 0 {
		t.Fatalf("interrupts=%d sent=%q", p.Interrupts(), p.Sent())
	}
	p.State(model.StateIdle)
	eventually(t, "urgent mail sent after interruption", func() bool {
		return strings.Contains(lastSent(p), "stop the task") && !strings.Contains(lastSent(p), "ordinary")
	})
}

func TestAgentMachineDeclinesOwnerQuestions(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	head := mustAgent(t, f, "head")
	p := e.chat.Last(head.ID)
	f.mu.Lock()
	f.opts.AgentMachine = true
	f.addPermissionLocked(f.agents[head.ID], p, runner.ApprovalRequest{ID: "question", Kind: model.ApprovalQuestion})
	f.mu.Unlock()
	if _, err := f.AskOwner(AgentCaller(head.ID), AskRequest{Question: "Which plan?"}); KindOf(err) != KindConflict {
		t.Fatalf("ask_owner: %v", err)
	}
	eventually(t, "harness question declined", func() bool {
		a := p.Answers()
		return len(a) == 1 && a[0].RequestID == "question" && !a[0].Decision.Allow
	})
	if pending, err := f.Approvals(model.ApprovalPending); err != nil || len(pending) != 0 {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
}
