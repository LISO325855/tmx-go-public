package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/peers"
)

// sessionAliases are the /api/sessions/{id}/ routes added for the relay, each with the
// /api/agents twin whose handler it shares.
var sessionAliases = []struct {
	method, suffix string
	body           any
}{
	{"GET", "screen", nil},
	{"POST", "keys", map[string]string{"keys": "enter"}},
	{"POST", "stop", nil},
	{"POST", "start", nil},
	{"POST", "restart", nil},
}

// The session aliases are the agent routes under another name: whoever may not do something to
// an agent through /api/agents may not do it through /api/sessions either, and whoever may gets
// the same answer.
func TestSessionAliasesShareTheAgentRoutesChecks(t *testing.T) {
	d := apitest.New(t)
	owner := client{t: t, base: d.URL, token: d.OwnerToken}
	workerID := mkWorker(t, d)
	worker := client{t: t, base: d.URL, token: d.AgentToken(t, workerID)}
	anon := client{t: t, base: d.URL}

	for _, a := range sessionAliases {
		alias, twin := "/api/sessions/head/"+a.suffix, "/api/agents/head/"+a.suffix
		// A worker has no say over the head: refused on both names, the same way. A refusal
		// changes nothing, so the two calls see the same agent.
		got, want := worker.do(a.method, alias, a.body, nil), worker.do(a.method, twin, a.body, nil)
		if got != want || got == http.StatusNotFound || got == http.StatusMethodNotAllowed {
			t.Fatalf("worker %s %s: %d, its /api/agents twin: %d", a.method, alias, got, want)
		}
		if a.suffix != "screen" && got != http.StatusForbidden {
			t.Fatalf("a worker may %s the head through %s: %d", a.suffix, alias, got)
		}
		if got := anon.do(a.method, alias, a.body, nil); got != http.StatusUnauthorized {
			t.Fatalf("no token, %s %s: %d", a.method, alias, got)
		}
	}
	for _, path := range []string{"/api/sessions/head/forget", "/api/sessions/" + workerID + "/forget"} {
		if got := worker.do("POST", path, nil, nil); got != http.StatusForbidden {
			t.Fatalf("a worker may forget through %s: %d", path, got)
		}
	}
	if got := anon.do("POST", "/api/sessions", map[string]string{"name": "x"}, nil); got != http.StatusUnauthorized {
		t.Fatalf("no token, launch: %d", got)
	}
	// A launch a worker may not make is refused under both names alike.
	launch := map[string]string{"name": "w" + model.NewID(), "level": "lead"}
	if got, want := worker.do("POST", "/api/sessions", launch, nil), worker.do("POST", "/api/agents", launch, nil); got != want || got < 400 {
		t.Fatalf("worker launch of a lead: %d through /api/sessions, %d through /api/agents", got, want)
	}

	// The owner, through the aliases alone: stop, start, launch, forget - each lands on the agent.
	state := func(id string) model.AgentState {
		t.Helper()
		a, err := d.Fleet.Agent(id)
		if err != nil {
			t.Fatal(err)
		}
		return a.State
	}
	if got := owner.do("POST", "/api/sessions/"+workerID+"/stop", nil, nil); got != 200 || state(workerID) != model.StateStopped {
		t.Fatalf("owner stop: %d, state %s", got, state(workerID))
	}
	if got := owner.do("POST", "/api/sessions/"+workerID+"/start", nil, nil); got != 200 || state(workerID) == model.StateStopped {
		t.Fatalf("owner start: %d, state %s", got, state(workerID))
	}
	me, err := d.Fleet.Agent(workerID)
	if err != nil {
		t.Fatal(err)
	}
	var made model.Agent
	if got := owner.do("POST", "/api/sessions", map[string]string{"name": "w" + model.NewID(), "project_id": me.ProjectID, "level": "worker"}, &made); got != http.StatusCreated || made.ID == "" {
		t.Fatalf("owner launch through /api/sessions: %d %+v", got, made)
	}
	if got := owner.do("POST", "/api/sessions/"+made.ID+"/stop", nil, nil); got != 200 {
		t.Fatalf("stop before forget: %d", got)
	}
	if got := owner.do("POST", "/api/sessions/"+made.ID+"/forget", nil, nil); got != 200 {
		t.Fatalf("owner forget: %d", got)
	}
	if _, err := d.Fleet.Agent(made.ID); err == nil {
		t.Fatal("the forgotten agent is still there")
	}
}

// Two Go machines, paired: the owner at alpha works an agent on beta through POST /api/remote -
// reads its screen, stops and starts it, launches one there - which answered 404 before beta
// served the session routes the relay admits. What stays at the machine itself stays there:
// forgetting an agent is refused over the relay, an agent at alpha cannot relay controls at
// all, and a wrong pair token opens nothing.
func TestRemoteGoToGoSessionControls(t *testing.T) {
	da, db := apitest.New(t), apitest.New(t)
	pair := peers.NewPairToken()
	// beta only has to know alpha's name and token: it is never asked to call alpha here.
	cb := peers.New(config.Config{Profile: config.ProfileProduction, Machine: "beta", Peers: []config.Peer{{Name: "alpha", URL: "https://alpha.example.ts.net"}}}, config.Paths{Home: db.Home}, nil)
	if _, err := cb.Tokens().Store("alpha", pair, "test"); err != nil {
		t.Fatal(err)
	}
	betaURL := db.WithPeers(t, cb)
	ca := peers.New(config.Config{Profile: config.ProfileProduction, Machine: "alpha", Peers: []config.Peer{{Name: "beta", URL: betaURL}}}, config.Paths{Home: da.Home}, nil)
	if _, err := ca.Tokens().Store("beta", pair, "test"); err != nil {
		t.Fatal(err)
	}
	alphaURL := da.WithPeers(t, ca)

	owner := client{t: t, base: alphaURL, token: da.OwnerToken}
	relay := func(c client, method, path string, body any, out any) int {
		t.Helper()
		req := map[string]any{"host": "beta", "method": method, "path": path}
		if body != nil {
			req["body"] = body
		}
		return c.do("POST", "/api/remote", req, out)
	}
	workerID := mkWorker(t, db) // an agent on beta
	state := func(id string) model.AgentState {
		t.Helper()
		a, err := db.Fleet.Agent(id)
		if err != nil {
			t.Fatal(err)
		}
		return a.State
	}

	// A launch first: a terminal agent, made on beta and nowhere else.
	on, err := db.Fleet.Agent(workerID)
	if err != nil {
		t.Fatal(err)
	}
	name := "w" + model.NewID()
	var made model.Agent
	if got := relay(owner, "POST", "/api/sessions", map[string]string{"name": name, "mode": "terminal"}, &made); got != 200 || made.Name != name {
		t.Fatalf("relayed launch: %d %+v", got, made)
	}
	if _, err := db.Fleet.Agent(name); err != nil {
		t.Fatalf("the agent launched from alpha is not on beta: %v", err)
	}
	if _, err := da.Fleet.Agent(name); err == nil {
		t.Fatal("the agent launched for beta was made on alpha")
	}
	if p := db.Term.Last(made.ID); p != nil {
		p.SetReplay([]byte("hello from beta"))
	}

	var screen struct {
		Text string `json:"text"`
	}
	if got := relay(owner, "GET", "/api/sessions/"+name+"/screen?history=10", nil, &screen); got != 200 || !strings.Contains(screen.Text, "hello from beta") {
		t.Fatalf("relayed screen: %d %q", got, screen.Text)
	}
	if got := relay(owner, "POST", "/api/sessions/"+name+"/keys", map[string]string{"keys": "enter"}, nil); got != 200 {
		t.Fatalf("relayed keys: %d", got)
	}
	if got := relay(owner, "POST", "/api/sessions/"+workerID+"/stop", nil, nil); got != 200 || state(workerID) != model.StateStopped {
		t.Fatalf("relayed stop: %d, state on beta %s", got, state(workerID))
	}
	if got := relay(owner, "POST", "/api/sessions/"+workerID+"/start", nil, nil); got != 200 || state(workerID) == model.StateStopped {
		t.Fatalf("relayed start: %d, state on beta %s", got, state(workerID))
	}
	if got := relay(owner, "POST", "/api/sessions/"+workerID+"/restart", nil, nil); got != 200 {
		t.Fatalf("relayed restart: %d", got)
	}

	// Forgetting an agent is the owner's at that machine: a paired machine is not that owner.
	if got := relay(owner, "POST", "/api/sessions/"+workerID+"/stop", nil, nil); got != 200 {
		t.Fatalf("relayed stop before forget: %d", got)
	}
	if got := relay(owner, "POST", "/api/sessions/"+workerID+"/forget", nil, nil); got != http.StatusForbidden {
		t.Fatalf("relayed forget: %d", got)
	}
	if _, err := db.Fleet.Agent(workerID); err != nil {
		t.Fatalf("a relayed forget removed the agent: %v", err)
	}

	// The head at alpha may relay reads, never controls or a launch on another machine.
	head := client{t: t, base: alphaURL, token: da.AgentToken(t, "head")}
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/sessions/" + workerID + "/start", nil},
		{"POST", "/api/sessions/" + workerID + "/keys", map[string]string{"keys": "enter"}},
		{"GET", "/api/sessions/" + workerID + "/screen", nil},
		{"POST", "/api/sessions", map[string]string{"name": "w" + model.NewID(), "project_id": on.ProjectID, "level": "worker"}},
	} {
		if got := relay(head, c.method, c.path, c.body, nil); got != http.StatusForbidden {
			t.Fatalf("alpha's head relays %s %s: %d", c.method, c.path, got)
		}
	}
	if state(workerID) != model.StateStopped {
		t.Fatalf("a refused relay started the agent: %s", state(workerID))
	}

	// Straight at beta with a token that is not alpha's: nothing, on any of the new routes.
	for _, a := range sessionAliases {
		if got := peerDo(t, betaURL, a.method, "/api/sessions/"+workerID+"/"+a.suffix, "alpha", peers.NewPairToken(), nil, a.body, nil); got != http.StatusUnauthorized {
			t.Fatalf("wrong pair token, %s %s: %d", a.method, a.suffix, got)
		}
	}
	if got := peerDo(t, betaURL, "POST", "/api/sessions", "alpha", peers.NewPairToken(), nil, map[string]string{"name": "x"}, nil); got != http.StatusUnauthorized {
		t.Fatalf("wrong pair token, launch: %d", got)
	}
	// And the right token still reads the roster on GET: the launch did not take that path over.
	var roster struct {
		Host string `json:"host"`
	}
	if got := peerDo(t, betaURL, "GET", "/api/sessions", "alpha", pair, nil, nil, &roster); got != 200 || roster.Host != "beta" {
		t.Fatalf("roster after the launch alias: %d %+v", got, roster)
	}
}

// POST /api/sessions/{id}/send is Python's "type this into the agent" (the Python CLI's
// `tmx send name@machine`), served by the type route - never the owner's mail route that
// happens to share its last word. A paired machine's text arrives as typed text, with the same
// refusals as /api/agents/{id}/type, and no message from the owner comes into being.
func TestSessionSendTypesAndIsNeverOwnerMail(t *testing.T) {
	s := newPeerSetup(t)
	var made model.Agent
	if code := peerDo(t, s.url, "POST", "/api/sessions", "hosta", s.pair, nil, map[string]string{"name": "typed-into", "mode": "terminal"}, &made); code != http.StatusCreated {
		t.Fatalf("launch: %d", code)
	}
	mail := func() int {
		t.Helper()
		ms, err := s.d.Fleet.Messages(fleet.OwnerCaller, made.ID, 100)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, m := range ms {
			if m.From == model.Owner {
				n++
			}
		}
		return n
	}
	before := mail()
	// Python's body, extra keys and all; on behalf of one of that machine's agents.
	body := map[string]any{"text": "hello from hosta", "submit": true, "raw": false, "mode": "text"}
	relayed := map[string]string{peers.HeaderOnBehalf: "agent:tmx-lead"}
	if code := peerDo(t, s.url, "POST", "/api/sessions/typed-into/send", "hosta", s.pair, relayed, body, nil); code != 200 {
		t.Fatalf("a paired machine's send: %d", code)
	}
	p := s.d.Term.Last(made.ID)
	if p == nil || string(p.Written()) != "hello from hosta\r" {
		t.Fatalf("typed into the terminal: %q", p.Written())
	}
	if after := mail(); after != before {
		t.Fatalf("a paired machine's send became mail from the owner: %d -> %d", before, after)
	}
	// submit false leaves it unsent, as on /api/agents/{id}/type.
	if code := peerDo(t, s.url, "POST", "/api/sessions/typed-into/send", "hosta", s.pair, nil, map[string]any{"text": "draft", "submit": false}, nil); code != 200 {
		t.Fatalf("unsubmitted send: %d", code)
	}
	if got := string(p.Written()); got != "hello from hosta\rdraft" {
		t.Fatalf("after the draft: %q", got)
	}

	// The same refusals as the type route, for the same callers.
	owner := client{t: t, base: s.url, token: s.d.OwnerToken}
	worker := client{t: t, base: s.url, token: s.d.AgentToken(t, mkWorker(t, s.d))}
	text := map[string]any{"text": "x"}
	for _, target := range []string{"head", "typed-into"} {
		alias, twin := "/api/sessions/"+target+"/send", "/api/agents/"+target+"/type"
		if got, want := worker.do("POST", alias, text, nil), worker.do("POST", twin, text, nil); got != want || got < 400 {
			t.Fatalf("worker send to %s: %d, type: %d", target, got, want)
		}
	}
	if got := (client{t: t, base: s.url}).do("POST", "/api/sessions/typed-into/send", text, nil); got != http.StatusUnauthorized {
		t.Fatalf("no token: %d", got)
	}
	if got := peerDo(t, s.url, "POST", "/api/sessions/typed-into/send", "hosta", peers.NewPairToken(), nil, text, nil); got != http.StatusUnauthorized {
		t.Fatalf("wrong pair token: %d", got)
	}
	// The owner's own mail route is untouched: still the owner's alone, still mail.
	if got := peerDo(t, s.url, "POST", "/api/agents/typed-into/send", "hosta", s.pair, nil, text, nil); got != http.StatusForbidden {
		t.Fatalf("a paired machine on the owner's mail route: %d", got)
	}
	if got := owner.do("POST", "/api/agents/typed-into/send", text, nil); got >= 300 || mail() != before+1 {
		t.Fatalf("the owner's mail route: %d, owner mail %d -> %d", got, before, mail())
	}
}
