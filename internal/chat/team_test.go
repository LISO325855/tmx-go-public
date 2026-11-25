package chat

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/p2p"
	"github.com/blackcode-ch/tmx-go/internal/p2p/p2ptest"
	"github.com/blackcode-ch/tmx-go/internal/team"
	"github.com/blackcode-ch/tmx-go/internal/teamstore"
)

// fakeSource is a computer's report as the API would collect it; tests change it.
type fakeSource struct {
	mu sync.Mutex
	r  team.Report
}

func newSource(label string) *fakeSource {
	return &fakeSource{r: team.Report{Report: 1, Contract: 1,
		Node:     team.Node{Label: label, OS: "darwin", Build: "v0.1.18", BasedOn: "v0.1.18", Personal: true, Update: team.UpdateAuto, StartedAt: 1},
		Fleet:    team.Fleet{AgentsRunning: 2, AIAccounts: map[string]string{"claude": team.SignedIn}},
		Phone:    team.Phone{State: team.Connected, Paired: 1, OwnerGate: team.GateOn},
		WhatsApp: team.WhatsApp{State: team.Connected, AuthorisedChats: 2},
		Email:    team.Email{State: team.Connected, Via: team.ViaMCP},
		Health:   team.Health{DiskOK: true, MemoryOK: true}}}
}

func (f *fakeSource) get() team.Report {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.r
	r.Fleet.AIAccounts = map[string]string{"claude": team.SignedIn}
	return r
}

func (f *fakeSource) set(fn func(*team.Report)) {
	f.mu.Lock()
	fn(&f.r)
	f.mu.Unlock()
}

type teamSide struct {
	*side
	src *fakeSource
}

func newTeamSide(t *testing.T, n *p2ptest.Network, name string) teamSide {
	t.Helper()
	s := newSide(t, n, name, func(o *Options) {
		o.TeamStore = teamstore.New(config.Paths{Home: t.TempDir()})
		o.TeamTick, o.TeamEvery = 20*time.Millisecond, time.Hour
	})
	src := newSource("studio-mac")
	s.svc.SetTeamSource(src.get)
	return teamSide{side: s, src: src}
}

// teamGroup: alex runs the group "Trip"; bo and cleo join by one invite.
func teamGroup(t *testing.T, n *p2ptest.Network) (alex, bo, cleo teamSide, gid string) {
	t.Helper()
	alex, bo, cleo = newTeamSide(t, n, "alex"), newTeamSide(t, n, "bo"), newTeamSide(t, n, "cleo")
	g, err := alex.svc.Create(CreateRequest{Name: "Trip"})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []teamSide{bo, cleo} {
		// One invite per member: a group invite is used once in this build.
		inv, err := alex.svc.CreateInvite(InviteRequest{Kind: p2p.InviteGroup, GroupID: g.ID})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.svc.Join(context.Background(), inv.Link); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []teamSide{alex, bo, cleo} {
		eventually(t, s.name+" sees 3 members", func() bool {
			c, err := s.svc.Chat(g.ID)
			return err == nil && c.Status == model.ChatActive && len(current(c.Members)) == 3
		})
	}
	return alex, bo, cleo, g.ID
}

func sharing(t *testing.T, s teamSide, gid string) TeamSharing {
	t.Helper()
	p, err := s.svc.Team()
	if err != nil {
		t.Fatal(err)
	}
	for _, sh := range p.Sharing {
		if sh.ChatID == gid {
			return sh
		}
	}
	return TeamSharing{}
}

func statusSent(s teamSide) []p2p.Envelope {
	var out []p2p.Envelope
	for _, x := range s.node.SentLog() {
		if x.Env.Type == TypeStatus {
			out = append(out, x.Env)
		}
	}
	return out
}

func groupView(t *testing.T, s teamSide, gid string) TeamGroupView {
	t.Helper()
	p, err := s.svc.Team()
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range p.Groups {
		if g.ChatID == gid {
			return g
		}
	}
	return TeamGroupView{}
}

func nodeRow(g TeamGroupView, peer string) (team.NodeRow, bool) {
	for _, p := range g.People {
		for _, n := range p.Nodes {
			if n.PeerID == peer {
				return n, true
			}
		}
	}
	return team.NodeRow{}, false
}

// A member's computer reports to the admin only after its owner opted in for that group, and
// only once the admin's tmx said it takes reports.
func TestTeamStatusNeedsOptIn(t *testing.T) {
	n := p2ptest.NewNetwork()
	alex, bo, cleo, gid := teamGroup(t, n)
	eventually(t, "bo offered the opt-in", func() bool {
		sh := sharing(t, bo, gid)
		return sh.Collects && sh.Share == ShareUnasked && sh.AdminName == "alex"
	})
	if g := groupView(t, bo, gid); g.ChatID != "" {
		t.Fatalf("bo sees the team before sharing: %+v", g)
	}
	time.Sleep(150 * time.Millisecond)
	if len(statusSent(bo)) != 0 || len(statusSent(cleo)) != 0 {
		t.Fatal("a status report went out before the owner opted in")
	}
	if rs, _ := alex.svc.teamSt.TeamReports(gid); len(rs) != 0 {
		t.Fatalf("the admin has reports nobody shared: %+v", rs)
	}
	if _, err := alex.svc.SetTeamShare(gid, true); err == nil {
		t.Fatal("the admin opted in to its own group")
	}

	sh, err := bo.svc.SetTeamShare(gid, true)
	if err != nil || sh.Share != ShareOn {
		t.Fatalf("opt in: %+v %v", sh, err)
	}
	eventually(t, "alex has bo's report", func() bool {
		rs, _ := alex.svc.teamSt.TeamReports(gid)
		return len(rs) == 1 && rs[0].PeerID == bo.node.ID()
	})
	p, err := alex.svc.Team()
	if err != nil || !p.Visible || p.Self == nil {
		t.Fatalf("the admin's Team page: %+v %v", p, err)
	}
	g := groupView(t, alex, gid)
	if !g.Admin || g.Scope != "all" || g.Reports != 1 || len(g.People) != 3 {
		t.Fatalf("the admin's view: %+v", g)
	}
	row, _ := nodeRow(g, bo.node.ID())
	if !row.HasReport || row.Seen != team.SeenNow || row.Passed != 4 || row.Links.Members != 2 {
		t.Fatalf("bo's row: %+v", row)
	}
	if row, _ := nodeRow(g, cleo.node.ID()); row.HasReport || row.Seen != team.SeenNever {
		t.Fatalf("cleo did not share: %+v", row)
	}
	// The member sees its own row only.
	bg := groupView(t, bo, gid)
	if bg.Scope != "own" || len(bg.People) != 1 || !bg.People[0].Me {
		t.Fatalf("bo's view: %+v", bg)
	}
	// Off: no more reports, even on a change.
	if _, err := bo.svc.SetTeamShare(gid, false); err != nil {
		t.Fatal(err)
	}
	sent := len(statusSent(bo))
	bo.src.set(func(r *team.Report) { r.WhatsApp.State = team.Error })
	bo.svc.teamKick()
	time.Sleep(150 * time.Millisecond)
	if got := len(statusSent(bo)); got != sent {
		t.Fatalf("%d reports after opting out", got-sent)
	}
	if pb, _ := bo.svc.Team(); pb.Visible {
		t.Fatal("the Team page shows for a member who shares nothing")
	}
}

// A report is accepted only signed by a current member, at the group's admin; a forged one
// (another peer's signature), a stranger's, and one at a non-admin are dropped.
func TestTeamStatusRejected(t *testing.T) {
	n := p2ptest.NewNetwork()
	alex, bo, cleo, gid := teamGroup(t, n)
	dave := newTeamSide(t, n, "dave")
	pairDM(t, alex.side, dave.side) // a contact of the admin, not in the group
	body := func(r team.Report) []byte {
		rb, _ := json.Marshal(r)
		b, _ := json.Marshal(statusBody{V: WireVersion, Report: rb})
		return b
	}
	r := bo.src.get()
	r.SentAt = model.Now()
	env := p2p.Envelope{ID: model.NewID(), Type: TypeStatus, ChatID: gid, Body: body(r)}

	forged, _ := cleo.node.Sign(env)
	forged.From = bo.node.ID() // cleo's signature, bo's name
	if err := alex.svc.onEnvelope(forged); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("forged report: %v", err)
	}
	tampered, _ := bo.node.Sign(env)
	tampered.Body = body(team.Report{Report: 1, Contract: 1, SentAt: 1})
	if err := alex.svc.onEnvelope(tampered); err == nil {
		t.Fatal("a report changed after signing was accepted")
	}
	stranger, _ := dave.node.Sign(env)
	if err := alex.svc.onEnvelope(stranger); err == nil || !strings.Contains(err.Error(), "not a member") {
		t.Fatalf("a stranger's report: %v", err)
	}
	atMember, _ := cleo.node.Sign(env)
	if err := bo.svc.onEnvelope(atMember); err == nil || !strings.Contains(err.Error(), "not the group's admin") {
		t.Fatalf("a report at a member: %v", err)
	}
	other := env
	other.Body = []byte(`{"v":2,"report":{"report":2}}`)
	newer, _ := bo.node.Sign(other)
	if err := alex.svc.onEnvelope(newer); err == nil {
		t.Fatal("report format 2 accepted")
	}
	if rs, _ := alex.svc.teamSt.TeamReports(gid); len(rs) != 0 {
		t.Fatalf("stored: %+v", rs)
	}
	// The real one, signed by bo.
	good, _ := bo.node.Sign(env)
	if err := alex.svc.onEnvelope(good); err != nil {
		t.Fatalf("bo's own report: %v", err)
	}
	// A chat.team from someone other than the admin changes nothing.
	tb, _ := json.Marshal(teamBody{V: WireVersion, Collects: true, Open: true})
	fake, _ := cleo.node.Sign(p2p.Envelope{ID: model.NewID(), Type: TypeTeam, ChatID: gid, Body: tb})
	before, _ := bo.svc.teamSt.TeamGroup(gid)
	if err := bo.svc.onEnvelope(fake); err == nil {
		t.Fatal("a member's chat.team accepted")
	}
	if after, _ := bo.svc.teamSt.TeamGroup(gid); after != before {
		t.Fatalf("settings changed: %+v → %+v", before, after)
	}
}

// What leaves the computer is the report and nothing else: no names, chat names, message
// text, file or agent names from the chats, whatever they hold.
func TestTeamStatusPrivacy(t *testing.T) {
	n := p2ptest.NewNetwork()
	alex, bo, _, gid := teamGroup(t, n)
	if _, err := alex.svc.Update(gid, ChatPatch{Name: ptr("Falcon launch room")}); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"the vault code is 4242-ZEBRA", "call me on +41 79 123 45 67 or mail bo@example.org"} {
		if _, err := bo.svc.Send(owner, gid, body); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, "bo offered the opt-in", func() bool { return sharing(t, bo, gid).Collects })
	if _, err := bo.svc.SetTeamShare(gid, true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a report went out", func() bool { return len(statusSent(bo)) > 0 })
	for _, env := range statusSent(bo) {
		raw, _ := json.Marshal(env)
		var sb statusBody
		if err := json.Unmarshal(env.Body, &sb); err != nil {
			t.Fatal(err)
		}
		if len(sb.Report) > 1024 {
			t.Fatalf("report is %d bytes", len(sb.Report))
		}
		for _, secret := range []string{"Falcon", "launch room", "Trip", "4242", "ZEBRA", "+41", "79 123", "example.org", "bo@",
			`"alex"`, `"cleo"`, `"bo"`, "helper", "head", "chatfiles"} {
			if strings.Contains(string(sb.Report), secret) || strings.Contains(string(raw), secret) {
				t.Errorf("the status report carries %q: %s", secret, sb.Report)
			}
		}
	}
}

// The admin can open the team: members who share then see everyone, checked by each
// computer's own signature; closed again, they keep nothing of the others.
func TestTeamOpenToMembers(t *testing.T) {
	n := p2ptest.NewNetwork()
	alex, bo, cleo, gid := teamGroup(t, n)
	for _, s := range []teamSide{bo, cleo} {
		eventually(t, s.name+" offered the opt-in", func() bool { return sharing(t, s, gid).Collects })
		if _, err := s.svc.SetTeamShare(gid, true); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, "alex has both reports", func() bool { rs, _ := alex.svc.teamSt.TeamReports(gid); return len(rs) == 2 })
	if _, err := bo.svc.SetTeamOpen(gid, true); err == nil {
		t.Fatal("a member opened the team")
	}
	if _, err := alex.svc.SetTeamOpen(gid, true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "bo sees cleo and alex", func() bool {
		g := groupView(t, bo, gid)
		c, ok1 := nodeRow(g, cleo.node.ID())
		a, ok2 := nodeRow(g, alex.node.ID())
		return g.Scope == "all" && ok1 && ok2 && c.HasReport && a.HasReport && c.Passed == 4
	})
	if _, err := alex.svc.SetTeamOpen(gid, false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "bo sees only itself again", func() bool {
		g := groupView(t, bo, gid)
		rs, _ := bo.svc.teamSt.TeamReports(gid)
		return g.Scope == "own" && len(g.People) == 1 && len(rs) == 0
	})
}

// A change goes out at once and the admin records it.
func TestTeamStatusChangeRecorded(t *testing.T) {
	n := p2ptest.NewNetwork()
	alex, bo, _, gid := teamGroup(t, n)
	eventually(t, "bo offered the opt-in", func() bool { return sharing(t, bo, gid).Collects })
	if _, err := bo.svc.SetTeamShare(gid, true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the first report", func() bool { rs, _ := alex.svc.teamSt.TeamReports(gid); return len(rs) == 1 })
	bo.src.set(func(r *team.Report) { r.WhatsApp.State = team.Error; r.Health.LastError = "whatsapp" })
	eventually(t, "the change recorded", func() bool {
		cs, _ := alex.svc.teamSt.TeamChanges(gid, bo.node.ID(), 0)
		return len(cs) == 1 && cs[0].Field == "whatsapp" && cs[0].Old == team.Connected && cs[0].New == team.Error
	})
	// Unchanged: nothing more until the 15 minutes (an hour here) are up.
	sent := len(statusSent(bo))
	time.Sleep(150 * time.Millisecond)
	if got := len(statusSent(bo)); got != sent {
		t.Fatalf("%d unchanged reports re-sent", got-sent)
	}
	_ = teamstore.ShareOn
}

func ptr[T any](v T) *T { return &v }
