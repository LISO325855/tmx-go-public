package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/accounts"
	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/harness"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
	"github.com/blackcode-ch/tmx-go/internal/runner/claude"
	"github.com/blackcode-ch/tmx-go/internal/store"
)

func TestClaudeFailoverResumesAndNotifiesOnce(t *testing.T) {
	testClaudeFailover(t, false)
}

func TestClaudeFailoverFailureKeepsOriginalResumePath(t *testing.T) {
	testClaudeFailover(t, true)
}

func TestClaudeFailoverNewDefaultAndNoAlternate(t *testing.T) {
	e := newEnv(t)
	profiles, err := accounts.OpenProfiles(filepath.Join(e.home, "ai-accounts"))
	if err != nil {
		t.Fatal(err)
	}
	if err := profiles.Add(model.HarnessClaude, "alternate"); err != nil {
		t.Fatal(err)
	}
	e.accounts = profiles
	f := e.started()
	project := workerProject(t, f)
	f.SetClaudeFailoverSelector(func(account, model string) string { return "alternate" })
	launch := func(name, account string) model.Agent {
		a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{
			ProjectID: project, Name: name, Harness: model.HarnessClaude, Mode: model.ModeChat, Account: account,
		})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	if a := launch("implicit", ""); a.Account != "alternate" {
		t.Fatalf("exhausted default selected %q", a.Account)
	}
	explicit := launch("explicit", accounts.DefaultName)
	if explicit.Account != accounts.DefaultName {
		t.Fatalf("explicit launch selected %q", explicit.Account)
	}
	prior := e.chat.Last(explicit.ID)
	f.SetClaudeFailoverSelector(func(account, model string) string { return "" })
	f.FailoverClaudeAccount(accounts.DefaultName)
	f.mu.Lock()
	switching := f.agents[explicit.ID].accountSwitch
	f.mu.Unlock()
	if switching || e.chat.Last(explicit.ID) != prior {
		t.Fatal("agent restarted with no alternate")
	}
}

func testClaudeFailover(t *testing.T, failBeforeReady bool) {
	t.Helper()
	e := newEnv(t)
	profiles, err := accounts.OpenProfiles(filepath.Join(e.home, "ai-accounts"))
	if err != nil {
		t.Fatal(err)
	}
	if err := profiles.Add(model.HarnessClaude, "alternate"); err != nil {
		t.Fatal(err)
	}
	e.accounts = profiles
	f := e.started()
	agent, err := f.LaunchAgent(OwnerCaller, LaunchRequest{
		ProjectID: workerProject(t, f), Name: "limited", Harness: model.HarnessClaude, Mode: model.ModeChat,
	})
	if err != nil {
		t.Fatal(err)
	}
	prior := e.chat.Last(agent.ID)
	if prior == nil {
		t.Fatal("source did not start")
	}
	const session = "ab123456-7890-1234-89ab-1234567890ab"
	const history = "{\"type\":\"user\",\"message\":\"existing context\"}\n"
	source := config.Paths{Home: e.home}.ClaudeConfigDir()
	destination := profiles.Home(model.HarnessClaude, "alternate")
	transcript := func(home string) string {
		return filepath.Join(harness.ClaudeProjectDirIn(home, agent.Cwd), session+".jsonl")
	}
	if err := os.MkdirAll(filepath.Dir(transcript(source)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript(source), []byte(history), 0o600); err != nil {
		t.Fatal(err)
	}
	prior.Emit(runner.Event{Kind: runner.EvSession, SessionID: session})
	eventually(t, "source UUID persisted", func() bool { return mustAgent(t, f, agent.ID).SessionID == session })
	f.SetClaudeFailoverSelector(func(account, model string) string {
		if account == accounts.DefaultName {
			return "alternate"
		}
		return ""
	})
	for range 5 {
		f.FailoverClaudeAccount(accounts.DefaultName)
	}
	eventually(t, "alternate starts", func() bool {
		p := e.chat.Last(agent.ID)
		return p != nil && p != prior && claude.ConfigDir(p.Spec.Env) == destination
	})
	next := e.chat.Last(agent.ID)
	if next.Spec.SessionID != session || len(next.Sent()) != 0 {
		t.Fatalf("alternate start did not resume quietly: %+v", next.Spec)
	}
	for _, dir := range []string{source, destination} {
		if got, err := os.ReadFile(transcript(dir)); err != nil || string(got) != history {
			t.Fatalf("%s resume path: %q, %v", dir, got, err)
		}
	}
	persisted, err := e.st.GetAgent(agent.ID)
	if err != nil || persisted.Account != accounts.DefaultName || persisted.SessionID != session {
		t.Fatalf("source must remain durable until alternate ready: %+v, %v", persisted, err)
	}
	countNotices := func() int {
		mail, err := e.st.ListMessages(store.MessageFilter{Party: f.HeadID()})
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, m := range mail {
			if m.From == model.WatchParty && strings.Contains(m.Body, "Claude account switched for limited") {
				n++
			}
		}
		return n
	}
	if failBeforeReady {
		next.Exit(1, errors.New("the alternate failed before readiness"))
		eventually(t, "original resumes", func() bool {
			p := e.chat.Last(agent.ID)
			return p != nil && p != next && p != prior && claude.ConfigDir(p.Spec.Env) == source && p.Spec.SessionID == session
		})
		if current := mustAgent(t, f, agent.ID); current.Account != accounts.DefaultName || current.SessionID != session || countNotices() != 0 {
			t.Fatalf("failed switch changed the resume path or issued a notice: %+v", current)
		}
		return
	}
	next.Emit(runner.Event{Kind: runner.EvState, State: model.StateIdle})
	next.Emit(runner.Event{Kind: runner.EvState, State: model.StateIdle})
	eventually(t, "one head notice", func() bool { return countNotices() == 1 })
	for range 5 {
		f.FailoverClaudeAccount(accounts.DefaultName)
	}
	if current := mustAgent(t, f, agent.ID); current.Account != "alternate" || current.SessionID != session || countNotices() != 1 {
		t.Fatalf("duplicate event restarted the agent or sent another notice: %+v", current)
	}
}
