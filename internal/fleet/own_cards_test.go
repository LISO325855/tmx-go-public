package fleet

import (
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// An agent sees its own waiting owner cards and nobody else's, takes one back while it waits,
// and cannot touch another agent's card or one the owner already answered.
func TestAnAgentListsAndWithdrawsItsOwnOwnerCards(t *testing.T) {
	_, f, p := sectionFixture(t)
	lead, head := Caller{AgentID: "lead"}, Caller{AgentID: "head"}

	plain, err := f.AskOwner(lead, AskRequest{Question: "Which colour?", Options: []string{"Red", "Blue"}})
	if err != nil {
		t.Fatal(err)
	}
	later := true
	parked, err := f.AskProjectOwner(lead, p.ID, ProjectCardRequest{AskRequest: AskRequest{Question: "Project name?"}, Later: &later})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := f.AskOwner(head, AskRequest{Question: "The head's question"})
	if err != nil {
		t.Fatal(err)
	}

	mine, err := f.OwnCards(lead)
	if err != nil || len(mine) != 2 {
		t.Fatalf("the lead's cards: %+v %v", mine, err)
	}
	for _, a := range mine {
		if a.ID != plain.ID && a.ID != parked.ID {
			t.Fatalf("a card of another agent is listed: %+v", a)
		}
	}
	for _, c := range []Caller{OwnerCaller, {Companion: true}, PeerCaller("box"), {}} {
		if _, err := f.OwnCards(c); KindOf(err) != KindForbidden {
			t.Fatalf("%+v lists agent cards: %v", c, err)
		}
		if _, err := f.WithdrawOwnCard(c, plain.ID, "x"); KindOf(err) != KindForbidden {
			t.Fatalf("%+v withdraws an agent's card: %v", c, err)
		}
	}

	// Another agent's card, and a card that does not exist, look the same.
	if _, err := f.WithdrawOwnCard(lead, theirs.ID, "not mine"); KindOf(err) != KindNotFound {
		t.Fatalf("withdrew another agent's card: %v", err)
	}
	if _, err := f.WithdrawOwnCard(lead, "nope", ""); KindOf(err) != KindNotFound {
		t.Fatalf("unknown card: %v", err)
	}
	if still, _ := f.st.GetApproval(theirs.ID); still.State != model.ApprovalPending {
		t.Fatalf("the head's card changed: %+v", still)
	}

	gone, err := f.WithdrawOwnCard(lead, plain.ID, "  Found it in the brief  ")
	if err != nil || gone.State != model.ApprovalExpired || gone.Answer != "" || gone.Card.CloseReason != "Found it in the brief" || gone.Version != plain.Version+1 {
		t.Fatalf("withdraw: %+v %v", gone, err)
	}
	again, err := f.WithdrawOwnCard(lead, plain.ID, "another reason")
	if err != nil || again.Version != gone.Version || again.Card.CloseReason != "Found it in the brief" {
		t.Fatalf("repeated withdraw: %+v %v", again, err)
	}
	// A parked project card too, with the default reason.
	gone2, err := f.WithdrawOwnCard(lead, parked.ID, "")
	if err != nil || gone2.State != model.ApprovalExpired || gone2.Deferred || gone2.Card.CloseReason != "Withdrawn by report-lead" {
		t.Fatalf("withdraw a parked project card: %+v %v", gone2, err)
	}
	if mine, err = f.OwnCards(lead); err != nil || len(mine) != 0 {
		t.Fatalf("after withdrawing: %+v %v", mine, err)
	}
	if open, err := f.Approvals(model.ApprovalPending); err != nil || len(open) != 1 || open[0].ID != theirs.ID {
		t.Fatalf("the owner's list: %+v %v", open, err)
	}

	// An answered card cannot be withdrawn.
	if _, err := f.Decide(OwnerCaller, theirs.ID, DecideRequest{Decision: "answer", Version: theirs.Version, AnswerID: "a1", Answer: "Yes", FreeText: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WithdrawOwnCard(head, theirs.ID, "too late"); KindOf(err) != KindConflict {
		t.Fatalf("withdrew an answered card: %v", err)
	}
}
