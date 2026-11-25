package processes

import (
	"os"
	"testing"
)

func TestRestartKeepsCommandOwnerAndProject(t *testing.T) {
	r := New()
	t.Cleanup(r.Close)
	exe, _ := os.Executable()
	p, err := r.Start(Spec{OwnerID: "a", ProjectID: "p1", Purpose: "preview", Command: exe, Args: []string{"-test.run=^TestProcessHelper$", "--", "process-helper"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Promote(p.ID, "owner", "p2"); err != nil {
		t.Fatal(err)
	}
	n, err := r.Restart(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n.ID == p.ID || n.PID == 0 || n.PID == p.PID || n.State != "running" || n.Purpose != "preview" || n.OwnerID != "owner" ||
		n.ProjectID != "p2" || !n.Promoted || n.CreatorID != "a" || n.Restarts != 1 {
		t.Fatalf("restarted: %+v (was %+v)", n, p)
	}
	list := r.List()
	if len(list) != 1 || list[0].ID != n.ID {
		t.Fatalf("the old entry stayed: %+v", list)
	}
	// A system tool restarts itself; an unknown ID is nothing to restart.
	s, err := r.Start(Spec{OwnerID: "system", Purpose: "sys", Command: exe, Args: []string{"-test.run=^TestProcessHelper$", "--", "process-helper"}, System: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Restart(s.ID); err == nil {
		t.Fatal("a system tool was restarted by hand")
	}
	if _, err := r.Restart("nope"); err == nil {
		t.Fatal("an unknown ID was restarted")
	}
}
