package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/harness"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
)

const carriedConversation = "0199aaaa-bbbb-7ccc-8ddd-eeeeffff0001"

func timelineTexts(t *testing.T, f *Fleet, agent string) string {
	t.Helper()
	entries, err := f.Timeline(agent, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e.Text + "\n")
	}
	return b.String()
}

// An agent's stored conversation that lives in the owner's own folder of that harness is
// copied into the agent's own before a start; one that is found nowhere is reported, and the
// id of the new conversation that then starts is not stored over it.
func TestAStoredConversationIsCopiedIntoTheAgentsHomeOrKept(t *testing.T) {
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	t.Setenv("CODEX_HOME", "")
	e := newUnguardedEnv(t)
	f := e.started()
	a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: workerProject(t, f), Name: "cx", Harness: model.HarnessCodex})
	if err != nil {
		t.Fatal(err)
	}
	proc := e.chat.Last(a.ID)
	f.mu.Lock()
	r := f.agents[a.ID]
	r.a.SessionID = carriedConversation
	f.mu.Unlock()

	// The owner's folder has the conversation; the agent's own home does not.
	rel := filepath.Join("sessions", "2026", "11", "17", "rollout-2026-11-17T10-00-00-"+carriedConversation+".jsonl")
	src := filepath.Join(harness.CodexHome(userHome), rel)
	writeFile(t, src, `{"type":"session_meta"}`+"\n")
	agentHome := codexAgentHome(filepath.Join(t.TempDir(), "guard"))

	f.mu.Lock()
	ok := f.carryCodexRolloutLocked(r, agentHome)
	lost := r.codexLost
	f.mu.Unlock()
	if !ok || lost {
		t.Fatalf("a conversation in the owner's folder was not carried: ok=%v lost=%v", ok, lost)
	}
	if b, err := os.ReadFile(filepath.Join(agentHome, rel)); err != nil || !strings.Contains(string(b), "session_meta") {
		t.Fatalf("the copy: %q %v", b, err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("the owner's file is gone: %v", err)
	}
	if !strings.Contains(timelineTexts(t, f, "cx"), "was copied from your own") {
		t.Fatalf("the timeline does not say it was copied:\n%s", timelineTexts(t, f, "cx"))
	}
	// Again: it is there already, nothing more is said.
	before := timelineTexts(t, f, "cx")
	f.mu.Lock()
	ok = f.carryCodexRolloutLocked(r, agentHome)
	f.mu.Unlock()
	if !ok || timelineTexts(t, f, "cx") != before {
		t.Fatal("a second start copied or reported again")
	}

	// A conversation that is nowhere: reported once, the stored id kept.
	const gone = "0199aaaa-bbbb-7ccc-8ddd-eeeeffff0002"
	f.mu.Lock()
	r.a.SessionID = gone
	ok = f.carryCodexRolloutLocked(r, agentHome)
	again := f.carryCodexRolloutLocked(r, agentHome)
	lost = r.codexLost
	f.mu.Unlock()
	if ok || again || !lost {
		t.Fatalf("a conversation that is nowhere: ok=%v again=%v lost=%v", ok, again, lost)
	}
	tl := timelineTexts(t, f, "cx")
	if strings.Count(tl, "has no conversation "+gone) != 1 || !strings.Contains(tl, gone+" stays stored") {
		t.Fatalf("the timeline about the missing conversation:\n%s", tl)
	}
	// The process then opens a new conversation: named in the timeline, never stored over it.
	proc.Emit(runner.Event{Kind: runner.EvSession, SessionID: "0199aaaa-bbbb-7ccc-8ddd-eeeeffff0009"})
	eventually(t, "the new conversation is named", func() bool {
		return strings.Contains(timelineTexts(t, f, "cx"), "runs as a new conversation 0199aaaa-bbbb-7ccc-8ddd-eeeeffff0009")
	})
	if got := mustAgent(t, f, "cx").SessionID; got != gone {
		t.Fatalf("the stored conversation was replaced: %s", got)
	}
	// An agent whose conversation is not lost takes the id its process reports, as before.
	f.mu.Lock()
	r.codexLost = false
	f.mu.Unlock()
	proc.Emit(runner.Event{Kind: runner.EvSession, SessionID: "0199aaaa-bbbb-7ccc-8ddd-eeeeffff0010"})
	eventually(t, "an ordinary new id is stored", func() bool { return mustAgent(t, f, "cx").SessionID == "0199aaaa-bbbb-7ccc-8ddd-eeeeffff0010" })
}
