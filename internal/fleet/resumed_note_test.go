package fleet

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// An agent started again on its earlier conversation is told, with the first message after
// that start, its whole instructions as they are now (who it is, its project, its brief, tmx's
// rules) and where the project's CLAUDE.md is: what is given only at launch did not reach a
// resumed Claude in two live checks (17 Nov). Once per start; a new conversation gets nothing
// extra; an agent without a CLAUDE.md gets its instructions without that line.
func TestAResumedAgentIsToldItsInstructionsAndTheProjectsClaudeMdAgain(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	project := workerProject(t, f)
	dir := filepath.Join(t.TempDir(), "rules")
	writeFile(t, filepath.Join(dir, "CLAUDE.md"), "Always answer in Swiss German.\n")

	a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: project, Name: "ruled", Cwd: dir, Brief: "Build the report", Prompt: "start"})
	if err != nil {
		t.Fatal(err)
	}
	first := e.chat.Last(a.ID)
	eventually(t, "the first prompt", func() bool { return len(first.Sent()) == 1 })
	if got := first.Sent()[0]; got != "start" {
		t.Fatalf("a new conversation got more than its prompt: %q", got)
	}
	// The conversation exists now (the agent answered, the harness confirmed it); the agent is
	// started again on it.
	first.Turn("On it.", 0, 1)
	eventually(t, "the answer in the timeline", func() bool {
		ok, _ := f.st.HasTimelineKind(a.ID, string(model.EntryAssistant))
		return ok
	})
	f.mu.Lock()
	f.agents[a.ID].sessionConfirmed = true
	f.mu.Unlock()
	first.State(model.StateIdle)
	if _, err := f.RestartAgentWith(OwnerCaller, "ruled", RestartOptions{}); err != nil {
		t.Fatal(err)
	}
	second := first
	eventually(t, "a new process", func() bool { second = e.chat.Last(a.ID); return second != first })
	if second.Spec.SessionID == "" {
		t.Fatalf("the restart did not resume the conversation: %+v", second.Spec.SessionID)
	}
	second.State(model.StateIdle)
	if _, err := f.SendOwner("ruled", "what is next?", ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the first message after the resume", func() bool { return len(second.Sent()) == 1 })
	got := second.Sent()[0]
	for _, want := range []string{"[tmx instructions: you were started again on your earlier conversation; these are your instructions as they are now]", `You are "ruled"`, "Your brief: Build the report",
		"mcp__tmxg__", filepath.Join(dir, "CLAUDE.md"), "read them before your first step", "what is next?"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the first message after a resume lacks %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "[tmx instructions: you were started again") > strings.Index(got, "what is next?") {
		t.Fatalf("the note comes after the message:\n%s", got)
	}
	// Once per start.
	second.State(model.StateIdle)
	if _, err := f.SendOwner("ruled", "and then?", ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the second message", func() bool { return len(second.Sent()) == 2 })
	if strings.Contains(second.Sent()[1], "you were started again") {
		t.Fatalf("the note was repeated:\n%s", second.Sent()[1])
	}

	// No brief, no CLAUDE.md: the instructions all the same (who it is, tmx's rules), without those lines.
	bare, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: project, Name: "bare", Cwd: filepath.Join(t.TempDir(), "bare")})
	if err != nil {
		t.Fatal(err)
	}
	b1 := e.chat.Last(bare.ID)
	b1.Turn("Ready.", 0, 1)
	eventually(t, "bare's answer in the timeline", func() bool {
		ok, _ := f.st.HasTimelineKind(bare.ID, string(model.EntryAssistant))
		return ok
	})
	f.mu.Lock()
	f.agents[bare.ID].sessionConfirmed = true
	f.mu.Unlock()
	b1.State(model.StateIdle)
	if _, err := f.RestartAgentWith(OwnerCaller, "bare", RestartOptions{}); err != nil {
		t.Fatal(err)
	}
	b2 := b1
	eventually(t, "bare restarted", func() bool { b2 = e.chat.Last(bare.ID); return b2 != b1 })
	b2.State(model.StateIdle)
	if _, err := f.SendOwner("bare", "hello", ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "bare's message", func() bool { return len(b2.Sent()) == 1 })
	if got := b2.Sent()[0]; !strings.Contains(got, "you were started again") || !strings.Contains(got, `You are "bare"`) ||
		strings.Contains(got, "CLAUDE.md") || strings.Contains(got, "Your brief:") || !strings.HasSuffix(got, "hello") {
		t.Fatalf("the note of an agent without brief and CLAUDE.md:\n%s", got)
	}
}
