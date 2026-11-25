package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/harness"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
)

const lostSession = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"

func timelineHas(t *testing.T, f *Fleet, id, part string) bool {
	t.Helper()
	entries, err := f.Timeline(id, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Text, part) {
			return true
		}
	}
	return false
}

// An agent whose resume fails because the harness does not have the conversation is started
// once with a new conversation, with a timeline line, and without the crash-recovery restarts.
// If the new conversation fails the same way, the usual recovery takes over.
func TestResumeOfALostConversationStartsFreshOnce(t *testing.T) {
	e := newUnguardedEnv(t)
	f := e.started()
	a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: workerProject(t, f), Name: "lost"})
	if err != nil {
		t.Fatal(err)
	}
	first := e.chat.Last(a.ID)
	first.Emit(runner.Event{Kind: runner.EvSession, SessionID: lostSession})
	first.Turn("on it", 0.1, 1)
	eventually(t, "session known", func() bool { return mustAgent(t, f, "lost").SessionID == lostSession })

	// As claude --resume ends when the transcript is gone.
	first.Exit(1, errors.New("exit status 1: No conversation found with session ID: "+lostSession))
	eventually(t, "a fresh process", func() bool { p := e.chat.Last(a.ID); return p != first && p != nil })
	fresh := e.chat.Last(a.ID)
	got := mustAgent(t, f, "lost")
	if got.SessionID == lostSession || got.SessionID == "" {
		t.Fatalf("the agent still has the lost conversation, or none: %q", got.SessionID)
	}
	if fresh.Spec.SessionID != "" || !slices.Contains(fresh.Spec.ExtraArgs, "--session-id") || !slices.Contains(fresh.Spec.ExtraArgs, got.SessionID) {
		t.Fatalf("not a new conversation: resume %q args %v", fresh.Spec.SessionID, fresh.Spec.ExtraArgs)
	}
	if sent := fresh.Sent(); len(sent) != 0 {
		t.Fatalf("something was sent into the fresh conversation: %q", sent)
	}
	if !timelineHas(t, f, a.ID, "Previous conversation "+lostSession+" not found by claude; started fresh") {
		t.Fatal("no timeline line about the fresh start")
	}
	if timelineHas(t, f, a.ID, "Automatic restart") {
		t.Fatal("the crash recovery was scheduled as well")
	}
	if got.Desired != "running" {
		t.Fatalf("desired %q", got.Desired)
	}

	// The same words about the new conversation: no second fresh start, the usual recovery.
	fresh.Emit(runner.Event{Kind: runner.EvSession, SessionID: got.SessionID})
	fresh.Exit(1, errors.New("No conversation found with session ID: "+got.SessionID))
	eventually(t, "the usual recovery", func() bool { return timelineHas(t, f, a.ID, "Automatic restart 1 scheduled") })
	if after := mustAgent(t, f, "lost"); after.SessionID != got.SessionID {
		t.Fatalf("a second conversation was thrown away: %q -> %q", got.SessionID, after.SessionID)
	}
	if e.chat.Last(a.ID) != fresh {
		t.Fatal("a second fresh start happened at once")
	}
}

// Any other failure keeps the conversation and takes the usual recovery.
func TestOtherExitKeepsTheConversation(t *testing.T) {
	e := newUnguardedEnv(t)
	f := e.started()
	a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: workerProject(t, f), Name: "kept"})
	if err != nil {
		t.Fatal(err)
	}
	p := e.chat.Last(a.ID)
	p.Emit(runner.Event{Kind: runner.EvSession, SessionID: lostSession})
	p.Turn("on it", 0.1, 1)
	eventually(t, "session known", func() bool { return mustAgent(t, f, "kept").SessionID == lostSession })
	p.Exit(1, errors.New("exit status 1: API error: overloaded"))
	eventually(t, "the usual recovery", func() bool { return timelineHas(t, f, a.ID, "Automatic restart 1 scheduled") })
	if got := mustAgent(t, f, "kept"); got.SessionID != lostSession || got.State != model.StateError {
		t.Fatalf("session %q state %s", got.SessionID, got.State)
	}
	if timelineHas(t, f, a.ID, "started fresh") {
		t.Fatal("a conversation was thrown away for an unrelated error")
	}
}

func TestConversationLost(t *testing.T) {
	for text, want := range map[string]bool{
		"exit status 1: No conversation found with session ID: 1234": true,
		"no conversation found with session id 1234":                 true,
		"exit status 1: API error: overloaded":                       false,
		"Session ID 1234 is already in use":                          false,
		"":                                                           false,
	} {
		if got := conversationLost(errors.New(text)); got != want {
			t.Errorf("%q: %v, want %v", text, got, want)
		}
	}
	if conversationLost(nil) {
		t.Error("nil error")
	}
}

// Mail that was given to the process whose resume failed is not lost: nothing consumed it (the
// harness refused the conversation), so it goes back to the queue and the fresh start gets it,
// once. Mail delivered and answered before stays delivered.
func TestMailGivenToALostConversationIsDeliveredAgain(t *testing.T) {
	e := newUnguardedEnv(t)
	f := e.started()
	a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: workerProject(t, f), Name: "mail-lost"})
	if err != nil {
		t.Fatal(err)
	}
	first := e.chat.Last(a.ID)
	first.Emit(runner.Event{Kind: runner.EvSession, SessionID: lostSession})
	first.State(model.StateIdle)
	eventually(t, "idle", func() bool { return mustAgent(t, f, "mail-lost").State == model.StateIdle })

	// One message is delivered and its turn finishes: settled, never replayed.
	if _, err := f.SendOwner(a.ID, "an earlier, answered message", ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the earlier message was delivered", func() bool { return sentContains(first, "an earlier, answered message") })
	first.Turn("answered", 0.1, 1)
	first.State(model.StateIdle)
	eventually(t, "idle again", func() bool { return mustAgent(t, f, "mail-lost").State == model.StateIdle })

	// The process is restarted by this daemon (as at the incident: a plain restart) and the
	// next message goes to the new process, which then turns out to have no conversation.
	if _, err := f.RestartAgent(OwnerCaller, a.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "restarted", func() bool { p := e.chat.Last(a.ID); return p != nil && p != first })
	second := e.chat.Last(a.ID)
	second.State(model.StateIdle)
	if _, err := f.SendOwner(a.ID, "reply with the word ok", ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "delivered to the process that will fail", func() bool { return sentContains(second, "reply with the word ok") })
	second.Exit(1, errors.New("No conversation found with session ID: "+lostSession))

	eventually(t, "a fresh process", func() bool { p := e.chat.Last(a.ID); return p != second && p != first })
	fresh := e.chat.Last(a.ID)
	if !timelineHas(t, f, a.ID, "1 message given to the start that failed went back to the queue") {
		t.Fatal("the timeline does not say the message was queued again")
	}
	fresh.State(model.StateIdle)
	eventually(t, "delivered again to the fresh conversation", func() bool { return sentContains(fresh, "reply with the word ok") })
	n := 0
	for _, s := range fresh.Sent() {
		if strings.Contains(s, "reply with the word ok") {
			n++
		}
		if strings.Contains(s, "an earlier, answered message") {
			t.Fatal("a message that was answered before was delivered again")
		}
	}
	if n != 1 {
		t.Fatalf("the message reached the fresh conversation %d times", n)
	}
}

func sentContains(p interface{ Sent() []string }, part string) bool {
	for _, s := range p.Sent() {
		if strings.Contains(s, part) {
			return true
		}
	}
	return false
}

// A conversation that its process named but never wrote (it ended before any turn) is not
// resumed: the next start begins it as a new conversation under the same id. A conversation
// that ever answered, or that has a transcript, is resumed as before.
func TestAStartDoesNotResumeAConversationThatWasNeverWritten(t *testing.T) {
	e := newUnguardedEnv(t)
	f := e.started()
	project := workerProject(t, f)
	restart := func(name string, prepare func(a model.Agent, first interface {
		Emit(runner.Event)
		Turn(string, float64, int)
		State(model.AgentState)
	})) (model.Agent, runner.Spec) {
		t.Helper()
		a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: project, Name: name})
		if err != nil {
			t.Fatal(err)
		}
		first := e.chat.Last(a.ID)
		sid := lostSession[:len(lostSession)-2] + map[string]string{"ph": "01", "an": "02", "tr": "03"}[name]
		first.Emit(runner.Event{Kind: runner.EvSession, SessionID: sid})
		eventually(t, "session known", func() bool { return mustAgent(t, f, name).SessionID == sid })
		prepare(mustAgent(t, f, name), first)
		if _, err := f.StopAgent(OwnerCaller, a.ID); err != nil {
			t.Fatal(err)
		}
		eventually(t, "stopped", func() bool { return mustAgent(t, f, name).State == model.StateStopped })
		if _, err := f.StartAgent(OwnerCaller, a.ID); err != nil {
			t.Fatal(err)
		}
		eventually(t, "started again", func() bool { p := e.chat.Last(a.ID); return p != nil && p != first })
		return mustAgent(t, f, name), e.chat.Last(a.ID).Spec
	}
	none := func(model.Agent, interface {
		Emit(runner.Event)
		Turn(string, float64, int)
		State(model.AgentState)
	}) {
	}

	// Named, never written, never answered: a new conversation under the same id.
	a, spec := restart("ph", none)
	if spec.SessionID != "" || !slices.Contains(spec.ExtraArgs, "--session-id") || !slices.Contains(spec.ExtraArgs, a.SessionID) {
		t.Fatalf("a conversation that was never written was resumed: resume %q args %v", spec.SessionID, spec.ExtraArgs)
	}
	if !timelineHas(t, f, a.ID, "was never written by claude and never answered") {
		t.Fatal("the timeline does not say so")
	}

	// It answered once: resumed, although this test's Claude folder has no transcript file.
	a, spec = restart("an", func(_ model.Agent, first interface {
		Emit(runner.Event)
		Turn(string, float64, int)
		State(model.AgentState)
	}) {
		first.Turn("an answer", 0.1, 1)
		first.State(model.StateIdle)
		eventually(t, "answered", func() bool { return mustAgent(t, f, "an").Turns > 0 })
	})
	if spec.SessionID != a.SessionID || slices.Contains(spec.ExtraArgs, "--session-id") {
		t.Fatalf("a conversation that answered was not resumed: resume %q args %v", spec.SessionID, spec.ExtraArgs)
	}

	// Never answered, but Claude wrote a transcript: resumed.
	a, spec = restart("tr", func(a model.Agent, _ interface {
		Emit(runner.Event)
		Turn(string, float64, int)
		State(model.AgentState)
	}) {
		f.mu.Lock()
		root, err := f.claudeConfigDirLocked(a)
		f.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		dir := harness.ClaudeProjectDirIn(root, a.Cwd)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, a.SessionID+".jsonl"), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if spec.SessionID != a.SessionID || slices.Contains(spec.ExtraArgs, "--session-id") {
		t.Fatalf("a conversation with a transcript was not resumed: resume %q args %v", spec.SessionID, spec.ExtraArgs)
	}
	if timelineHas(t, f, a.ID, "never written") {
		t.Fatal("a conversation with a transcript was called never written")
	}
}
