package api_test

import (
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// Over real HTTP: an agent lists and takes back its own owner card; the owner's list loses
// it; the owner cannot use the agent routes; a withdrawn card stays withdrawn.
func TestAgentListsAndWithdrawsItsOwnCardOverHTTP(t *testing.T) {
	d := apitest.New(t)
	owner := client{t: t, base: d.URL, token: d.OwnerToken}
	asker := client{t: t, base: d.URL, token: d.AgentToken(t, "head")}
	var card model.Approval
	if s := asker.do("POST", "/api/approvals", map[string]any{"question": "Which plan?", "options": []string{"A", "B"}}, &card); s != 201 {
		t.Fatalf("ask: %d", s)
	}
	var mine []model.Approval
	if s := asker.do("GET", "/api/approvals/mine", nil, &mine); s != 200 || len(mine) != 1 || mine[0].ID != card.ID {
		t.Fatalf("my cards: %d %+v", s, mine)
	}
	if s := owner.do("GET", "/api/approvals/mine", nil, nil); s != 403 {
		t.Fatalf("the owner listed agent cards: %d", s)
	}
	if s := owner.do("POST", "/api/approvals/"+card.ID+"/withdraw", map[string]any{"reason": "x"}, nil); s != 403 {
		t.Fatalf("the owner used the agent's withdraw route: %d", s)
	}
	if s := asker.do("POST", "/api/approvals/nope/withdraw", nil, nil); s != 404 {
		t.Fatalf("unknown card: %d", s)
	}
	var gone model.Approval
	if s := asker.do("POST", "/api/approvals/"+card.ID+"/withdraw", map[string]any{"reason": "Found it myself"}, &gone); s != 200 ||
		gone.State != model.ApprovalExpired || gone.Card == nil || gone.Card.CloseReason != "Found it myself" || gone.Answer != "" {
		t.Fatalf("withdraw: %d %+v", s, gone)
	}
	var open []model.Approval
	if s := owner.do("GET", "/api/approvals", nil, &open); s != 200 || len(open) != 0 {
		t.Fatalf("the owner's list after the withdrawal: %d %+v", s, open)
	}
	if s := asker.do("GET", "/api/approvals/mine", nil, &mine); s != 200 || len(mine) != 0 {
		t.Fatalf("my cards after the withdrawal: %d %+v", s, mine)
	}
	// Without a body, and repeated: still fine, the first reason stays.
	var again model.Approval
	if s := asker.do("POST", "/api/approvals/"+card.ID+"/withdraw", nil, &again); s != 200 || again.Card.CloseReason != "Found it myself" || again.Version != gone.Version {
		t.Fatalf("repeated withdraw: %d %+v", s, again)
	}
}
