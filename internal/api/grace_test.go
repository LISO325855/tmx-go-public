package api_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/api"
	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	tc "github.com/blackcode-ch/tmx-go/internal/client"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/gate"
	"github.com/blackcode-ch/tmx-go/internal/guard"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// gateEvent waits for a gate.updated event on the bus (it carries no data).
func gateEvent(t *testing.T, sub *fleet.Sub, what string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-sub.C():
			if ev.Type == gate.EvUpdated {
				if ev.Data != nil {
					t.Fatalf("%s: gate.updated carries data (agents see the bus): %v", what, ev.Data)
				}
				return
			}
		case <-deadline:
			t.Fatalf("%s: no gate.updated event", what)
		}
	}
}

func TestGraceWindowOverTheAPI(t *testing.T) {
	d := apitest.New(t) // grace off, gate off
	owner := tc.New(d.URL, d.OwnerToken)
	owner.Header = http.Header{api.UISessionHeader: {strings.Repeat("ab", 32)}}
	agent := tc.New(d.URL, d.AgentToken(t, "head"))
	sub := d.Fleet.Subscribe()
	defer d.Fleet.Unsubscribe(sub)

	var st gate.Status
	if s, _ := call(t, owner, "GET", "/api/gate", nil, &st); s != 0 || st.GraceMinutes != 0 || st.GraceUntil != 0 || len(st.GraceSteps) != len(gate.GraceSteps) {
		t.Fatalf("status: %d %+v", s, st)
	}
	if s, _ := call(t, owner, "PUT", "/api/gate/grace", map[string]int{"grace_minutes": 7}, nil); s != http.StatusBadRequest {
		t.Fatalf("7 minutes: %d", s)
	}
	if s, _ := call(t, owner, "PUT", "/api/gate/grace", map[string]any{}, nil); s != http.StatusBadRequest {
		t.Fatalf("no length: %d", s)
	}

	// Turning grace on is a longer window: a fresh proof, although the gate is off. That proof
	// opens the window.
	hour := map[string]int{"grace_minutes": 60}
	s, conf := call(t, owner, "PUT", "/api/gate/grace", hour, nil)
	if s != http.StatusPreconditionRequired || conf.Reason != "stop asking for 1 hour after each confirmation" {
		t.Fatalf("grace on without proof: %d %+v", s, conf)
	}
	proof := confirmed(t, owner, conf)
	gateEvent(t, sub, "a proof")
	if s, _ := call(t, proof, "PUT", "/api/gate/grace", hour, &st); s != 0 || st.GraceMinutes != 60 || st.GraceUntil == 0 {
		t.Fatalf("grace on with proof: %d %+v", s, st)
	}
	gateEvent(t, sub, "grace set")

	// Turning the gate on always asks, window or not.
	on := map[string]bool{"enabled": true}
	if s, conf = call(t, owner, "PUT", "/api/gate", on, nil); s != http.StatusPreconditionRequired {
		t.Fatalf("gate on inside the window: %d", s)
	}
	if s, _ := call(t, confirmed(t, owner, conf), "PUT", "/api/gate", on, &st); s != 0 || !st.Enabled {
		t.Fatalf("gate on: %d %+v", s, st)
	}
	prompts := len(d.GateAuth.Prompts())

	// Inside the window the owner's gated requests pass without a prompt.
	hold := map[string]any{"tool": "Bash", "command": "sudo true", "rule": guard.RuleElevation, "match": "sudo true"}
	var ap, out model.Approval
	if s, _ := call(t, agent, "POST", "/api/guard/holds", hold, &ap); s != 0 {
		t.Fatalf("hold: %d", s)
	}
	if s, _ := call(t, owner, "POST", "/api/approvals/"+ap.ID, map[string]string{"decision": "allow"}, &out); s != 0 || out.State != model.ApprovalAllowed {
		t.Fatalf("allow inside the window: %d %+v", s, out)
	}
	var p model.Project
	call(t, owner, "POST", "/api/projects", map[string]string{"name": "Doomed"}, &p)
	if s, _ := call(t, owner, "DELETE", "/api/projects/"+p.ID, nil, nil); s != 0 {
		t.Fatalf("delete inside the window: %d", s)
	}
	if s, _ := call(t, owner, "PUT", "/api/safety/guard", guard.Settings{AllowBrowserAutomation: true}, nil); s != 0 {
		t.Fatalf("browser automation inside the window: %d", s)
	}
	call(t, owner, "PUT", "/api/safety/guard", guard.Settings{}, nil)
	if got := len(d.GateAuth.Prompts()); got != prompts {
		t.Fatalf("the window prompted %d times", got-prompts)
	}

	// An agent's token never reaches a gated route, window or not (and Companion's link token
	// neither); the agent's hold is still pending until the owner decides.
	var ap2 model.Approval
	call(t, agent, "POST", "/api/guard/holds", hold, &ap2)
	link := linkClient(t, d)
	var pol model.Policy
	call(t, owner, "GET", "/api/policy", nil, &pol)
	call(t, owner, "POST", "/api/projects", map[string]string{"name": "Kept"}, &p)
	// head: since 17 Nov the head manages projects, the policy and profiles like the owner, with
	// no confirmation asked of him (fleet/manage.go); these four are then not the owner's alone.
	// The owner's identity stays his: deciding approvals, the guard, adoption, the gate.
	gated := []struct {
		m, p string
		body any
		head bool
	}{
		{"POST", "/api/approvals/" + ap2.ID, map[string]string{"decision": "allow"}, false},
		{"DELETE", "/api/projects/" + p.ID, nil, true},
		{"PUT", "/api/policy", pol, true},
		{"PUT", "/api/safety/guard", guard.Settings{AllowBrowserAutomation: true}, false},
		{"POST", "/api/profiles", map[string]any{"name": "Wild", "permission_mode": "bypassPermissions"}, true},
		{"PUT", "/api/profiles/worker", map[string]any{"name": "Worker", "permission_mode": "bypassPermissions"}, true},
		{"POST", "/api/adopt", map[string]any{"pid": 1, "confirm": true}, false},
		{"PUT", "/api/gate", map[string]bool{"enabled": false}, false},
		{"PUT", "/api/gate/grace", map[string]int{"grace_minutes": 1440}, false},
		{"POST", "/api/gate/lock", nil, false},
		{"GET", "/api/gate", nil, false},
	}
	for _, c := range []struct {
		name string
		cl   *tc.Client
	}{{"agent", agent}, {"Companion", link}} {
		for _, g := range gated {
			if g.head && c.name == "agent" {
				continue
			}
			if s, _ := call(t, c.cl, g.m, g.p, g.body, nil); s != http.StatusForbidden {
				t.Errorf("%s %s %s inside the window: %d, want 403", c.name, g.m, g.p, s)
			}
		}
	}
	if _, err := d.Fleet.Project(p.ID); err != nil {
		t.Fatalf("a refused delete went through: %v", err)
	}
	if got, _ := d.Fleet.ApprovalByID(ap2.ID); got.State != model.ApprovalPending {
		t.Fatalf("an agent's hold was decided without the owner: %+v", got)
	}

	// Inside the window: turning the gate off and a longer window still ask; shorter does not.
	if s, _ := call(t, owner, "PUT", "/api/gate", map[string]bool{"enabled": false}, nil); s != http.StatusPreconditionRequired {
		t.Fatalf("gate off inside the window: %d", s)
	}
	waitIdle(t, d.Gate, owner)
	if s, _ := call(t, owner, "PUT", "/api/gate/grace", map[string]int{"grace_minutes": 120}, nil); s != http.StatusPreconditionRequired {
		t.Fatalf("longer inside the window: %d", s)
	}
	waitIdle(t, d.Gate, owner)
	prompts = len(d.GateAuth.Prompts())
	if s, _ := call(t, owner, "PUT", "/api/gate/grace", map[string]int{"grace_minutes": 30}, &st); s != 0 || st.GraceMinutes != 30 || st.GraceUntil == 0 {
		t.Fatalf("shorter: %d %+v", s, st)
	}
	gateEvent(t, sub, "shorter")
	if len(d.GateAuth.Prompts()) != prompts {
		t.Fatal("shortening prompted")
	}

	// Lock now: no proof, the window ends, the next gated request asks again.
	var locked gate.Status // grace_until is left out when locked: decode into a new value
	if s, _ := call(t, owner, "POST", "/api/gate/lock", nil, &locked); s != 0 || locked.GraceUntil != 0 || locked.GraceMinutes != 30 {
		t.Fatalf("lock: %d %+v", s, locked)
	}
	gateEvent(t, sub, "lock")
	call(t, owner, "POST", "/api/projects", map[string]string{"name": "Asks"}, &p)
	if s, _ := call(t, owner, "DELETE", "/api/projects/"+p.ID, nil, nil); s != http.StatusPreconditionRequired {
		t.Fatalf("delete after Lock now: %d", s)
	}
	waitIdle(t, d.Gate, owner)

	// Off needs no proof either; GET /api/safety shows the same.
	if s, _ := call(t, owner, "PUT", "/api/gate/grace", map[string]int{"grace_minutes": 0}, &st); s != 0 || st.GraceMinutes != 0 {
		t.Fatalf("off: %d %+v", s, st)
	}
	var info api.SafetyInfo
	if s, _ := call(t, owner, "GET", "/api/safety", nil, &info); s != 0 || info.Gate.GraceMinutes != 0 || info.Gate.GraceSteps == nil {
		t.Fatalf("safety: %d %+v", s, info.Gate)
	}
}
