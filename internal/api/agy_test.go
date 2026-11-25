package api_test

import (
	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	tc "github.com/blackcode-ch/tmx-go/internal/client"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"net/http"
	"testing"
)

func TestAgyFullAccessRequiresOwnerGate(t *testing.T) {
	d := apitest.New(t)
	owner := tc.New(d.URL, d.OwnerToken)
	agent := tc.New(d.URL, d.AgentToken(t, "head"))
	gateOn(t, owner)
	req := map[string]any{"name": "full-agy", "harness": "agy", "agy_full_access": true}
	if status, _ := call(t, agent, "POST", "/api/agents", req, nil); status != http.StatusForbidden {
		t.Fatalf("agent opt-in: %d", status)
	}
	status, proof := call(t, owner, "POST", "/api/agents", req, nil)
	if status != http.StatusPreconditionRequired {
		t.Fatalf("owner without proof: %d", status)
	}
	if _, err := d.Fleet.Agent("full-agy"); fleet.KindOf(err) != fleet.KindNotFound {
		t.Fatal("unconfirmed launch created agent")
	}
	// With the owner's proof it starts: tmx has no command guard for agy, and that is a warning
	// in the agent's timeline now, not a refusal (agents always start).
	if status, _ = call(t, confirmed(t, owner, proof), "POST", "/api/agents", req, nil); status != 0 {
		t.Fatalf("confirmed opt-in refused: %d", status)
	}
	a, err := d.Fleet.Agent("full-agy")
	if err != nil {
		t.Fatal(err)
	}
	tl, _ := d.Fleet.Timeline(a.ID, 0, 0)
	warned := false
	for _, e := range tl {
		warned = warned || e.Meta["guard_warning"] != nil
	}
	if !warned {
		t.Fatalf("no guard warning in the timeline: %+v", tl)
	}
}
