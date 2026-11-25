package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/harness"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

const (
	convClaude = "11111111-2222-4333-8444-555555555555"
	convCodex  = "66666666-7777-4888-8999-aaaaaaaaaaaa"
)

// convEnv is a started fleet with one project and the owner's home in a temp folder.
func convEnv(t *testing.T) (e *env, f *Fleet, project, folder, home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	e = newUnguardedEnv(t)
	f = e.started()
	project = workerProject(t, f)
	f.mu.Lock()
	folder = f.projects[project].Folder
	f.mu.Unlock()
	return e, f, project, folder, home
}

func writeClaudeConversation(t *testing.T, home, cwd, id string) string {
	t.Helper()
	dir := harness.ClaudeProjectDir(home, cwd)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A Claude agent launched with a conversation id resumes that conversation: no new session id,
// nothing sent (the brief is not replayed), and the owner's transcript is copied, not moved.
func TestLaunchIntoClaudeConversation(t *testing.T) {
	e, f, project, folder, home := convEnv(t)
	src := writeClaudeConversation(t, home, folder, convClaude)

	a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "moved", ProjectID: project, Brief: "standing brief", ConversationID: convClaude})
	if err != nil {
		t.Fatal(err)
	}
	if a.SessionID != convClaude || a.Cwd != folder {
		t.Fatalf("agent: session %q cwd %q", a.SessionID, a.Cwd)
	}
	p := e.chat.Last(a.ID)
	if p.Spec.SessionID != convClaude || slices.Contains(p.Spec.ExtraArgs, "--session-id") {
		t.Fatalf("not resumed: session %q args %v", p.Spec.SessionID, p.Spec.ExtraArgs)
	}
	if sent := p.Sent(); len(sent) != 0 {
		t.Fatalf("something was sent into the resumed conversation: %q", sent)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatal("the owner's transcript must stay where it was")
	}
	f.mu.Lock()
	root, _ := f.claudeConfigDirLocked(a)
	f.mu.Unlock()
	if !harness.ClaudeTranscriptExistsIn(root, folder, convClaude) {
		t.Fatal("the transcript was not copied into the agents' Claude folder")
	}

	// The same conversation cannot be given to a second agent.
	if _, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "twice", ProjectID: project, ConversationID: convClaude}); KindOf(err) != KindConflict {
		t.Fatalf("second agent on the same conversation: %v", err)
	}
}

// With a prompt, that prompt is the next turn of the resumed conversation: one turn, the brief
// not replayed as a turn of its own. The conversation existed before this agent did, so the
// turn starts with what every start on an earlier conversation gets: the agent's instructions
// as they are now, the brief among them (what is given only at launch does not reach an
// existing conversation).
func TestLaunchIntoConversationWithPrompt(t *testing.T) {
	e, f, project, folder, home := convEnv(t)
	writeClaudeConversation(t, home, folder, convClaude)
	a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "moved", ProjectID: project, Brief: "standing brief", Prompt: "carry on", ConversationID: convClaude})
	if err != nil {
		t.Fatal(err)
	}
	sent := e.chat.Last(a.ID).Sent()
	if len(sent) != 1 || !strings.HasSuffix(sent[0], "\n\ncarry on") || !strings.HasPrefix(sent[0], "[tmx instructions: you were started again on your earlier conversation; these are your instructions as they are now]\n") ||
		!strings.Contains(sent[0], "Your brief: standing brief") || strings.Count(sent[0], "standing brief") != 1 {
		t.Fatalf("sent: %q", sent)
	}
}

// writeCodexRollout writes a Codex rollout as Codex does: a session_meta record with the id and
// the folder it was recorded in.
func writeCodexRollout(t *testing.T, codexHome, cwd, id string) {
	t.Helper()
	dir := filepath.Join(codexHome, "sessions", "2026", "11", "17")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]string{"id": id, "timestamp": "2026-11-17T10:00:00Z", "cwd": cwd}})
	if err := os.WriteFile(filepath.Join(dir, "rollout-x-"+id+".jsonl"), append(meta, 10), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLaunchIntoCodexConversation(t *testing.T) {
	e, f, project, folder, home := convEnv(t)
	codexHome := filepath.Join(home, ".codex")
	writeCodexRollout(t, codexHome, folder, convCodex)
	const elsewhere = "77777777-7777-4888-8999-bbbbbbbbbbbb"
	writeCodexRollout(t, codexHome, t.TempDir(), elsewhere) // recorded in another folder

	for name, id := range map[string]string{
		"no rollout with that id":              "99999999-7777-4888-8999-aaaaaaaaaaaa",
		"a rollout recorded in another folder": elsewhere,
	} {
		_, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "codex-no", ProjectID: project, Harness: model.HarnessCodex, ConversationID: id})
		if KindOf(err) != KindNotFound {
			t.Errorf("%s: got %v, want not found", name, err)
		}
	}

	a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "moved-codex", ProjectID: project, Harness: model.HarnessCodex, ConversationID: convCodex})
	if err != nil {
		t.Fatal(err)
	}
	r := e.chat
	if a.Mode == model.ModeTerminal {
		r = e.term
	}
	if a.SessionID != convCodex || r.Last(a.ID).Spec.SessionID != convCodex {
		t.Fatalf("codex not resumed: agent %q spec %q", a.SessionID, r.Last(a.ID).Spec.SessionID)
	}
}

func TestLaunchIntoConversationRefusals(t *testing.T) {
	_, f, project, folder, home := convEnv(t)
	writeClaudeConversation(t, home, folder, convClaude)
	f.mu.Lock()
	lead := f.projects[project].LeadID
	f.mu.Unlock()
	worker, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "plain-worker", ProjectID: project})
	if err != nil {
		t.Fatal(err)
	}
	before := f.agentCountForTest()

	cases := []struct {
		name string
		c    Caller
		req  LaunchRequest
		want Kind
	}{
		{"no such conversation", OwnerCaller, LaunchRequest{Name: "a1", ProjectID: project, ConversationID: "99999999-2222-4333-8444-555555555555"}, KindNotFound},
		{"a conversation of another folder", OwnerCaller, LaunchRequest{Name: "a2", ProjectID: project, Cwd: t.TempDir(), ConversationID: convClaude}, KindNotFound},
		{"not an id", OwnerCaller, LaunchRequest{Name: "a3", ProjectID: project, ConversationID: "../../etc"}, KindInvalid},
		{"a harness that cannot resume by id", OwnerCaller, LaunchRequest{Name: "a4", ProjectID: project, Harness: model.HarnessShell, ConversationID: convClaude}, KindInvalid},
		{"a lead", Caller{AgentID: lead}, LaunchRequest{Name: "a5", ProjectID: project, ConversationID: convClaude}, KindForbidden},
		{"a worker", Caller{AgentID: worker.ID}, LaunchRequest{Name: "a7", ProjectID: project, ConversationID: convClaude}, KindForbidden},
		{"Companion", CompanionCaller, LaunchRequest{Name: "a6", ProjectID: project, ConversationID: convClaude}, KindForbidden},
	}
	for _, tc := range cases {
		if _, err := f.LaunchAgent(tc.c, tc.req); KindOf(err) != tc.want {
			t.Errorf("%s: got %v, want kind %v", tc.name, err, tc.want)
		}
	}
	if n := f.agentCountForTest(); n != before {
		t.Fatalf("a refused launch left an agent behind: %d -> %d", before, n)
	}
	// The refusal of an agent is this feature's own rule, not another launch rule that happens
	// to say no: the same lead may launch a plain worker.
	f.mu.Lock()
	leadRec := f.agents[lead]
	_, rule := f.resumeConversationLocked(Caller{AgentID: lead}, &model.Agent{Harness: model.HarnessClaude, Cwd: folder}, convClaude)
	f.mu.Unlock()
	if leadRec == nil || KindOf(rule) != KindForbidden || !strings.Contains(rule.Error(), "existing conversation") {
		t.Fatalf("the lead is not refused by the conversation rule itself: %v", rule)
	}

	// A paired fleet machine may do it (it stands like the owner for agent operations here);
	// what it may relay for is decided at the API (launch handler), not here.
	f.mu.Lock()
	_, peerRule := f.resumeConversationLocked(PeerCaller("hosta"), &model.Agent{Harness: model.HarnessClaude, Cwd: folder}, convClaude)
	f.mu.Unlock()
	if peerRule != nil {
		t.Fatalf("a paired machine: %v", peerRule)
	}

	// The head may do it for the owner.
	if _, err := f.LaunchAgent(Caller{AgentID: f.headLockedForTest().ID}, LaunchRequest{Name: "by-head", ProjectID: project, ConversationID: convClaude}); err != nil {
		t.Fatalf("head: %v", err)
	}
}

func (f *Fleet) headLockedForTest() model.Agent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.headLocked().a
}

func (f *Fleet) agentCountForTest() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.agents)
}
