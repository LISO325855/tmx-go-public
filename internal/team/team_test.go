package team

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// good is a complete report of a computer that passes everything a report can show.
func good(build string, at int64) Report {
	return Report{Report: 1, Contract: 1, SentAt: at,
		Node:     Node{Label: "studio-mac", OS: "darwin", Build: build, BasedOn: build, Personal: true, Update: UpdateAuto, StartedAt: at - 3600_000},
		Fleet:    Fleet{AgentsRunning: 3, WaitingForOwner: 1, AIAccounts: map[string]string{"claude": SignedIn, "codex": None}},
		Links:    []Link{},
		Phone:    Phone{State: Connected, Paired: 1, LastSeenAt: at, Notifications: true, OwnerGate: GateOnWithPIN},
		WhatsApp: WhatsApp{State: Connected, AuthorisedChats: 2, LastEventAt: at},
		Email:    Email{State: Connected, Via: ViaMCP},
		Health:   Health{DiskOK: true, MemoryOK: true}}
}

func result(cs []Check, id string) Check {
	for _, c := range cs {
		if c.ID == id {
			return c
		}
	}
	return Check{}
}

func TestChecks(t *testing.T) {
	now := time.Date(2026, 11, 17, 15, 0, 0, 0, time.UTC).UnixMilli()
	r := good("v0.1.18", now-60_000)
	in := Input{Report: r, HasReport: true, SignedBy: "peer-a", SigOK: true, Member: "peer-a", At: now - 60_000, Now: now}
	cs := Checks(in)
	if len(cs) != 10 {
		t.Fatalf("contract v1 has 10 clauses, got %d", len(cs))
	}
	for _, id := range []string{"C2", "C3", "C4", "C6"} {
		if c := result(cs, id); c.Result != Pass {
			t.Errorf("%s: %+v", id, c)
		}
	}
	for _, id := range []string{"C1", "C5", "C7", "C8", "C9", "C10"} {
		if c := result(cs, id); c.Result != NotChecked {
			t.Errorf("%s should not be checked: %+v", id, c)
		}
	}
	if p, n := Passed(cs); p != 4 || n != 4 {
		t.Fatalf("passed %d of %d", p, n)
	}

	// C2: another peer ID signed it, or the signature fails.
	x := in
	x.SignedBy = "peer-b"
	if c := result(Checks(x), "C2"); c.Result != Fail {
		t.Fatalf("C2 with another signer: %+v", c)
	}
	x = in
	x.SigOK = false
	if c2, c3 := result(Checks(x), "C2"), result(Checks(x), "C3"); c2.Result != Fail || c3.Result != Fail || !strings.Contains(c3.Detail, "signature") {
		t.Fatalf("bad signature: %+v %+v", c2, c3)
	}
	// C3: 45 minutes old, or a required field missing.
	x = in
	x.At = now - 45*60_000
	if c := result(Checks(x), "C3"); c.Result != Fail || !strings.Contains(c.Detail, "45 minutes") {
		t.Fatalf("old report: %+v", c)
	}
	x = in
	x.Report.Node.Build = ""
	x.Report.Phone.OwnerGate = "maybe"
	if c := result(Checks(x), "C3"); c.Result != Fail || !strings.Contains(c.Detail, "no build") || !strings.Contains(c.Detail, "owner_gate") {
		t.Fatalf("incomplete report: %+v", c)
	}
	// C4: one channel missing.
	x = in
	x.Report.WhatsApp.State = NotSet
	if c := result(Checks(x), "C4"); c.Result != Fail || c.Detail != "not connected: WhatsApp" {
		t.Fatalf("C4: %+v", c)
	}
	// C6: below the floor, the floor itself, a team's own build, a dev build.
	for based, want := range map[string]string{"v0.1.13": Fail, "v0.1.14": Pass, "v0.1.14-3-gabcdef1": Pass, "v0.2.0-team.4": Pass, "0.1.0-dev": Fail, "dev": Fail} {
		x = in
		x.Report.Node.BasedOn = based
		if c := result(Checks(x), "C6"); c.Result != want {
			t.Errorf("C6 based on %s: %+v", based, c)
		}
	}
	// No report: the four checks fail, the rest are not checked.
	cs = Checks(Input{Member: "peer-a", Now: now})
	if p, n := Passed(cs); p != 0 || n != 4 {
		t.Fatalf("no report: %d of %d", p, n)
	}
}

func TestDecodeAndSanitize(t *testing.T) {
	r := good("v0.1.18", 1)
	r.Links = []Link{{Member: "peer-b", State: LinkDirect}, {Member: "stranger", State: LinkRelayed}}
	r.Node.Label = "a\x00very long label " + strings.Repeat("x", 100)
	r.Hours = &Hours{Days: []string{"Mon", "tue", "funday", "mon"}, From: "09:00", To: "18:00", TZ: "Europe/Zurich"}
	b, _ := json.Marshal(r)
	got, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	got = Sanitize(got, []string{"peer-b"})
	if len(got.Links) != 1 || got.Links[0].Member != "peer-b" {
		t.Fatalf("links to non-members kept: %+v", got.Links)
	}
	if len([]rune(got.Node.Label)) != 64 || strings.ContainsRune(got.Node.Label, 0) {
		t.Fatalf("label: %q", got.Node.Label)
	}
	if strings.Join(got.Hours.Days, ",") != "mon,tue" {
		t.Fatalf("days: %v", got.Hours.Days)
	}
	if _, err := Decode([]byte(`{"report":2}`)); err == nil {
		t.Fatal("another major version accepted")
	}
	if _, err := Decode([]byte(`{"report":1,` + strings.Repeat(" ", MaxReportBytes) + `}`)); err == nil {
		t.Fatal("an oversized report accepted")
	}
	// The report is a few hundred bytes.
	if len(b) > 1024 {
		t.Fatalf("report is %d bytes", len(b))
	}
}

func TestStateKeyAndChanges(t *testing.T) {
	a := good("v0.1.18", 1000)
	b := a
	b.SentAt, b.Phone.LastSeenAt, b.Fleet.AgentsRunning = 2000, 2000, 7
	if StateKey(a) != StateKey(b) {
		t.Fatal("times and counts changed the state")
	}
	b.WhatsApp.State = Error
	b.Links = []Link{{Member: "peer-b", State: LinkRelayed}}
	if StateKey(a) == StateKey(b) {
		t.Fatal("a WhatsApp problem is no change")
	}
	cs := Changes(a, b, 2000)
	if len(cs) != 2 || cs[0].Field != "whatsapp" || cs[0].Old != Connected || cs[0].New != Error ||
		cs[1].Field != "link.peer-b" || cs[1].Old != "" || cs[1].New != LinkRelayed {
		t.Fatalf("changes: %+v", cs)
	}
}

func TestBuildPeople(t *testing.T) {
	now := time.Now()
	ms := now.UnixMilli()
	members := []Member{
		{PeerID: "p-me", Name: "Sam", Me: true, Admin: true},
		{PeerID: "p-sam-server", Name: "sam"}, // the same person's second computer
		{PeerID: "p-alex", Name: "Alex"},
		{PeerID: "p-old", Name: "Priya"},
		{PeerID: "p-quiet", Name: "Jo"},
	}
	link := func(peers ...string) []Link {
		var out []Link
		for _, p := range peers {
			out = append(out, Link{Member: p, State: LinkDirect})
		}
		return out
	}
	me := good("v0.1.18", ms)
	me.Links = link("p-sam-server", "p-alex")
	me.Links = append(me.Links, Link{Member: "p-old", State: LinkOffline}, Link{Member: "p-quiet", State: LinkOffline})
	server := good("v0.1.18", ms)
	server.Node.Label, server.Node.Personal = "build-box", false
	server.Phone.State, server.WhatsApp.State, server.Email.State = NotSet, NotSet, NotSet
	server.Links = link("p-me", "p-alex")
	alex := good("v0.1.16", ms)
	alex.Links = link("p-me", "p-sam-server")
	alex.Email.State = NotSet
	old := good("v0.1.12", ms-3*24*3600_000)
	reports := map[string]Stored{
		"p-me":         {Report: me, SignedBy: "p-me", SigOK: true, At: ms},
		"p-sam-server": {Report: server, SignedBy: "p-sam-server", SigOK: true, At: ms - 60_000},
		"p-alex":       {Report: alex, SignedBy: "p-alex", SigOK: true, At: ms - 120_000},
		"p-old":        {Report: old, SignedBy: "p-old", SigOK: true, At: ms - 3*24*3600_000},
	}
	people, sum := BuildPeople(members, reports, now)
	if len(people) != 4 || people[0].Name != "Sam" || !people[0].Me || len(people[0].Nodes) != 2 {
		t.Fatalf("people: %+v", people)
	}
	if !people[0].Passing || !people[0].Channels || !people[0].Linked {
		t.Fatalf("Sam (both computers, channels on the laptop) should pass: %+v", people[0])
	}
	byName := map[string]Person{}
	for _, p := range people {
		byName[p.Name] = p
	}
	if a := byName["Alex"]; a.Passing || a.Channels || !a.Linked || a.Nodes[0].BuildState != BuildOld {
		t.Fatalf("Alex: %+v", a)
	}
	if p := byName["Priya"]; p.Nodes[0].BuildState != BuildBelowFloor || p.Nodes[0].Seen != SeenOffline || p.Linked {
		t.Fatalf("Priya: %+v", p.Nodes[0])
	}
	if j := byName["Jo"]; j.Nodes[0].HasReport || j.Nodes[0].Seen != SeenNever || j.Passing {
		t.Fatalf("Jo: %+v", j)
	}
	if sum.People != 4 || sum.Passing != 1 || sum.Linked != 2 || sum.Channels != 2 || len(sum.Builds) != 3 || sum.Newest != "v0.1.18" {
		t.Fatalf("summary: %+v", sum)
	}
}
