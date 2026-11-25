package mcp

import (
	"regexp"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// Through the real tools: an agent asks the owner, sees its card waiting, takes it back with a
// reason, and the owner's list is empty again.
func TestMyCardsAndWithdrawCardTools(t *testing.T) {
	d := apitest.New(t)
	cs := session(t, d, "head")
	if out, isErr := call(t, cs, "my_cards", nil); isErr || !strings.Contains(out, "None of your cards") {
		t.Fatalf("no cards yet: %s", out)
	}
	out, isErr := call(t, cs, "ask_owner", map[string]any{"question": "Stripe or\nPayPal?", "options": []string{"Stripe", "PayPal"}})
	if isErr {
		t.Fatalf("ask_owner: %s", out)
	}
	id := regexp.MustCompile(`question (\S+)\)`).FindStringSubmatch(out)[1]
	out, isErr = call(t, cs, "my_cards", nil)
	if isErr || !strings.Contains(out, id) || !strings.Contains(out, "Stripe or PayPal?") || !strings.Contains(out, "[Stripe | PayPal]") {
		t.Fatalf("my_cards: %s", out)
	}
	if out, isErr := call(t, cs, "withdraw_card", map[string]any{"id": ""}); !isErr {
		t.Fatalf("withdraw without an id: %s", out)
	}
	if out, isErr := call(t, cs, "withdraw_card", map[string]any{"id": "nope"}); !isErr {
		t.Fatalf("withdraw an unknown card: %s", out)
	}
	if out, isErr := call(t, cs, "withdraw_card", map[string]any{"id": id, "reason": "The brief already says Stripe"}); isErr || !strings.Contains(out, "Withdrawn") {
		t.Fatalf("withdraw_card: %s", out)
	}
	ap, err := d.Fleet.ApprovalByID(id)
	if err != nil || ap.State != model.ApprovalExpired || ap.Card.CloseReason != "The brief already says Stripe" {
		t.Fatalf("the card after the withdrawal: %+v %v", ap, err)
	}
	if open, err := d.Fleet.Approvals(model.ApprovalPending); err != nil || len(open) != 0 {
		t.Fatalf("the owner's list: %+v %v", open, err)
	}
	if out, _ := call(t, cs, "my_cards", nil); !strings.Contains(out, "None of your cards") {
		t.Fatalf("my_cards after the withdrawal: %s", out)
	}
}

// people shows who is in People and which channels exist, never a number or an address.
func TestPeopleToolHidesAddresses(t *testing.T) {
	d := apitest.New(t)
	name, role, kind := "Sam Example", "designer", "team"
	if _, err := d.Fleet.PutPerson(fleet.OwnerCaller, fleet.PersonInput{Key: "sam", Name: &name, Role: &role, Kind: &kind,
		Channels: map[string]string{"whatsapp": "+41790000000", "email": "sam@example.test"}}); err != nil {
		t.Fatal(err)
	}
	cs := session(t, d, "head")
	if out, isErr := call(t, cs, "people", nil); isErr || !strings.Contains(out, "sam: Sam Example") || !strings.Contains(out, "designer") ||
		!strings.Contains(out, "channels: whatsapp, email") || strings.Contains(out, "4179") || strings.Contains(out, "example.test") {
		t.Fatalf("people: %s", out)
	}
}

// The head's two tools that replace a command it cannot run: closing other agents' questions
// without an answer, and giving a project without a lead one.
func TestHeadClosesCardsAndFillsAnEmptyLead(t *testing.T) {
	d := apitest.New(t)
	if _, err := d.Fleet.CreateProject(fleet.OwnerCaller, fleet.ProjectRequest{Name: "shop"}); err != nil {
		t.Fatal(err)
	}
	head, lead := session(t, d, "head"), session(t, d, "shop-lead")
	out, isErr := call(t, lead, "ask_owner", map[string]any{"question": "Which colour?"})
	if isErr {
		t.Fatalf("ask_owner: %s", out)
	}
	id := regexp.MustCompile(`question (\S+)\)`).FindStringSubmatch(out)[1]
	if out, isErr := call(t, lead, "cards_close", map[string]any{"ids": []string{id}, "reason": "x"}); !isErr {
		t.Fatalf("a lead closed cards: %s", out)
	}
	if out, isErr := call(t, head, "cards_close", map[string]any{"ids": []string{id, "nope"}, "reason": "Overtaken"}); isErr ||
		!strings.Contains(out, "Closed 1 card") || !strings.Contains(out, "Not closed nope") {
		t.Fatalf("cards_close: %v %s", isErr, out)
	}
	if ap, err := d.Fleet.ApprovalByID(id); err != nil || ap.State != model.ApprovalExpired || ap.Card.CloseReason != "Overtaken" {
		t.Fatalf("the card after closing: %+v %v", ap, err)
	}

	// The head manages the node (the owner, 17 Nov 22:18: "the head can do everything"): it
	// replaces a project's lead (until then it could only fill an empty place). Through this
	// tool it still cannot leave a project without a lead: that stays the owner's.
	if out, isErr := call(t, head, "project_lead_set", map[string]any{"project": "shop", "lead": "head"}); isErr || !strings.Contains(out, "its lead is head") {
		t.Fatalf("the head replacing a lead: %v %s", isErr, out)
	}
	if out, isErr := call(t, head, "project_lead_set", map[string]any{"project": "shop", "lead": "none"}); !isErr {
		t.Fatalf("the head removed a lead: %s", out)
	}
	none := "none"
	if _, err := d.Fleet.UpdateProject(fleet.OwnerCaller, "shop", fleet.ProjectPatch{Lead: &none}); err != nil {
		t.Fatal(err)
	}
	if out, isErr := call(t, lead, "project_lead_set", map[string]any{"project": "shop", "lead": "shop-lead"}); !isErr {
		t.Fatalf("a lead assigned itself: %s", out)
	}
	if out, isErr := call(t, head, "project_lead_set", map[string]any{"project": "shop", "lead": "shop-lead"}); isErr || !strings.Contains(out, "its lead is shop-lead") {
		t.Fatalf("project_lead_set: %v %s", isErr, out)
	}
}

// A lead shelves a worker of its own and brings it back; it cannot shelve an agent that is not
// below it.
func TestShelveAndUnshelveTools(t *testing.T) {
	d := apitest.New(t)
	if _, err := d.Fleet.CreateProject(fleet.OwnerCaller, fleet.ProjectRequest{Name: "shop"}); err != nil {
		t.Fatal(err)
	}
	lead := session(t, d, "shop-lead")
	mustOK(t, lead, "agent_launch", map[string]any{"name": "shop-api", "task": "the API"})
	if out, isErr := call(t, lead, "agent_shelve", map[string]any{"name": "shop-api", "note": "done for now"}); isErr || !strings.Contains(out, "shelved") {
		t.Fatalf("agent_shelve: %v %s", isErr, out)
	}
	a, err := d.Fleet.Agent("shop-api")
	if err != nil || a.ShelvedAt == 0 || a.Desired != "stopped" {
		t.Fatalf("after shelving: %+v %v", a, err)
	}
	if out, isErr := call(t, lead, "agent_shelve", map[string]any{"name": "head"}); !isErr {
		t.Fatalf("a lead shelved the head: %s", out)
	}
	if out, isErr := call(t, lead, "agent_unshelve", map[string]any{"name": "shop-api", "start": true}); isErr || !strings.Contains(out, "out of the Library") {
		t.Fatalf("agent_unshelve: %v %s", isErr, out)
	}
	if a, err = d.Fleet.Agent("shop-api"); err != nil || a.ShelvedAt != 0 || a.Desired != "running" {
		t.Fatalf("after unshelving with start: %+v %v", a, err)
	}
}
