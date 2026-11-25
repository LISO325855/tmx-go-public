package fleet

import (
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// The owner, 17 Nov 22:18: "The head can do everything; a lead can do them inside its own project."
// Two projects, each with its lead and a worker. The head acts on both; each lead only on its own;
// a worker on neither; a paired machine and Companion on neither.
func TestHeadManagesTheNodeALeadItsProject(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	alpha, _ := f.CreateProject(OwnerCaller, ProjectRequest{Name: "alpha"})
	beta, _ := f.CreateProject(OwnerCaller, ProjectRequest{Name: "beta"})
	head := AgentCaller(mustAgent(t, f, "head").ID)
	aLead := AgentCaller(mustAgent(t, f, "alpha-lead").ID)
	launch := func(name, project string) model.Agent {
		t.Helper()
		a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: name, ProjectID: project})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a1, b1 := launch("a1", alpha.ID), launch("b1", beta.ID)
	worker := AgentCaller(a1.ID)
	role := "docs"
	edit := AgentPatch{Role: &role}
	mode := ModeRequest{Mode: model.ModeTerminal}

	// A lead: inside its own project yes, in another project no, on the head no, on itself no.
	if _, err := f.UpdateAgent(aLead, "a1", edit); err != nil {
		t.Errorf("a lead editing its worker: %v", err)
	}
	for name, target := range map[string]string{"another project's worker": "b1", "another project's lead": "beta-lead", "the head": "head", "itself": "alpha-lead"} {
		if _, err := f.UpdateAgent(aLead, target, edit); KindOf(err) != KindForbidden {
			t.Errorf("a lead edited %s: %v", name, err)
		}
		if _, err := f.SetMode(aLead, target, mode); KindOf(err) != KindForbidden {
			t.Errorf("a lead switched the mode of %s: %v", name, err)
		}
		if err := f.RemoveAgent(aLead, target); KindOf(err) != KindForbidden {
			t.Errorf("a lead removed %s: %v", name, err)
		}
		if _, err := f.StopAgent(aLead, target); KindOf(err) != KindForbidden {
			t.Errorf("a lead stopped %s: %v", name, err)
		}
	}
	// Its own project: name, brief and status, not who leads it; not another project; no delete.
	name := "Alpha two"
	if got, err := f.UpdateProject(aLead, alpha.ID, ProjectPatch{Name: &name}); err != nil || got.Name != name {
		t.Errorf("a lead renaming its project: %+v %v", got, err)
	}
	none := "none"
	if _, err := f.UpdateProject(aLead, alpha.ID, ProjectPatch{Lead: &none}); KindOf(err) != KindForbidden {
		t.Errorf("a lead changed who leads its project: %v", err)
	}
	if _, err := f.UpdateProject(aLead, beta.ID, ProjectPatch{Name: &name}); KindOf(err) != KindForbidden {
		t.Errorf("a lead renamed another project: %v", err)
	}
	if err := f.DeleteProject(aLead, alpha.ID); KindOf(err) != KindForbidden {
		t.Errorf("a lead deleted its project: %v", err)
	}
	// Node-wide things are not a lead's.
	person := "Anna"
	if _, err := f.PutPerson(aLead, PersonInput{Key: "anna", Name: &person}); KindOf(err) != KindForbidden {
		t.Errorf("a lead edited people: %v", err)
	}
	if err := f.DeletePerson(aLead, "anna"); KindOf(err) != KindForbidden {
		t.Errorf("a lead removed a person: %v", err)
	}

	// A worker, a paired machine, Companion: none of it.
	for who, c := range map[string]Caller{"a worker": worker, "a paired machine": PeerCaller("hostb"), "Companion": CompanionCaller} {
		if _, err := f.UpdateAgent(c, "b1", edit); KindOf(err) != KindForbidden {
			t.Errorf("%s edited an agent: %v", who, err)
		}
		if _, err := f.SetMode(c, "b1", mode); KindOf(err) != KindForbidden {
			t.Errorf("%s switched a mode: %v", who, err)
		}
		if err := f.RemoveAgent(c, "b1"); KindOf(err) != KindForbidden {
			t.Errorf("%s removed an agent: %v", who, err)
		}
		if _, err := f.UpdateProject(c, beta.ID, ProjectPatch{Name: &name}); KindOf(err) != KindForbidden {
			t.Errorf("%s changed a project: %v", who, err)
		}
		if err := f.DeleteProject(c, beta.ID); KindOf(err) != KindForbidden {
			t.Errorf("%s deleted a project: %v", who, err)
		}
		if _, err := f.PutPerson(c, PersonInput{Key: "anna", Name: &person}); KindOf(err) != KindForbidden {
			t.Errorf("%s edited people: %v", who, err)
		}
	}
	if _, err := f.Agent(b1.ID); err != nil {
		t.Fatalf("a refused removal removed b1: %v", err)
	}

	// The head: node-wide, on both projects; not on itself.
	if _, err := f.UpdateAgent(head, "b1", edit); err != nil {
		t.Errorf("the head editing a worker: %v", err)
	}
	if _, err := f.UpdateAgent(head, "beta-lead", edit); err != nil {
		t.Errorf("the head editing a lead: %v", err)
	}
	if _, err := f.StopAgent(head, "b1"); err != nil {
		t.Errorf("the head stopping a worker two levels down: %v", err)
	}
	if _, err := f.PutPerson(head, PersonInput{Key: "anna", Name: &person}); err != nil {
		t.Errorf("the head adding a person: %v", err)
	}
	if err := f.DeletePerson(head, "anna"); err != nil {
		t.Errorf("the head removing a person: %v", err)
	}
	if err := f.RemoveAgent(head, "b1"); err != nil {
		t.Errorf("the head removing a worker: %v", err)
	}
	if _, err := f.UpdateAgent(head, "head", edit); KindOf(err) != KindForbidden {
		t.Errorf("the head edited itself: %v", err)
	}
	if err := f.RemoveAgent(head, "head"); KindOf(err) != KindForbidden {
		t.Errorf("the head removed itself: %v", err)
	}
	if err := f.DeleteProject(head, beta.ID); err != nil {
		t.Errorf("the head deleting a project: %v", err)
	}

	// What stays the owner's alone: deciding a card, the guard, a shell's command.
	sh, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "own-sh", Harness: model.HarnessShell, Brief: "echo up"})
	if err != nil {
		t.Fatal(err)
	}
	cmd := "echo changed"
	if _, err := f.UpdateAgent(head, sh.ID, AgentPatch{Brief: &cmd}); KindOf(err) != KindForbidden {
		t.Errorf("the head changed a shell's command: %v", err)
	}
	if _, err := f.Decide(head, "nothing", DecideRequest{Decision: "allow"}); KindOf(err) != KindForbidden {
		t.Errorf("the head decided a card: %v", err)
	}
	if _, err := f.SetRuleState(head, "disk", "allow"); KindOf(err) != KindForbidden {
		t.Errorf("the head flipped a guard rule: %v", err)
	}
}
