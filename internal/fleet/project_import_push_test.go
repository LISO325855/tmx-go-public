package fleet

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// The owner's import never alerts the owner's phone: not for history (answered, closed), not
// for cards put aside, and not for imported open questions either. A question asked afterwards
// does. (An agent's import: TestAgentImportAlertsOnlyForItsOpenCard.)
func TestProjectImportSendsNoPush(t *testing.T) {
	_, f, p := sectionFixture(t)
	var pushes atomic.Int32
	pushed := make(chan string, 16)
	f.SetApprovalNotifier(func(ctx context.Context, ap model.Approval) (string, error) {
		pushes.Add(1)
		pushed <- ap.ID
		return "fake_phone", nil
	})

	req := importFixtureRequest() // one open, one later, one answered card
	receipt, err := f.ImportProject(OwnerCaller, p.ID, req)
	if err != nil || len(receipt.CardIDs) != 3 {
		t.Fatalf("import: %+v %v", receipt, err)
	}
	// The imported open card is in Needs you all the same.
	waiting, err := f.Approvals(model.ApprovalPending)
	if err != nil {
		t.Fatal(err)
	}
	open := 0
	for _, ap := range waiting {
		if ap.Card != nil && ap.Card.Origin != nil && ap.State == model.ApprovalPending && !ap.Deferred {
			open++
		}
	}
	if open != 1 {
		t.Fatalf("imported open cards waiting: %d, want 1", open)
	}

	// A genuinely new request made after the import still alerts, and it is the only one.
	fresh, err := f.AskProjectOwner(Caller{AgentID: "lead"}, p.ID, ProjectCardRequest{AskRequest: AskRequest{Question: "A new question, asked now?"}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-pushed:
		if id != fresh.ID {
			t.Fatalf("a push went out for %s, not for the new request %s", id, fresh.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the new request sent no push")
	}
	// The queue is one goroutine in order: the new request's push came first, so nothing of
	// the import was queued before it. Give stragglers a moment anyway.
	time.Sleep(200 * time.Millisecond)
	if n := pushes.Load(); n != 1 {
		t.Fatalf("pushes: %d, want exactly 1 (the new request)", n)
	}
}

func TestNotifiesOwner(t *testing.T) {
	imported := &model.OwnerCard{Origin: &model.ImportOrigin{Source: "python@hostb", ImportedBy: model.Owner}}
	byAgent := &model.OwnerCard{Origin: &model.ImportOrigin{Source: "python@hostb", ImportedBy: "lead"}}
	cases := []struct {
		name string
		ap   model.Approval
		want bool
	}{
		{"a new open request", model.Approval{State: model.ApprovalPending}, true},
		{"a new open card", model.Approval{State: model.ApprovalPending, Card: &model.OwnerCard{}}, true},
		{"put aside for later", model.Approval{State: model.ApprovalPending, Deferred: true}, false},
		{"already decided", model.Approval{State: model.ApprovalExpired}, false},
		{"imported by the owner and open", model.Approval{State: model.ApprovalPending, Card: imported}, false},
		{"imported history", model.Approval{State: model.ApprovalExpired, Card: imported}, false},
		{"imported by an agent and open", model.Approval{State: model.ApprovalPending, Card: byAgent}, true},
		{"imported by an agent, later", model.Approval{State: model.ApprovalPending, Deferred: true, Card: byAgent}, false},
		{"imported by an agent, history", model.Approval{State: model.ApprovalExpired, Card: byAgent}, false},
	}
	for _, tc := range cases {
		if got := notifiesOwner(tc.ap); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

// An import run by an agent (the project's lead) cannot put an open card into Needs you
// quietly: its one open card alerts once; its later and answered cards do not.
func TestAgentImportAlertsOnlyForItsOpenCard(t *testing.T) {
	_, f, p := sectionFixture(t)
	pushed := make(chan model.Approval, 16)
	f.SetApprovalNotifier(func(ctx context.Context, ap model.Approval) (string, error) {
		pushed <- ap
		return "fake_phone", nil
	})
	receipt, err := f.ImportProject(Caller{AgentID: "lead"}, p.ID, importFixtureRequest()) // open, later, answered
	if err != nil || len(receipt.CardIDs) != 3 {
		t.Fatalf("import: %+v %v", receipt, err)
	}
	select {
	case ap := <-pushed:
		if ap.State != model.ApprovalPending || ap.Deferred || ap.Card == nil || ap.Card.Origin == nil || ap.Card.Origin.ImportedBy != "lead" {
			t.Fatalf("the push was not for the lead's open imported card: %+v", ap)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the lead's open imported card sent no push")
	}
	select {
	case ap := <-pushed:
		t.Fatalf("a second push went out: %s (state %s, deferred %v)", ap.ID, ap.State, ap.Deferred)
	case <-time.After(300 * time.Millisecond):
	}
}
