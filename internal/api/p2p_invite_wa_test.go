package api_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/api"
	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	tc "github.com/blackcode-ch/tmx-go/internal/client"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/p2p"
	"github.com/blackcode-ch/tmx-go/internal/p2p/p2ptest"
	"github.com/blackcode-ch/tmx-go/internal/whatsapp"
)

const waInviteRoute = "/api/p2p/agent/invite-whatsapp"

// inviteWADaemon: a node, a linked fake WhatsApp with Anna (who wrote) and Bo (who wrote), and
// People: anna (number = Anna's), carl (a number nobody ever wrote from), dora (no number).
func inviteWADaemon(t *testing.T) (*apitest.Daemon, *p2ptest.Fake) {
	t.Helper()
	node := p2ptest.NewNetwork().NewNode("Alice")
	d := apitest.NewWithNode(t, node)
	d.EnableWhatsApp(t)
	d.LinkWhatsApp(t)
	waSeed(d)
	str := func(s string) *string { return &s }
	for _, p := range []fleet.PersonInput{
		{Key: "anna", Name: str("Anna Muster"), Lang: str("it"), Channels: map[string]string{"whatsapp": "+41 79 111 11 11"}},
		{Key: "carl", Name: str("Carl"), Channels: map[string]string{"whatsapp": "+41 79 333 33 33"}},
		{Key: "dora", Name: str("Dora")},
	} {
		if _, err := d.Fleet.PutPerson(fleet.OwnerCaller, p); err != nil {
			t.Fatal(err)
		}
	}
	return d, node
}

func waitSent(t *testing.T, d *apitest.Daemon, n int) []whatsapp.Outgoing {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if out := d.WaFake.SentNow(); len(out) >= n {
			return out
		}
		if time.Now().After(deadline) {
			t.Fatalf("WhatsApp did not send %d message(s)", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestInviteByWhatsApp(t *testing.T) {
	d, node := inviteWADaemon(t)
	head := tc.New(d.URL, d.AgentToken(t, "head"))
	owner := tc.New(d.URL, d.OwnerToken)

	var pending api.AgentP2PResult
	if code, _ := call(t, head, "POST", waInviteRoute, api.AgentP2PInviteWARequest{Person: "anna"}, &pending); code != 0 || pending.State != model.ApprovalPending {
		t.Fatalf("request: %d %+v", code, pending)
	}
	if pending.Preview != "Send Anna Muster a join invite by WhatsApp (single use, valid 48 h)" {
		t.Fatal(pending.Preview)
	}
	if len(node.Invites()) != 0 || len(d.WaFake.SentNow()) != 0 {
		t.Fatal("something happened before the owner's approval")
	}
	card, err := d.Fleet.ApprovalByID(pending.ApprovalID)
	if err != nil || card.Tool != fleet.ToolP2PInviteWA || card.Description != pending.Preview {
		t.Fatalf("card: %+v %v", card, err)
	}
	resume := map[string]string{"approval_id": pending.ApprovalID}
	if code, _ := call(t, head, "POST", waInviteRoute, map[string]string{"approval_id": pending.ApprovalID, "person": "carl"}, nil); code != 400 {
		t.Fatal("changed the held person", code)
	}
	if code, _ := call(t, head, "POST", "/api/approvals/"+pending.ApprovalID, map[string]string{"decision": "allow"}, nil); code != 403 {
		t.Fatal("agent self-approved", code)
	}
	approvePairing(t, owner, pending.ApprovalID, "allow")

	var raw json.RawMessage
	call(t, head, "POST", waInviteRoute, resume, &raw)
	var done api.AgentP2PResult
	if err := json.Unmarshal(raw, &done); err != nil {
		t.Fatal(err)
	}
	if done.Error != "" || done.Sent == nil || done.Invite != nil {
		t.Fatalf("result: %s", raw)
	}
	// The agent's receipt: who, until when, a short hash. Never the link, never the number.
	if strings.Contains(string(raw), "tmx://") || strings.Contains(string(raw), "4179") {
		t.Fatalf("the receipt leaks: %s", raw)
	}
	invites := node.Invites()
	if len(invites) != 1 || invites[0].MaxUses != 1 || invites[0].Kind != p2p.InviteDM {
		t.Fatalf("invite: %+v", invites)
	}
	inv := invites[0]
	ttl := time.Until(time.UnixMilli(inv.ExpiresAt))
	if ttl > api.InviteWATTL || ttl < api.InviteWATTL-time.Minute {
		t.Fatalf("expiry: %v", ttl)
	}
	if done.Sent.Person != "anna" || done.Sent.InviteID != inv.ID || done.Sent.ExpiresAt != inv.ExpiresAt ||
		done.Sent.Hash != api.InviteHash(inv.Link) || len(done.Sent.Hash) != 8 {
		t.Fatalf("receipt: %+v", done.Sent)
	}

	// Exactly one message, to Anna's verified number, written by the daemon in her language.
	sent := waitSent(t, d, 1)
	if len(sent) != 1 || sent[0].Chat != waAnna || !strings.HasSuffix(sent[0].Text, "\n\n"+inv.Link) || !strings.HasPrefix(sent[0].Text, "Ciao Anna, ") {
		t.Fatalf("sent: %+v", sent)
	}
	// A resume neither creates nor sends again.
	call(t, head, "POST", waInviteRoute, resume, nil)
	if len(node.Invites()) != 1 || len(d.WaFake.SentNow()) != 1 {
		t.Fatal("resume executed twice")
	}

	// The owner gets one line: who, until when, the short hash. No link.
	agent, _ := d.Fleet.Agent("head")
	msgs, err := d.Fleet.Messages(fleet.OwnerCaller, agent.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	lines := 0
	for _, m := range msgs {
		if m.To == model.Owner && strings.Contains(m.Body, "Join invite sent to Anna Muster by WhatsApp") {
			lines++
			if strings.Contains(m.Body, "tmx://") || !strings.Contains(m.Body, "#"+done.Sent.Hash) {
				t.Fatalf("owner line: %q", m.Body)
			}
		}
		if strings.Contains(m.Body, "tmx://join/") {
			t.Fatalf("the link reached mail: %q", m.Body)
		}
	}
	if lines != 1 {
		t.Fatalf("owner lines: %d (%+v)", lines, msgs)
	}

	// The message stays in the owner's WhatsApp history; an agent reading the chat gets it
	// without the link, the owner with it.
	var asAgent, asOwner []model.WaMessage
	hc := client{t: t, base: d.URL, token: d.AgentToken(t, "head")}
	oc := client{t: t, base: d.URL, token: d.OwnerToken}
	hc.do("GET", "/api/whatsapp/chats/"+waAnna+"/messages", nil, &asAgent)
	oc.do("GET", "/api/whatsapp/chats/"+waAnna+"/messages", nil, &asOwner)
	last := asAgent[len(asAgent)-1]
	if !strings.HasSuffix(last.Text, "tmx://join/[hidden]") || strings.Contains(last.Text, inv.Link) {
		t.Fatalf("agent reads: %q", last.Text)
	}
	if !strings.Contains(asOwner[len(asOwner)-1].Text, inv.Link) {
		t.Fatal("the owner's own view lost the link")
	}
	var chats []model.WaChat
	hc.do("GET", "/api/whatsapp/chats", nil, &chats)
	for _, c := range chats {
		if c.Last != nil && strings.Contains(c.Last.Text, inv.Link) {
			t.Fatal("the chat list shows an agent the link")
		}
	}

	// Allow once is not a standing grant.
	var again api.AgentP2PResult
	call(t, head, "POST", waInviteRoute, api.AgentP2PInviteWARequest{Person: "anna"}, &again)
	if again.State != model.ApprovalPending {
		t.Fatal("allow once became always")
	}
}

func TestInviteByWhatsAppOnlyVerifiedPeople(t *testing.T) {
	d, node := inviteWADaemon(t)
	head := tc.New(d.URL, d.AgentToken(t, "head"))
	for _, tt := range []struct {
		name string
		body any
		code int
	}{
		{"not in People", api.AgentP2PInviteWARequest{Person: "nobody"}, 404},
		{"no number in People", api.AgentP2PInviteWARequest{Person: "dora"}, 409},
		{"a number that never wrote to this account", api.AgentP2PInviteWARequest{Person: "carl"}, 409},
		{"no person", api.AgentP2PInviteWARequest{}, 400},
		{"unknown JID", api.AgentP2PInviteWARequest{Person: "999999999@s.whatsapp.net"}, 409},
		// Nothing but a People key is accepted: no number, chat, link or text of the agent's.
		{"a raw number", map[string]string{"person": "+41791111111"}, 404},
		{"an extra link", map[string]string{"person": "anna", "link": "tmx://join/AAAA"}, 400},
		{"an extra text", map[string]string{"person": "anna", "text": "hi"}, 400},
		{"an extra ttl", map[string]string{"person": "anna", "ttl": "720h"}, 400},
	} {
		if code, _ := call(t, head, "POST", waInviteRoute, tt.body, nil); code != tt.code {
			t.Errorf("%s: %d, want %d", tt.name, code, tt.code)
		}
	}
	// Bo wrote to this account but is not in People: a WhatsApp chat alone is not enough.
	if code, _ := call(t, head, "POST", waInviteRoute, api.AgentP2PInviteWARequest{Person: "bo"}, nil); code != 404 {
		t.Errorf("a chat without a People entry: %d", code)
	}
	if aps, _ := d.Fleet.Approvals(model.ApprovalPending); len(aps) != 0 {
		t.Fatalf("a refused request left a card: %+v", aps)
	}
	if len(node.Invites()) != 0 || len(d.WaFake.SentNow()) != 0 {
		t.Fatal("a refused request created or sent something")
	}
}

// The approval covers the person as they were: a number changed meanwhile runs nothing.
func TestInviteByWhatsAppTargetMustNotChange(t *testing.T) {
	d, node := inviteWADaemon(t)
	head := tc.New(d.URL, d.AgentToken(t, "head"))
	owner := tc.New(d.URL, d.OwnerToken)
	var pending api.AgentP2PResult
	call(t, head, "POST", waInviteRoute, api.AgentP2PInviteWARequest{Person: "anna"}, &pending)
	if _, err := d.Fleet.PutPerson(fleet.OwnerCaller, fleet.PersonInput{Key: "anna", Channels: map[string]string{"whatsapp": "+41 79 222 22 22"}}); err != nil {
		t.Fatal(err)
	}
	approvePairing(t, owner, pending.ApprovalID, "allow")
	var done api.AgentP2PResult
	call(t, head, "POST", waInviteRoute, map[string]string{"approval_id": pending.ApprovalID}, &done)
	if done.Sent != nil || !strings.Contains(done.Error, "changed after the approval") {
		t.Fatalf("ran for a changed number: %+v", done)
	}
	if len(node.Invites()) != 0 || len(d.WaFake.SentNow()) != 0 {
		t.Fatal("created or sent for a changed number")
	}
}

// Allow always covers this tool only, and creating invites does not cover sending them.
func TestInviteByWhatsAppGrantIsItsOwn(t *testing.T) {
	d, _ := inviteWADaemon(t)
	head := tc.New(d.URL, d.AgentToken(t, "head"))
	owner := tc.New(d.URL, d.OwnerToken)
	var out api.AgentP2PResult
	call(t, head, "POST", "/api/p2p/agent/invite", api.AgentP2PInviteRequest{Label: "First"}, &out)
	approvePairing(t, owner, out.ApprovalID, fleet.DecisionAllowAlways)
	var wa api.AgentP2PResult
	call(t, head, "POST", waInviteRoute, api.AgentP2PInviteWARequest{Person: "anna"}, &wa)
	if wa.State != model.ApprovalPending {
		t.Fatal("the invite-create grant let an invite go out by WhatsApp")
	}
	approvePairing(t, owner, wa.ApprovalID, fleet.DecisionAllowAlways)
	var next api.AgentP2PResult
	call(t, head, "POST", waInviteRoute, api.AgentP2PInviteWARequest{Person: "anna"}, &next)
	if next.State != model.ApprovalAllowed || next.Sent == nil {
		t.Fatalf("always: %+v", next)
	}
	// The standing grant widens nothing: an unverified person is still refused.
	if code, _ := call(t, head, "POST", waInviteRoute, api.AgentP2PInviteWARequest{Person: "carl"}, nil); code != 409 {
		t.Fatalf("unverified with a standing grant: %d", code)
	}
}

// Send paths accept invites and credential-shaped text.
func TestAgentSendsInviteAndCredentialText(t *testing.T) {
	d, node := inviteWADaemon(t)
	head := client{t: t, base: d.URL, token: d.AgentToken(t, "head")}
	owner := client{t: t, base: d.URL, token: d.OwnerToken}
	inv, err := node.CreateInvite(p2p.InviteOptions{Kind: p2p.InviteDM})
	if err != nil {
		t.Fatal(err)
	}
	// Even where the owner lets agents send without asking.
	owner.do("PUT", "/api/whatsapp/settings", map[string]any{"agents_send": "allowed"}, nil)
	for _, text := range []string{
		"Here is your invite: " + inv.Link,
		"here you go TMX://JOIN/abcdef",
		"token = ghp_" + "0123456789abcdefghijABCDEFGHIJ012345",
		"-----BEGIN " + "OPENSSH PRIVATE KEY-----",
	} {
		if s := head.do("POST", "/api/whatsapp/chats/"+waAnna+"/messages", map[string]string{"text": text}, nil); s != 201 {
			t.Errorf("whatsapp_send %q: %d, want 201", text, s)
		}
	}
	if aps, _ := d.Fleet.Approvals(model.ApprovalPending); len(aps) != 0 {
		t.Fatalf("a refused code became an approval card: %+v", aps)
	}
	if sent := waitSent(t, d, 4); len(sent) != 4 || sent[0].Text != "Here is your invite: "+inv.Link {
		t.Fatalf("sent: %+v", sent)
	}
	// Ordinary text still goes, and so do words that only sound like a code.
	if s := head.do("POST", "/api/whatsapp/chats/"+waAnna+"/messages", map[string]string{"text": "your code is 482 913"}, nil); s != 201 {
		t.Fatalf("words that sound like a code: %d", s)
	}
	if s := head.do("POST", "/api/whatsapp/chats/"+waAnna+"/messages", map[string]string{"text": "The meeting moved to 15:30 on 2026-11-18, room 204."}, nil); s != 201 {
		t.Fatalf("ordinary text: %d", s)
	}
	// A draft for a person is chat text in waiting.
	if s := head.do("POST", "/api/promises", map[string]string{"person_key": "anna", "title": "Invite", "text": "Send her " + inv.Link}, nil); s != 201 {
		t.Errorf("a draft with a link: %d, want 201", s)
	}
}

func TestInviteByWhatsAppExistingJIDWithoutPeople(t *testing.T) {
	d, node := inviteWADaemon(t)
	head := tc.New(d.URL, d.AgentToken(t, "head"))
	owner := tc.New(d.URL, d.OwnerToken)
	var pending api.AgentP2PResult
	if code, _ := call(t, head, "POST", waInviteRoute, api.AgentP2PInviteWARequest{Person: waBo}, &pending); code != 0 || pending.State != model.ApprovalPending {
		t.Fatalf("request: %d %+v", code, pending)
	}
	approvePairing(t, owner, pending.ApprovalID, "allow")
	var done api.AgentP2PResult
	call(t, head, "POST", waInviteRoute, api.AgentP2PInviteWARequest{ApprovalID: pending.ApprovalID}, &done)
	if done.Error != "" || done.Sent == nil {
		t.Fatalf("result: %+v", done)
	}
	sent := waitSent(t, d, 1)
	if len(node.Invites()) != 1 || sent[0].Chat != waBo || !strings.Contains(sent[0].Text, node.Invites()[0].Link) {
		t.Fatalf("sent: %+v", sent)
	}
}
