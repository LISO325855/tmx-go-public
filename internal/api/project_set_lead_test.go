package api_test

import (
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// Over real HTTP: the owner and the head assign a project's lead; a lead cannot; the head
// cannot rename a project through the same route.
func TestProjectLeadIsAssignedOverHTTP(t *testing.T) {
	d := apitest.New(t)
	owner := client{t: t, base: d.URL, token: d.OwnerToken}
	head := client{t: t, base: d.URL, token: d.AgentToken(t, "head")}
	var p model.Project
	if s := owner.do("POST", "/api/projects", map[string]any{"name": "alpha"}, &p); s != 201 || p.LeadID == "" {
		t.Fatalf("create: %d %+v", s, p)
	}
	firstLead := p.LeadID
	lead := client{t: t, base: d.URL, token: d.AgentToken(t, "alpha-lead")}

	// Who leads a project: the owner and the head (17 Nov), never the lead itself.
	if s := lead.do("PATCH", "/api/projects/alpha", map[string]any{"lead": "none"}, nil); s != 403 {
		t.Fatalf("a lead removed itself as lead: %d", s)
	}
	var cleared model.Project
	if s := owner.do("PATCH", "/api/projects/alpha", map[string]any{"lead": "none"}, &cleared); s != 200 || cleared.LeadID != "" {
		t.Fatalf("the owner removes the lead: %d %+v", s, cleared)
	}
	var filled model.Project
	if s := head.do("PATCH", "/api/projects/alpha", map[string]any{"lead": "alpha-lead"}, &filled); s != 200 || filled.LeadID != firstLead {
		t.Fatalf("the head fills the empty place: %d %+v", s, filled)
	}
	if s := owner.do("PATCH", "/api/projects/alpha", map[string]any{"lead": "none"}, &cleared); s != 200 {
		t.Fatalf("the owner removes the lead again: %d", s)
	}
	if s := lead.do("PATCH", "/api/projects/alpha", map[string]any{"lead": "alpha-lead"}, nil); s != 403 {
		t.Fatalf("a lead assigned itself: %d", s)
	}
	if s := owner.do("PATCH", "/api/projects/alpha", map[string]any{"lead": "alpha-lead"}, &p); s != 200 || p.LeadID != firstLead || p.Name != "alpha" {
		t.Fatalf("the owner assigns the lead: %d %+v", s, p)
	}
	if s := owner.do("PATCH", "/api/projects/alpha", map[string]any{"lead": "nobody"}, nil); s == 200 {
		t.Fatalf("an unknown lead was accepted")
	}
}
