package api_test

import (
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// The dashboard asks for the snapshot without shelved agents; everyone else gets them all.
func TestSnapshotWithoutShelvedAgents(t *testing.T) {
	d := apitest.New(t)
	owner := client{t: t, base: d.URL, token: d.OwnerToken}
	if s := owner.do("POST", "/api/projects", map[string]any{"name": "alpha"}, nil); s != 201 {
		t.Fatalf("project: %d", s)
	}
	if _, err := d.Fleet.StopAgent(fleet.OwnerCaller, "alpha-lead"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Fleet.ShelveAgent(fleet.OwnerCaller, "alpha-lead", ""); err != nil {
		t.Fatal(err)
	}
	names := func(s model.Snapshot) map[string]bool {
		out := map[string]bool{}
		for _, a := range s.Agents {
			out[a.Name] = true
		}
		return out
	}
	var all, compact model.Snapshot
	if s := owner.do("GET", "/api/snapshot", nil, &all); s != 200 || !names(all)["alpha-lead"] || all.ShelvedOmitted != nil {
		t.Fatalf("the full snapshot: %d %v %v", s, names(all), all.ShelvedOmitted)
	}
	if s := owner.do("GET", "/api/snapshot?shelved=0", nil, &compact); s != 200 || names(compact)["alpha-lead"] || !names(compact)["head"] ||
		compact.ShelvedOmitted == nil || *compact.ShelvedOmitted != 1 || len(compact.Agents) != len(all.Agents)-1 {
		t.Fatalf("the snapshot without shelved agents: %d %v %v", s, names(compact), compact.ShelvedOmitted)
	}
}
