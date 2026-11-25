package fleet

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/harness"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
	"github.com/blackcode-ch/tmx-go/internal/runner/fake"
)

// The classic UI profile (config.json "ui.profile": "classic"; the owner, 17 Nov 2026): the
// dashboard is a copy of the Python one, where every agent ran in Claude Code's real screen.
// New Claude leads and workers default to their own screen. The head defaults to chat,
// and reopening an existing agent keeps its saved mode.

func TestWithoutTheClassicProfileClaudeStillStartsInChat(t *testing.T) {
	e := newUnguardedEnv(t)
	f := e.started()
	if got := mustAgent(t, f, HeadName).Mode; got != model.ModeChat {
		t.Fatalf("head mode = %q, want chat", got)
	}
	a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: workerProject(t, f), Name: "w"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Mode != model.ModeChat {
		t.Fatalf("worker mode = %q, want chat", a.Mode)
	}
	snap, err := f.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snap.UIProfile != "" {
		t.Fatalf("snapshot ui_profile = %q, want none", snap.UIProfile)
	}
	b, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "ui_profile") {
		t.Fatal("the snapshot of a default daemon must not carry ui_profile")
	}
}

func TestClassicProfileDefaultsHeadToChatAndOtherClaudeLaunchesToTerminal(t *testing.T) {
	t.Setenv("TMX_HEAD_HARNESS", string(model.HarnessClaude))
	e := newUnguardedEnv(t)
	e.uiProfile = config.UIProfileClassic
	f := e.started()

	head := mustAgent(t, f, HeadName)
	if head.Mode != model.ModeChat || head.HomeMode != "" {
		t.Fatalf("new Classic head: mode %q home %q, want chat", head.Mode, head.HomeMode)
	}
	eventually(t, "the head starts in chat", func() bool { return e.chat.Last(head.ID) != nil })
	if e.term.Last(head.ID) != nil {
		t.Fatal("the head was also started in terminal")
	}

	pid := workerProject(t, f)
	lead := leadOf(t, f, pid)
	if lead.Harness == model.HarnessClaude && lead.Mode != model.ModeTerminal {
		t.Fatalf("a project's Claude lead under classic: mode %q, want its own screen", lead.Mode)
	}

	// no mode named: the form's default, the CLI, the API, a lead's launch
	w, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: pid, Name: "w"})
	if err != nil {
		t.Fatal(err)
	}
	if w.Mode != model.ModeTerminal {
		t.Fatalf("Claude worker with no mode = %q, want its own screen", w.Mode)
	}
	// an explicit mode always wins
	c, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: pid, Name: "c", Mode: model.ModeChat})
	if err != nil {
		t.Fatal(err)
	}
	if c.Mode != model.ModeChat {
		t.Fatalf("explicit chat = %q", c.Mode)
	}
	// Codex stays in chat: its screen's conversation is not followed
	x, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: pid, Name: "x", Harness: model.HarnessCodex})
	if err != nil {
		t.Fatal(err)
	}
	if x.Mode != model.ModeChat {
		t.Fatalf("Codex with no mode = %q, want chat", x.Mode)
	}

	snap, err := f.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snap.UIProfile != config.UIProfileClassic {
		t.Fatalf("snapshot ui_profile = %q", snap.UIProfile)
	}
}

func TestClassicReopenKeepsSavedClaudeHeadMode(t *testing.T) {
	for _, mode := range []model.Mode{model.ModeChat, model.ModeTerminal} {
		t.Run(string(mode), func(t *testing.T) {
			t.Setenv("TMX_HEAD_HARNESS", string(model.HarnessClaude))
			e := newUnguardedEnv(t)
			f := e.started()
			head := mustAgent(t, f, HeadName)
			p := newProc(t, e.chat, head.ID, nil)
			sid := mustAgent(t, f, HeadName).SessionID
			p.Emit(runner.Event{Kind: runner.EvSession, SessionID: sid})
			p.Turn("ready", 0.01, 1)
			waitState(t, f, HeadName, model.StateIdle)
			if err := f.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			saved, err := e.st.GetAgent(head.ID)
			if err != nil {
				t.Fatal(err)
			}
			saved.Mode = mode
			if err := e.st.PutAgent(saved); err != nil {
				t.Fatal(err)
			}
			e.uiProfile = config.UIProfileClassic
			e.chat, e.term = fake.New(), fake.New()
			reopened := e.started()
			assertClassicHeadReopened(t, e, reopened, head.ID, sid, mode)
		})
	}
}

func TestClassicHeadLiveSwitchesSurviveReopen(t *testing.T) {
	t.Setenv("TMX_HEAD_HARNESS", string(model.HarnessClaude))
	quickSwitch(t)
	e := newUnguardedEnv(t)
	e.uiProfile = config.UIProfileClassic
	f := e.started()
	head := mustAgent(t, f, HeadName)
	p := newProc(t, e.chat, head.ID, nil)
	sid := mustAgent(t, f, HeadName).SessionID
	p.Emit(runner.Event{Kind: runner.EvSession, SessionID: sid})
	p.Turn("ready", 0.01, 1)
	waitState(t, f, HeadName, model.StateIdle)

	for _, to := range []model.Mode{model.ModeTerminal, model.ModeChat} {
		if _, err := f.SetMode(OwnerCaller, head.ID, ModeRequest{Mode: to}); err != nil {
			t.Fatal(err)
		}
		assertClassicHeadReopened(t, e, f, head.ID, sid, to)
		if err := f.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		e.chat, e.term = fake.New(), fake.New()
		f = e.started()
		assertClassicHeadReopened(t, e, f, head.ID, sid, to)
		run := e.chat
		if to == model.ModeTerminal {
			run = e.term
		}
		run.Last(head.ID).State(model.StateIdle)
		waitState(t, f, HeadName, model.StateIdle)
	}
}

func assertClassicHeadReopened(t *testing.T, e *env, f *Fleet, id, sid string, mode model.Mode) {
	t.Helper()
	run := e.chat
	if mode == model.ModeTerminal {
		run = e.term
	}
	p := newProc(t, run, id, nil)
	got := mustAgent(t, f, HeadName)
	if sid == "" || got.ID != id || got.Mode != mode || got.SessionID != sid || p.Spec.SessionID != sid {
		t.Fatalf("head %s in %s on session %q, runner resumes %q; want %s in %s on %q", got.ID, got.Mode, got.SessionID, p.Spec.SessionID, id, mode, sid)
	}
	stored, err := e.st.GetAgent(id)
	if err != nil || stored.Mode != mode || stored.SessionID != sid {
		t.Fatalf("stored mode %q and session %q: %v", stored.Mode, stored.SessionID, err)
	}
}

func TestClassicProfileLeavesACodexHeadInChat(t *testing.T) {
	e := newUnguardedEnv(t)
	t.Setenv("TMX_HEAD_HARNESS", string(model.HarnessCodex))
	e.uiProfile = config.UIProfileClassic
	f := e.started()
	if a := mustAgent(t, f, HeadName); a.Harness != model.HarnessCodex || a.Mode != model.ModeChat {
		t.Fatalf("Codex head under classic: %s in %q, want chat", a.Harness, a.Mode)
	}
}

func TestClassicDefaultNeedsATerminalRunner(t *testing.T) {
	claude, ok := harness.Get(model.HarnessClaude)
	if !ok {
		t.Fatal("no claude harness")
	}
	codex, _ := harness.Get(model.HarnessCodex)
	both := map[model.Mode]runner.Runner{model.ModeChat: fake.New(), model.ModeTerminal: fake.New()}
	chatOnly := map[model.Mode]runner.Runner{model.ModeChat: fake.New()}
	for _, c := range []struct {
		name    string
		profile string
		runners map[model.Mode]runner.Runner
		info    harness.Info
		want    model.Mode
	}{
		{"default dashboard", "", both, claude, model.ModeChat},
		{"classic", config.UIProfileClassic, both, claude, model.ModeTerminal},
		{"classic, Codex", config.UIProfileClassic, both, codex, model.ModeChat},
		{"classic, no terminal runner", config.UIProfileClassic, chatOnly, claude, model.ModeChat},
		{"an unknown profile is the default dashboard", "retro", both, claude, model.ModeChat},
	} {
		f := &Fleet{opts: Options{UIProfile: c.profile, Runners: c.runners}}
		if got := f.defaultModeLocked(c.info); got != c.want {
			t.Errorf("%s: default mode = %q, want %q", c.name, got, c.want)
		}
	}
}

func leadOf(t *testing.T, f *Fleet, projectID string) model.Agent {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.projects[projectID]
	if p == nil {
		t.Fatalf("no project %s", projectID)
	}
	r := f.agents[p.LeadID]
	if r == nil {
		t.Fatalf("project %s has no lead", projectID)
	}
	return r.a
}
