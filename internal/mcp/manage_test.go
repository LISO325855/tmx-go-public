package mcp

import (
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
)

// The head manages the node through its tools; a lead its own project; a worker nothing
// (the owner, 17 Nov 22:18). The daemon decides; the tools only carry the request.
func TestManageTools(t *testing.T) {
	d := apitest.New(t)
	head := session(t, d, "head")
	mustOK(t, head, "project_create", map[string]any{"name": "Alpha"})
	mustOK(t, head, "project_create", map[string]any{"name": "Beta"})
	alpha, _ := d.Fleet.Project("alpha")
	beta, _ := d.Fleet.Project("beta")
	for name, project := range map[string]string{"a1": alpha.ID, "b1": beta.ID} {
		if _, err := d.Fleet.LaunchAgent(fleet.OwnerCaller, fleet.LaunchRequest{Name: name, ProjectID: project}); err != nil {
			t.Fatal(err)
		}
	}
	lead, worker := session(t, d, "alpha-lead"), session(t, d, "a1")

	// A worker: nothing.
	for tool, args := range map[string]map[string]any{
		"agent_edit":     {"name": "b1", "role": "x"},
		"agent_remove":   {"name": "b1"},
		"agent_mode":     {"name": "b1", "mode": "terminal"},
		"project_update": {"project": "beta", "name": "B"},
		"project_delete": {"project": "beta"},
		"profile_set":    {"slug": "worker", "instructions": "x"},
		"profile_reset":  {"slug": "worker"},
		"person_set":     {"key": "anna", "name": "Anna"},
		"person_remove":  {"key": "anna"},
	} {
		if out, isErr := call(t, worker, tool, args); !isErr {
			t.Errorf("a worker's %s went through: %s", tool, out)
		}
	}

	// A lead: its own project, not the other, nothing node-wide.
	if out := mustOK(t, lead, "agent_edit", map[string]any{"name": "a1", "role": "docs"}); !strings.Contains(out, "a1 updated") {
		t.Errorf("a lead's agent_edit: %s", out)
	}
	if a, _ := d.Fleet.Agent("a1"); a.Role != "docs" {
		t.Errorf("a1's role: %q", a.Role)
	}
	mustOK(t, lead, "project_update", map[string]any{"project": "alpha", "brief": "Ship the docs."})
	for tool, args := range map[string]map[string]any{
		"agent_edit":     {"name": "b1", "role": "x"},
		"agent_remove":   {"name": "b1"},
		"project_update": {"project": "beta", "name": "B"},
		"project_delete": {"project": "alpha"},
		"profile_set":    {"slug": "worker", "instructions": "x"},
		"person_set":     {"key": "anna", "name": "Anna"},
	} {
		if out, isErr := call(t, lead, tool, args); !isErr {
			t.Errorf("a lead's %s outside its project went through: %s", tool, out)
		}
	}
	if out, isErr := call(t, lead, "project_update", map[string]any{"project": "alpha", "lead": "none"}); !isErr {
		t.Errorf("a lead changed who leads its project: %s", out)
	}
	mustOK(t, lead, "agent_remove", map[string]any{"name": "a1"})
	if _, err := d.Fleet.Agent("a1"); err == nil {
		t.Error("a1 is still there")
	}

	// The head: node-wide.
	if out := mustOK(t, head, "profile_set", map[string]any{"slug": "worker", "instructions": "You are {{name}}. Report when done."}); !strings.Contains(out, "updated") {
		t.Errorf("profile_set: %s", out)
	}
	if p, _ := d.Fleet.GetProfile("worker"); p.Instructions != "You are {{name}}. Report when done." || p.Name == "" || p.Model == "" {
		t.Errorf("only the given field changes: %+v", p)
	}
	if out := mustOK(t, head, "profile_set", map[string]any{"slug": "docs", "name": "Docs", "level": "worker", "model": "claude-haiku-5-5", "instructions": "Write docs."}); !strings.Contains(out, "created") {
		t.Errorf("profile_set, new: %s", out)
	}
	mustOK(t, head, "profile_delete", map[string]any{"slug": "docs"})
	mustOK(t, head, "profile_reset", map[string]any{"slug": "worker"})
	mustOK(t, head, "person_set", map[string]any{"key": "anna", "name": "Anna", "role": "designer"})
	mustOK(t, head, "person_set", map[string]any{"key": "anna", "channels": map[string]string{"whatsapp": "41791111111@lid"}})
	target, err := d.Fleet.PersonInviteTarget("anna")
	if err != nil || target.Number != "41791111111@lid" {
		t.Fatalf("channel: %+v %v", target, err)
	}
	mustOK(t, head, "person_remove", map[string]any{"key": "anna"})
	mustOK(t, head, "agent_edit", map[string]any{"name": "b1", "role": "tests"})
	mustOK(t, head, "project_update", map[string]any{"project": "beta", "name": "Beta two"})
	mustOK(t, head, "project_delete", map[string]any{"project": "beta"})
	if _, err := d.Fleet.Project("beta"); err == nil {
		t.Error("beta is still there")
	}
	if out, isErr := call(t, head, "agent_remove", map[string]any{"name": "head"}); !isErr {
		t.Errorf("the head removed itself: %s", out)
	}
}
