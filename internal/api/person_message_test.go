package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/api"
	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	tc "github.com/blackcode-ch/tmx-go/internal/client"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/people"
)

func personMessageRoute(key string) string { return "/api/team/" + key + "/message" }

// A message to a person with a verified WhatsApp chat: an owner card with the exact text,
// nothing sent before Allow, exactly one message to that person's chat after it, and the
// number never shown to the agent.
func TestPersonMessageGoesOutByWhatsAppAfterTheOwnersAllow(t *testing.T) {
	d, _ := inviteWADaemon(t)
	head := tc.New(d.URL, d.AgentToken(t, "head"))
	owner := tc.New(d.URL, d.OwnerToken)

	var raw json.RawMessage
	if code, _ := call(t, head, "POST", personMessageRoute("anna"), api.PersonMessageRequest{Text: "  Ciao Anna, the draft is ready.  "}, &raw); code != 0 {
		t.Fatalf("request: %d %s", code, raw)
	}
	var res api.PersonMessageResult
	if err := json.Unmarshal(raw, &res); err != nil || res.Via != "whatsapp" || res.Approval == nil || res.Promise != nil || res.Person != "anna" {
		t.Fatalf("result: %s %v", raw, err)
	}
	ap := *res.Approval
	if ap.Kind != model.ApprovalWhatsApp || ap.State != model.ApprovalPending || ap.Question != "Ciao Anna, the draft is ready." ||
		ap.Description != "Send this WhatsApp message to Anna Muster" {
		t.Fatalf("card: %+v", ap)
	}
	if len(d.WaFake.SentNow()) != 0 {
		t.Fatal("something was sent before the owner's Allow")
	}
	// The agent names a person and never learns the number: what it gets back, and what it
	// lists as its own cards, carries no Input (the chat's JID is the number).
	if len(ap.Input) != 0 || strings.Contains(string(raw), "4179") || strings.Contains(string(raw), "chat_jid") {
		t.Fatalf("the agent's result names the chat: %s", raw)
	}
	var listed json.RawMessage
	if code, _ := call(t, head, "GET", "/api/approvals/mine", nil, &listed); code != 0 || strings.Contains(string(listed), "4179") || strings.Contains(string(listed), "chat_jid") {
		t.Fatalf("the agent's own cards name the chat: %d %s", code, listed)
	}
	// The owner's card still has it: Allow needs the chat.
	if full, err := d.Fleet.ApprovalByID(ap.ID); err != nil || !strings.Contains(string(full.Input), "chat_jid") {
		t.Fatalf("the stored card lost its chat: %+v %v", full, err)
	}
	// The agent sees its card waiting and cannot allow it itself.
	var mine []model.Approval
	if code, _ := call(t, head, "GET", "/api/approvals/mine", nil, &mine); code != 0 || len(mine) != 1 || mine[0].ID != ap.ID {
		t.Fatalf("my cards: %d %+v", code, mine)
	}
	if code, _ := call(t, head, "POST", "/api/approvals/"+ap.ID, map[string]string{"decision": "allow"}, nil); code != 403 {
		t.Fatal("the agent allowed its own message", code)
	}
	if code, _ := call(t, owner, "POST", "/api/approvals/"+ap.ID, map[string]string{"decision": "allow"}, nil); code != 0 {
		t.Fatalf("the owner's Allow: %d", code)
	}
	sent := waitSent(t, d, 1)
	if len(sent) != 1 || sent[0].Chat != waAnna || sent[0].Text != "Ciao Anna, the draft is ready." {
		t.Fatalf("sent: %+v", sent)
	}
	if after, err := d.Fleet.ApprovalByID(ap.ID); err != nil || after.State != model.ApprovalAllowed {
		t.Fatalf("the card after Allow: %+v %v", after, err)
	}
}

// Where the owner lets agents send without asking, an agent whose profile may use WhatsApp has
// its message to a person sent at once, with no card (it always waited until 17 Nov). Where the
// owner is asked, it waits; and a withdrawn one is never sent.
func TestPersonMessageGoesAtOnceWhereAllowedAndCanBeWithdrawnWhereNot(t *testing.T) {
	d, _ := inviteWADaemon(t)
	head := tc.New(d.URL, d.AgentToken(t, "head"))
	owner := tc.New(d.URL, d.OwnerToken)
	if code, _ := call(t, owner, "PUT", "/api/whatsapp/settings", map[string]any{"agents_send": "allowed"}, nil); code != 0 {
		t.Fatalf("settings: %d", code)
	}
	var res api.PersonMessageResult
	if code, _ := call(t, head, "POST", personMessageRoute("anna"), api.PersonMessageRequest{Text: "Hello Anna"}, &res); code != 0 || !res.Sent || res.Approval != nil {
		t.Fatalf("allowed: %d %+v", code, res)
	}
	if aps, _ := d.Fleet.Approvals(model.ApprovalPending); len(aps) != 0 || len(d.WaFake.SentNow()) != 1 {
		t.Fatalf("allowed: %d cards, %d sent", len(aps), len(d.WaFake.SentNow()))
	}
	// Join invites are ordinary sendable text.
	if code, _ := call(t, head, "POST", personMessageRoute("anna"), api.PersonMessageRequest{Text: "Join here: tmx://join/AQASIAJ7abc"}, nil); code != 0 {
		t.Fatalf("a join link where sending is allowed: %d", code)
	}
	waitSent(t, d, 2)
	// The owner is asked again: it waits.
	if code, _ := call(t, owner, "PUT", "/api/whatsapp/settings", map[string]any{"agents_send": "ask"}, nil); code != 0 {
		t.Fatalf("settings: %d", code)
	}
	res = api.PersonMessageResult{}
	if code, _ := call(t, head, "POST", personMessageRoute("anna"), api.PersonMessageRequest{Text: "Hello again"}, &res); code != 0 || res.Approval == nil || res.Sent {
		t.Fatalf("request: %d %+v", code, res)
	}
	if len(d.WaFake.SentNow()) != 2 {
		t.Fatal("sent without the owner's Allow")
	}
	var gone model.Approval
	if code, _ := call(t, head, "POST", "/api/approvals/"+res.Approval.ID+"/withdraw", map[string]string{"reason": "Anna already knows"}, &gone); code != 0 ||
		gone.State != model.ApprovalExpired || !strings.Contains(gone.Answer, "Anna already knows") {
		t.Fatalf("withdraw: %d %+v", code, gone)
	}
	if code, _ := call(t, owner, "POST", "/api/approvals/"+res.Approval.ID, map[string]string{"decision": "allow"}, nil); code == 0 {
		t.Fatal("a withdrawn message could still be allowed")
	}
	if len(d.WaFake.SentNow()) != 2 {
		t.Fatal("a withdrawn message was sent")
	}
}

// No usable WhatsApp chat: nothing is asked of the owner and nothing sent; the draft is kept
// as an open promise to carry by hand. Refusals: unknown person, empty text, a code, the owner.
func TestPersonMessageIsKeptToCarryByHand(t *testing.T) {
	d, _ := inviteWADaemon(t)
	head := tc.New(d.URL, d.AgentToken(t, "head"))
	owner := tc.New(d.URL, d.OwnerToken)

	for key, why := range map[string]string{"carl": "not verified", "dora": "no WhatsApp number"} {
		var res api.PersonMessageResult
		if code, _ := call(t, head, "POST", personMessageRoute(key), api.PersonMessageRequest{Text: "Please call back", Title: "Call back"}, &res); code != 0 {
			t.Fatalf("%s: %d", key, code)
		}
		if res.Via != "by_hand" || res.Approval != nil || res.Promise == nil || !strings.Contains(res.Reason, why) || strings.Contains(res.Reason, "4179") {
			t.Fatalf("%s: %+v", key, res)
		}
		p := *res.Promise
		if p.PersonKey != key || p.Status != people.Open || p.Title != "Call back" || !strings.Contains(p.Text, "Please call back") || !strings.Contains(p.Text, "carry by hand") {
			t.Fatalf("%s promise: %+v", key, p)
		}
	}
	if open, err := d.Fleet.Approvals(model.ApprovalPending); err != nil || len(open) != 0 {
		t.Fatalf("an owner card for a message that cannot be sent: %+v %v", open, err)
	}
	if len(d.WaFake.SentNow()) != 0 {
		t.Fatal("something was sent")
	}

	if code, _ := call(t, head, "POST", personMessageRoute("nobody"), api.PersonMessageRequest{Text: "Hi"}, nil); code != 404 {
		t.Fatal("an unknown person", code)
	}
	if code, _ := call(t, head, "POST", personMessageRoute("anna"), api.PersonMessageRequest{Text: "   "}, nil); code != 400 {
		t.Fatal("an empty message", code)
	}
	if code, _ := call(t, head, "POST", personMessageRoute("anna"), api.PersonMessageRequest{Text: "Join here: tmx://join/abcdefghijklmnopqrstuvwxyz0123456789"}, nil); code != 0 {
		t.Fatal("a join link in a message to a person", code)
	}
	if code, _ := call(t, owner, "POST", personMessageRoute("anna"), api.PersonMessageRequest{Text: "Hi"}, nil); code != 403 {
		t.Fatal("the owner used the agent route", code)
	}
	_ = fleet.OwnerCaller
	_ = apitest.New
}
