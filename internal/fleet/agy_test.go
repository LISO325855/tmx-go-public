package fleet

import (
	"github.com/blackcode-ch/tmx-go/internal/model"
	"strings"
	"testing"
)

func TestAgyFirstPromptAndRestartInstructions(t *testing.T) {
	e := newUnguardedEnv(t)
	f := e.started()
	a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "gemini", Harness: "agy", Prompt: "first task"})
	if err != nil {
		t.Fatal(err)
	}
	p := e.term.Last(a.ID)
	if !strings.HasPrefix(p.Spec.InitialPrompt, "[tmx instructions]\n") || !strings.HasSuffix(p.Spec.InitialPrompt, "first task") || len(p.Sent()) != 0 {
		t.Fatal("first prompt missing or typed twice")
	}
	f.mu.Lock()
	f.agents[a.ID].sessionConfirmed = true
	f.agents[a.ID].a.SessionID = "existing"
	f.mu.Unlock()
	if _, err := f.RestartAgent(OwnerCaller, a.ID); err != nil {
		t.Fatal(err)
	}
	p = e.term.Last(a.ID)
	if !strings.HasPrefix(p.Spec.InitialPrompt, "[tmx instructions]\n") || len(p.Sent()) != 0 {
		t.Fatal("restart lost instructions")
	}
	if err := harnessProfileOK("agy", model.Profile{Slug: "readonly", ReadOnly: true}); err == nil {
		t.Fatal("read-only accepted")
	}
}

// tmx has no command guard for agy: on Windows too it starts (the owner, 17 Nov: agents always
// start), with the warning in its timeline.
func TestAgyStartsUnguardedWithWarning(t *testing.T) {
	old := launchGOOS
	launchGOOS = "windows"
	t.Cleanup(func() { launchGOOS = old })
	e := newEnv(t)
	f := e.started()
	for _, h := range []model.Harness{"agy", "antigravity"} {
		a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: string(h), Harness: h})
		if err != nil || e.term.Last(a.ID) == nil {
			t.Fatalf("%s launch refused: %v", h, err)
		}
		if n := guardWarnings(t, f, a.ID); n != 1 || !strings.Contains(lastGuardWarning(t, f, a.ID), string(h)) {
			t.Fatalf("%s: %d guard warnings: %q", h, n, lastGuardWarning(t, f, a.ID))
		}
	}
}

func TestAgyFullAccessOwnerOptIn(t *testing.T) {
	e := newUnguardedEnv(t)
	f := e.started()
	for _, full := range []bool{false, true} {
		name := "default-agy"
		if full {
			name = "opted-in-agy"
		}
		a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: name, Harness: "agy", AgyFullAccess: full})
		if err != nil {
			t.Fatal(err)
		}
		if a.AgyFullAccess != full || e.term.Last(a.ID).Spec.AgyFullAccess != full {
			t.Fatal("opt-in not visible/propagated")
		}
		stored, err := e.st.GetAgent(a.ID)
		if err != nil || stored.AgyFullAccess {
			t.Fatal("opt-in must reset on daemon restart")
		}
	}
	if _, err := f.LaunchAgent(AgentCaller(f.HeadID()), LaunchRequest{Name: "escalation", Harness: "agy", AgyFullAccess: true}); KindOf(err) != KindForbidden {
		t.Fatalf("agent opt-in: %v", err)
	}
	if _, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "readonly", Harness: "agy", Profile: "reviewer", AgyFullAccess: true}); KindOf(err) != KindInvalid {
		t.Fatalf("readonly opt-in: %v", err)
	}
}
