package fleet

import (
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// A project's lead can be assigned after creation, by the owner or the head and nobody else;
// the head can change nothing but the lead; only a lead or head can be assigned.
func TestAProjectsLeadCanBeAssignedAfterCreation(t *testing.T) {
	e, f, p := sectionFixture(t)
	// An imported project: no lead yet.
	imported := model.Project{ID: "imported", Slug: "imported", Name: "Imported", Status: model.ProjectActive, CreatedAt: model.Now()}
	if err := e.st.PutProject(imported); err != nil {
		t.Fatal(err)
	}
	f.projects[imported.ID] = &imported
	f.agents["w"] = &agentRec{a: model.Agent{ID: "w", Name: "worker-1", ProjectID: p.ID, Level: model.LevelWorker, Harness: model.HarnessClaude}}
	f.agents["sh"] = &agentRec{a: model.Agent{ID: "sh", Name: "shell-1", Level: model.LevelLead, Harness: model.HarnessShell}}
	lead := func(s string) ProjectPatch { return ProjectPatch{Lead: &s} }

	got, err := f.UpdateProject(OwnerCaller, "imported", lead("report-lead"))
	if err != nil || got.LeadID != "lead" {
		t.Fatalf("the owner assigns a lead: %+v %v", got, err)
	}
	if stored, err := e.st.GetProject("imported"); err != nil || stored.LeadID != "lead" {
		t.Fatalf("stored: %+v %v", stored, err)
	}
	// The head changes who leads a project and the rest of it, as the owner does (until 17 Nov it
	// only filled an empty place).
	head := Caller{AgentID: "head"}
	if got, err = f.UpdateProject(head, "imported", lead("none")); err != nil || got.LeadID != "" {
		t.Fatalf("the head removes a lead: %+v %v", got, err)
	}
	if got, err = f.UpdateProject(head, "imported", lead("report-lead")); err != nil || got.LeadID != "lead" {
		t.Fatalf("the head assigns a lead: %+v %v", got, err)
	}
	if got, err = f.UpdateProject(head, "imported", lead("head")); err != nil || got.LeadID != "head" {
		t.Fatalf("the head replaces a lead: %+v %v", got, err)
	}
	name, back := "Renamed", "Imported"
	if got, err = f.UpdateProject(head, "imported", ProjectPatch{Name: &name}); err != nil || got.Name != "Renamed" {
		t.Fatalf("the head renames a project: %+v %v", got, err)
	}
	if _, err = f.UpdateProject(head, "imported", ProjectPatch{Name: &back}); err != nil {
		t.Fatal(err)
	}
	// Nobody else changes who leads: a lead of another project, a worker, Companion, a peer machine.
	for _, c := range []Caller{{AgentID: "lead"}, {AgentID: "w"}, {Companion: true}, PeerCaller("box"), {}} {
		if _, err := f.UpdateProject(c, "imported", lead("report-lead")); KindOf(err) != KindForbidden {
			t.Fatalf("%+v assigned a lead: %v", c, err)
		}
	}
	// Only a lead or head, never a worker, a shell or an unknown name.
	for _, bad := range []string{"worker-1", "shell-1", "nobody"} {
		if _, err := f.UpdateProject(OwnerCaller, "imported", lead(bad)); err == nil {
			t.Fatalf("%s was accepted as a lead", bad)
		}
	}
	if cur := f.projects["imported"]; cur.LeadID != "head" || cur.Name != "Imported" {
		t.Fatalf("a refused change changed the project: %+v", cur)
	}
	// The owner's other changes still work, with and without a lead.
	both := lead("report-lead")
	both.Name = &name
	if got, err = f.UpdateProject(OwnerCaller, "imported", both); err != nil || got.Name != "Renamed" || got.LeadID != "lead" {
		t.Fatalf("the owner renames and assigns: %+v %v", got, err)
	}
}
