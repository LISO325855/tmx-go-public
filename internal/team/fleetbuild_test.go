package team

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func ptr(n int) *int { return &n }

// baseReport is a complete, well-formed report of an older computer (none of the fleet build's
// additions).
func baseReport() Report {
	return Report{Report: ReportVersion, Contract: ContractVersion, SentAt: 1794940000000,
		Node:     Node{Label: "hostc", OS: "windows", Build: "v0.1.20", BasedOn: "v0.1.20", Update: UpdatePinned},
		Fleet:    Fleet{AgentsRunning: 3, WaitingForOwner: 1, AIAccounts: map[string]string{"claude": SignedIn}},
		Phone:    Phone{State: Connected, OwnerGate: GateOnWithPIN},
		WhatsApp: WhatsApp{State: Connected}, Email: Email{State: Connected, Via: ViaMCP},
		Health: Health{DiskOK: true, MemoryOK: true}}
}

// The fleet build's additions are optional: an older computer's report stays complete without
// them, and its JSON does not grow fields it never sent.
func TestAdditionsAreOptional(t *testing.T) {
	old := baseReport()
	if p := Problems(old); len(p) != 0 {
		t.Fatalf("an older report has problems: %v", p)
	}
	b, _ := json.Marshal(old)
	for _, key := range []string{`"agents"`, "agents_working", "companion", "activity"} {
		if strings.Contains(string(b), key) {
			t.Fatalf("an older report grew %s: %s", key, b)
		}
	}
	got := Sanitize(old, nil)
	if got.Fleet.Agents != nil || got.Fleet.AgentsWorking != nil || got.Companion != nil || got.Activity != nil {
		t.Fatalf("not reported became a value: %+v", got)
	}

	r := baseReport()
	r.Fleet.Agents, r.Fleet.AgentsWorking = ptr(12), ptr(0) // 0 working is a reading
	r.Companion = &Companion{State: Connected}
	r.Activity = &Activity{Day: "2026-11-17", ActiveMinutes: 310, AgentMinutes: 1240}
	if p := Problems(r); len(p) != 0 {
		t.Fatalf("a fleet-build report has problems: %v", p)
	}
	b, _ = json.Marshal(r)
	back, err := Decode(b)
	if err != nil || len(b) > MaxReportBytes {
		t.Fatalf("decode: %v (%d bytes)", err, len(b))
	}
	back = Sanitize(back, nil)
	if *back.Fleet.Agents != 12 || *back.Fleet.AgentsWorking != 0 || back.Companion.State != Connected || *back.Activity != *r.Activity {
		t.Fatalf("round trip: %+v", back)
	}
}

// What another computer says about itself is bounded: impossible counts and days are dropped
// (shown as not reported), a wrong Companion state is a problem like any other enumeration.
func TestAdditionsAreBounded(t *testing.T) {
	for name, c := range map[string]struct {
		edit func(*Report)
		ok   func(Report) bool
	}{
		"negative agents":      {func(r *Report) { r.Fleet.Agents = ptr(-1) }, func(r Report) bool { return r.Fleet.Agents == nil }},
		"absurd agents":        {func(r *Report) { r.Fleet.Agents = ptr(1 << 30) }, func(r Report) bool { return r.Fleet.Agents == nil }},
		"more working than is": {func(r *Report) { r.Fleet.Agents, r.Fleet.AgentsWorking = ptr(2), ptr(9) }, func(r Report) bool { return *r.Fleet.Agents == 2 && r.Fleet.AgentsWorking == nil }},
		"a 25-hour day":        {func(r *Report) { r.Activity = &Activity{Day: "2026-11-17", ActiveMinutes: 1500, AgentMinutes: 1500} }, func(r Report) bool { return r.Activity == nil }},
		"negative minutes":     {func(r *Report) { r.Activity = &Activity{Day: "2026-11-17", ActiveMinutes: -5, AgentMinutes: 10} }, func(r Report) bool { return r.Activity == nil }},
		"fewer agent minutes":  {func(r *Report) { r.Activity = &Activity{Day: "2026-11-17", ActiveMinutes: 60, AgentMinutes: 10} }, func(r Report) bool { return r.Activity == nil }},
		"a day that is text": {func(r *Report) {
			r.Activity = &Activity{Day: "project Apollo launch", ActiveMinutes: 1, AgentMinutes: 1}
		}, func(r Report) bool { return r.Activity == nil }},
		"a long companion": {func(r *Report) { r.Companion = &Companion{State: strings.Repeat("x", 500) + "\x07"} }, func(r Report) bool { return len(r.Companion.State) == 16 }},
	} {
		r := baseReport()
		c.edit(&r)
		if got := Sanitize(r, nil); !c.ok(got) {
			t.Fatalf("%s: %+v companion %+v activity %+v", name, got.Fleet, got.Companion, got.Activity)
		}
	}
	r := baseReport()
	r.Companion = &Companion{State: "reading your mail"}
	if p := Problems(r); len(p) != 1 || !strings.Contains(p[0], "companion.state") {
		t.Fatalf("a wrong companion state: %v", p)
	}
}

// The counts that move all day never count as "the state changed" (which sends a report at
// once); Companion connecting or failing does, once both reports carry it.
func TestStateKeyAndChangesWithAdditions(t *testing.T) {
	a, b := baseReport(), baseReport()
	a.Fleet.Agents, a.Fleet.AgentsWorking, a.Activity = ptr(5), ptr(1), &Activity{Day: "2026-11-17", ActiveMinutes: 1, AgentMinutes: 1}
	b.Fleet.Agents, b.Fleet.AgentsWorking, b.Activity = ptr(9), ptr(4), &Activity{Day: "2026-11-17", ActiveMinutes: 200, AgentMinutes: 900}
	if StateKey(a) != StateKey(b) {
		t.Fatal("moving counts changed the state key")
	}
	a.Companion, b.Companion = &Companion{State: Connected}, &Companion{State: Error}
	if StateKey(a) == StateKey(b) {
		t.Fatal("Companion failing did not change the state key")
	}
	ch := Changes(a, b, 7)
	if len(ch) != 1 || ch[0].Field != "companion" || ch[0].Old != Connected || ch[0].New != Error {
		t.Fatalf("changes: %+v", ch)
	}
	// An older computer upgrading (no Companion field before) is not a change of state.
	if ch := Changes(baseReport(), b, 7); len(ch) != 0 {
		t.Fatalf("an upgrade read as a change: %+v", ch)
	}
}

// The meter: one minute per tick at most, once per minute at most, zero at midnight, and a
// restart continues the same day only.
func TestMeter(t *testing.T) {
	var m Meter
	t0 := time.Date(2026, 11, 17, 9, 0, 0, 0, time.Local)
	if a := m.Activity(t0); a != (Activity{Day: "2026-11-17"}) {
		t.Fatalf("before anything: %+v", a)
	}
	m.Tick(t0, 2)
	m.Tick(t0.Add(10*time.Second), 2) // too soon: not another minute
	m.Tick(t0.Add(time.Minute), 0)    // nobody working: a minute passes, nothing counted
	m.Tick(t0.Add(2*time.Minute), 3)
	m.Tick(t0.Add(3*time.Hour), 1) // the daemon slept three hours: one minute, not 180
	if a := m.Activity(t0.Add(3 * time.Hour)); a != (Activity{Day: "2026-11-17", ActiveMinutes: 3, AgentMinutes: 6}) {
		t.Fatalf("the day so far: %+v", a)
	}
	next := time.Date(2026, 11, 18, 0, 0, 30, 0, time.Local)
	if a := m.Activity(next); a != (Activity{Day: "2026-11-18"}) {
		t.Fatalf("a new day before its first tick: %+v", a)
	}
	m.Tick(next, 1)
	if a := m.Activity(next); a != (Activity{Day: "2026-11-18", ActiveMinutes: 1, AgentMinutes: 1}) {
		t.Fatalf("a new day: %+v", a)
	}

	var r Meter
	r.Restore(next, Activity{Day: "2026-11-17", ActiveMinutes: 500, AgentMinutes: 900}) // yesterday's: ignored
	r.Restore(next, Activity{Day: "2026-11-18", ActiveMinutes: 90, AgentMinutes: 20})   // malformed: ignored
	if a := r.Activity(next); a != (Activity{Day: "2026-11-18"}) {
		t.Fatalf("restored what it should not: %+v", a)
	}
	r.Restore(next, Activity{Day: "2026-11-18", ActiveMinutes: 40, AgentMinutes: 75})
	r.Tick(next, 2)
	if a := r.Activity(next); a != (Activity{Day: "2026-11-18", ActiveMinutes: 41, AgentMinutes: 77}) {
		t.Fatalf("after a restart: %+v", a)
	}
}

// A fleet build is its own release line: the version clause passes for it instead of failing
// as "not a release", its row says so, and it does not become "the newest release" the
// upstream builds are measured against.
func TestFleetBuildAndTheVersionClause(t *testing.T) {
	fleetRep := baseReport()
	fleetRep.Node.Build, fleetRep.Node.BasedOn = "fleet-v1.4.0-12-gabc1234", FleetLine
	in := Input{Report: fleetRep, HasReport: true, SignedBy: "p-fleet", SigOK: true, Member: "p-fleet", At: 1794940000000, Now: 1794940000000}
	c6 := Checks(in)[5]
	if c6.ID != "C6" || c6.Result != Pass || !strings.Contains(c6.Title, "fleet build") {
		t.Fatalf("C6 for a fleet build: %+v", c6)
	}
	if passed, checked := Passed(Checks(in)); passed != checked {
		t.Fatalf("a complete fleet-build report passes %d of %d", passed, checked)
	}
	// Still judged: a build that is neither a release nor the fleet line fails as before.
	dev := in
	dev.Report.Node.BasedOn = "my-branch"
	if c := Checks(dev)[5]; c.Result != Fail {
		t.Fatalf("C6 for a dev build: %+v", c)
	}

	old := baseReport() // v0.1.20
	now := time.UnixMilli(1794940000000)
	g, _ := BuildPeople([]Member{{PeerID: "p-fleet", Name: "the owner"}, {PeerID: "p-old", Name: "Mustneer"}},
		map[string]Stored{"p-fleet": {Report: fleetRep, SignedBy: "p-fleet", SigOK: true, At: now.UnixMilli()},
			"p-old": {Report: old, SignedBy: "p-old", SigOK: true, At: now.UnixMilli()}}, now)
	states := map[string]string{}
	for _, p := range g {
		for _, n := range p.Nodes {
			states[n.PeerID] = n.BuildState
		}
	}
	if states["p-fleet"] != BuildFleet || states["p-old"] != BuildCurrent {
		t.Fatalf("build states: %v", states)
	}
}
