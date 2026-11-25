package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/blackcode-ch/tmx-go/internal/chat"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/p2p"
	"github.com/blackcode-ch/tmx-go/internal/p2p/p2ptest"
	"github.com/blackcode-ch/tmx-go/internal/team"
)

// Team status end to end: the member's tmx collects its report from the real parts (WhatsApp,
// the fleet, the phones, the gate), shares it only after the opt-in, and the report never
// carries names or content; the admin's Team page shows it. Owner only.
func TestTeamRoutes(t *testing.T) {
	net := p2ptest.NewNetwork()
	a, b := apitest.NewWithNode(t, net.NewNode("alex")), apitest.NewWithNode(t, net.NewNode("bo"))
	alex := client{t: t, base: a.URL, token: a.OwnerToken}
	bo := client{t: t, base: b.URL, token: b.OwnerToken}

	var g model.Chat
	alex.do("POST", "/api/chats", map[string]string{"name": "Falcon launch"}, &g)
	var inv p2p.Invite
	alex.do("POST", "/api/invites", map[string]string{"kind": "group", "group_id": g.ID}, &inv)
	if s := bo.do("POST", "/api/invites/join", map[string]string{"link": inv.Link}, nil); s != 200 {
		t.Fatalf("join: %d", s)
	}
	// Fixtures the report must never carry: a project, chat text, WhatsApp chats and numbers.
	if s := bo.do("POST", "/api/projects", map[string]string{"name": "Moonbeam Rewrite"}, nil); s != 201 {
		t.Fatalf("project: %d", s)
	}
	b.EnableWhatsApp(t)
	b.LinkWhatsApp(t)
	waSeed(b)
	waitFor(t, "bo in the group", func() bool {
		var c model.Chat
		bo.do("GET", "/api/chats/"+g.ID, nil, &c)
		return c.Status == model.ChatActive && len(c.Members) == 2
	})
	bo.do("POST", "/api/chats/"+g.ID+"/messages", map[string]string{"body": "the vault code is 4242-ZEBRA"}, nil)

	head := client{t: t, base: b.URL, token: b.AgentToken(t, "head")}
	if s := head.do("GET", "/api/team-status", nil, nil); s != http.StatusForbidden {
		t.Fatalf("an agent reads the team: %d", s)
	}
	var page chat.TeamPage
	waitFor(t, "bo is offered the opt-in", func() bool {
		bo.do("GET", "/api/team-status", nil, &page)
		return len(page.Sharing) == 1 && page.Sharing[0].Collects && page.Sharing[0].Share == chat.ShareUnasked
	})
	if page.Visible || page.Self == nil || page.Contract != 1 || page.Checks != 10 {
		t.Fatalf("bo's page before sharing: %+v", page)
	}

	var raw json.RawMessage
	if s := bo.do("GET", "/api/team-status/report?chat="+g.ID, nil, &raw); s != 200 {
		t.Fatalf("report: %d", s)
	}
	var rep team.Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Report != 1 || rep.Node.Build == "" || rep.Node.Label == "" || rep.WhatsApp.State != team.Connected ||
		rep.Node.BasedOn != team.FleetLine || len(rep.Links) != 1 || rep.Links[0].Member != g.Members[0].PeerID ||
		rep.Links[0].State != team.LinkDirect {
		t.Fatalf("report: %s", raw)
	}
	// The fleet build's additions: how many agents, how many working, Companion's state and
	// today's activity - numbers and states, present even when they are zero.
	if rep.Fleet.Agents == nil || *rep.Fleet.Agents < 1 || rep.Fleet.AgentsWorking == nil || *rep.Fleet.AgentsWorking > *rep.Fleet.Agents ||
		rep.Companion == nil || rep.Companion.State != team.NotSet || rep.Activity == nil || len(rep.Activity.Day) != 10 ||
		rep.Activity.AgentMinutes < rep.Activity.ActiveMinutes {
		t.Fatalf("the fleet build's part of the report: %s", raw)
	}
	for _, key := range []string{`"agents":`, `"agents_working":`, `"activity":`, `"companion":`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("the report lacks %s: %s", key, raw)
		}
	}
	if p := team.Problems(rep); len(p) > 0 {
		t.Fatalf("an incomplete report: %v", p)
	}
	// Nothing of what the agents or the people do: no group, project or agent name (the head
	// and Moonbeam's lead exist on bo's computer), no chat text, no WhatsApp chat or number.
	for _, secret := range []string{"Falcon", "Moonbeam", "moonbeam", "4242", "ZEBRA", "Anna", "Muster", "hello", "lunch", "the view",
		"41791111111", "41790000000", "Owner", "Projects", "head", "-lead", a.Home, b.Home} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("the report carries %q: %s", secret, raw)
		}
	}

	if s := bo.do("PUT", "/api/team-status/"+g.ID+"/share", map[string]bool{"share": true}, nil); s != 200 {
		t.Fatalf("share: %d", s)
	}
	if s := alex.do("PUT", "/api/team-status/"+g.ID+"/share", map[string]bool{"share": true}, nil); s != http.StatusBadRequest {
		t.Fatalf("the admin shares with itself: %d", s)
	}
	if s := bo.do("PUT", "/api/team-status/"+g.ID+"/open", map[string]bool{"open": true}, nil); s != http.StatusForbidden {
		t.Fatalf("a member opens the team: %d", s)
	}
	var ap chat.TeamPage
	waitFor(t, "alex sees bo's report", func() bool {
		alex.do("GET", "/api/team-status", nil, &ap)
		return ap.Visible && len(ap.Groups) == 1 && ap.Groups[0].Reports == 1
	})
	if s := alex.do("PUT", "/api/team-status/"+g.ID+"/open", map[string]bool{"open": true}, nil); s != 200 {
		t.Fatalf("open: %d", s)
	}
	waitFor(t, "bo sees the whole team", func() bool {
		bo.do("GET", "/api/team-status", nil, &page)
		return page.Visible && len(page.Groups) == 1 && page.Groups[0].Scope == "all" && len(page.Groups[0].People) == 2
	})
}
