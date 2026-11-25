package fleet

import (
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/guard"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/store"
)

// The owner or the head closes owner cards without an answer: by id, or every waiting card an
// import brought. No decision is recorded, no mail is sent, and nothing but questions is touched.
func TestOwnerCardsAreClosedWithoutAnAnswerAndWithoutMail(t *testing.T) {
	e, f, p := sectionFixture(t)
	lead := Caller{AgentID: "lead"}
	if _, err := f.ImportProject(lead, p.ID, importFixtureRequest()); err != nil { // one open imported card (one parked, one answered: both closed already)
		t.Fatal(err)
	}
	own1, err := f.AskOwner(lead, AskRequest{Question: "Own question one"})
	if err != nil {
		t.Fatal(err)
	}
	own2, err := f.AskProjectOwner(lead, p.ID, ProjectCardRequest{AskRequest: AskRequest{Question: "Own question two"}})
	if err != nil {
		t.Fatal(err)
	}
	f.agents["lead"].a.State = model.StateWorking
	heldRule(t, f, guard.RuleElevation) // a hold for this test, whatever the rule's default
	hold, err := f.Hold(lead, HoldRequest{Tool: "Bash", Command: "sudo true", Rule: guard.RuleElevation, Cwd: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	open := func() map[string]model.Approval {
		t.Helper()
		list, err := f.Approvals(model.ApprovalPending)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]model.Approval{}
		for _, a := range list {
			out[a.ID] = a
		}
		return out
	}
	if n := len(open()); n != 4 {
		t.Fatalf("waiting before: %d", n)
	}

	// Refusals: no reason, nothing named, an agent that is not the head, Companion, a peer.
	if _, err := f.CloseOwnerCards(OwnerCaller, CloseCardsRequest{Imported: true}); KindOf(err) != KindInvalid {
		t.Fatalf("no reason: %v", err)
	}
	if _, err := f.CloseOwnerCards(OwnerCaller, CloseCardsRequest{Reason: "x"}); KindOf(err) != KindInvalid {
		t.Fatalf("nothing named: %v", err)
	}
	for _, c := range []Caller{lead, {Companion: true}, PeerCaller("box"), {}} {
		if _, err := f.CloseOwnerCards(c, CloseCardsRequest{Imported: true, Reason: "x"}); KindOf(err) != KindForbidden {
			t.Fatalf("%+v closed cards: %v", c, err)
		}
	}
	if n := len(open()); n != 4 {
		t.Fatalf("a refused close changed the list: %d", n)
	}

	// --imported: only the import's card goes; the agent's own questions and the hold stay.
	res, err := f.CloseOwnerCards(OwnerCaller, CloseCardsRequest{Imported: true, Reason: "Stale after the move"})
	if err != nil || len(res.Closed) != 1 || len(res.Skipped) != 0 {
		t.Fatalf("close imported: %+v %v", res, err)
	}
	got := res.Closed[0]
	if got.State != model.ApprovalExpired || got.Answer != "" || got.Card.CloseReason != "Stale after the move" || got.Card.Origin == nil {
		t.Fatalf("the closed card: %+v", got)
	}
	left := open()
	if len(left) != 3 || left[own1.ID].ID == "" || left[own2.ID].ID == "" || left[hold.ID].ID == "" {
		t.Fatalf("after closing the imported cards: %+v", left)
	}

	// By id, as the head: a question closes; a hold and an unknown id are skipped with a reason.
	res, err = f.CloseOwnerCards(Caller{AgentID: "head"}, CloseCardsRequest{IDs: []string{own1.ID, hold.ID, "nope", " "}, Reason: "No longer needed"})
	if err != nil || len(res.Closed) != 1 || res.Closed[0].ID != own1.ID || len(res.Skipped) != 2 || res.Skipped[hold.ID] == "" || res.Skipped["nope"] == "" {
		t.Fatalf("close by id: %+v %v", res, err)
	}
	left = open()
	if len(left) != 2 || left[hold.ID].Kind != model.ApprovalGuard || left[own2.ID].ID == "" {
		t.Fatalf("after closing by id: %+v", left)
	}
	// Closing again: nothing to close, said so.
	if res, err = f.CloseOwnerCards(OwnerCaller, CloseCardsRequest{IDs: []string{own1.ID}, Reason: "again"}); err != nil || len(res.Closed) != 0 || res.Skipped[own1.ID] == "" {
		t.Fatalf("close twice: %+v %v", res, err)
	}
	if again, _ := f.ApprovalByID(own1.ID); again.Card.CloseReason != "No longer needed" {
		t.Fatalf("a second close changed the reason: %+v", again.Card)
	}
	// Nobody was told anything.
	if mail, _ := e.st.ListMessages(store.MessageFilter{}); len(mail) != 0 {
		t.Fatalf("closing sent mail: %+v", mail)
	}
}

// The head closes other agents' questions, never its own (review, 17 Nov): those are the
// owner's to close, or the head's to withdraw as their asker.
func TestTheHeadDoesNotCloseItsOwnQuestions(t *testing.T) {
	_, f, _ := sectionFixture(t)
	head := Caller{AgentID: "head"}
	mine, err := f.AskOwner(head, AskRequest{Question: "May I restart the node?"})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := f.AskOwner(Caller{AgentID: "lead"}, AskRequest{Question: "Which colour?"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.CloseOwnerCards(head, CloseCardsRequest{IDs: []string{mine.ID, theirs.ID}, Reason: "tidying"})
	if err != nil || len(res.Closed) != 1 || res.Closed[0].ID != theirs.ID || res.Skipped[mine.ID] == "" {
		t.Fatalf("the head closes: %+v %v", res, err)
	}
	if cur, _ := f.ApprovalByID(mine.ID); cur.State != model.ApprovalPending {
		t.Fatalf("the head's own question was closed: %+v", cur)
	}
	// The owner closes it; the head may still withdraw its own.
	other, err := f.AskOwner(head, AskRequest{Question: "Another one"})
	if err != nil {
		t.Fatal(err)
	}
	if res, err = f.CloseOwnerCards(OwnerCaller, CloseCardsRequest{IDs: []string{mine.ID}, Reason: "not needed"}); err != nil || len(res.Closed) != 1 {
		t.Fatalf("the owner closes the head's question: %+v %v", res, err)
	}
	if _, err := f.WithdrawOwnCard(head, other.ID, "answered elsewhere"); err != nil {
		t.Fatalf("the head withdraws its own question: %v", err)
	}
}
