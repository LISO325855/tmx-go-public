package fleet

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/accounts"
	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner/claude"
)

// An agent runs as the account its launch names, else its harness's default; a Claude agent's
// CLAUDE_CONFIG_DIR is then that account's home, and an unknown account is refused.
func TestLaunchAccount(t *testing.T) {
	e := newEnv(t)
	p, err := accounts.OpenProfiles(filepath.Join(e.home, "ai-accounts"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Add(model.HarnessClaude, "second"); err != nil {
		t.Fatal(err)
	}
	e.accounts = p
	f := e.started()
	proj := workerProject(t, f)

	w, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: proj, Name: "w-second", Account: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if w.Account != "second" {
		t.Fatalf("account %q", w.Account)
	}
	eventually(t, "started", func() bool { return e.chat.Last(w.ID) != nil })
	if dir := claude.ConfigDir(e.chat.Last(w.ID).Spec.Env); dir != p.Home(model.HarnessClaude, "second") {
		t.Fatalf("CLAUDE_CONFIG_DIR %q", dir)
	}

	d, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: proj, Name: "w-default"})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "started", func() bool { return e.chat.Last(d.ID) != nil })
	if d.Account != accounts.DefaultName {
		t.Fatalf("account %q", d.Account)
	}
	assertIsolatedClaude(t, e, e.chat.Last(d.ID).Spec) // the daemon's own folder, as before

	if err := p.SetDefault(model.HarnessClaude, "second"); err != nil {
		t.Fatal(err)
	}
	n, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: proj, Name: "w-newdefault"})
	if err != nil || n.Account != "second" {
		t.Fatalf("launch default: %q %v", n.Account, err)
	}

	if _, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: proj, Name: "w-bad", Account: "nope"}); err == nil {
		t.Fatal("an unknown account was accepted")
	}
}

// The CLAUDE_CONFIG_DIR the launch's hooks check reads: the account's folder, else the daemon's.
func TestAgentClaudeConfigDir(t *testing.T) {
	e := newEnv(t)
	p, _ := accounts.OpenProfiles(filepath.Join(e.home, "ai-accounts"))
	_ = p.Add(model.HarnessClaude, "second")
	f := &Fleet{opts: Options{Accounts: p, Paths: config.Paths{Home: e.home}}}
	if d := f.agentClaudeConfigDirLocked(model.Agent{Harness: model.HarnessClaude, Account: "second"}); d != p.Home(model.HarnessClaude, "second") {
		t.Errorf("account: %s", d)
	}
	if d := f.agentClaudeConfigDirLocked(model.Agent{Harness: model.HarnessClaude, Account: accounts.DefaultName}); d != (config.Paths{Home: e.home}).ClaudeConfigDir() {
		t.Errorf("default: %s", d)
	}
}

// An account's own CLAUDE_CONFIG_DIR with the hooks switched off: that account's agents start
// with the guard warning; the default account's start guarded (tmx-lead, 3 Nov: #234 wired to
// #236; the owner, 17 Nov: a warning, not a refusal).
func TestHooksDisabledInAccountWarns(t *testing.T) {
	e := newEnv(t)
	p, err := accounts.OpenProfiles(filepath.Join(e.home, "ai-accounts"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Add(model.HarnessClaude, "second"); err != nil {
		t.Fatal(err)
	}
	e.accounts = p
	f := e.started()
	proj := workerProject(t, f)
	home := p.Home(model.HarnessClaude, "second")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(`{"disableAllHooks": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: proj, Name: "w-second", Account: "second"})
	if err != nil || !strings.Contains(lastGuardWarning(t, f, w.ID), "disableAllHooks") {
		t.Fatalf("launch on an account with hooks off: %v, warning %q", err, lastGuardWarning(t, f, w.ID))
	}
	d, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: proj, Name: "w-default"})
	if err != nil || guardWarnings(t, f, d.ID) != 0 {
		t.Fatalf("the default account: %v", err)
	}
}

func TestAccountEnvForOtherHarnesses(t *testing.T) {
	p, _ := accounts.OpenProfiles(t.TempDir())
	_ = p.Add(model.HarnessCodex, "work")
	_ = p.Add("antigravity", "work")
	f := &Fleet{opts: Options{Accounts: p}}
	codex := f.accountEnvLocked(model.Agent{Harness: model.HarnessCodex, Account: "work"})
	if !slices.Equal(codex, []string{"CODEX_HOME=" + p.Home(model.HarnessCodex, "work")}) {
		t.Errorf("codex %v", codex)
	}
	agy := f.accountEnvLocked(model.Agent{Harness: "agy", Account: "work"})
	if home := p.Home("antigravity", "work"); !slices.Equal(agy, []string{"HOME=" + home, "USERPROFILE=" + home, "APPDATA=" + filepath.Join(home, "AppData", "Roaming"), "LOCALAPPDATA=" + filepath.Join(home, "AppData", "Local"), "XDG_CONFIG_HOME=" + filepath.Join(home, ".config")}) {
		t.Errorf("agy %v", agy)
	}
	if env := f.accountEnvLocked(model.Agent{Harness: model.HarnessCodex, Account: accounts.DefaultName}); env != nil {
		t.Errorf("default %v", env)
	}
	if env := f.accountEnvLocked(model.Agent{Harness: model.HarnessOpenCode, Account: "work"}); env != nil {
		t.Errorf("opencode %v", env)
	}
}

// An agent keeps its account across a daemon restart (it was held in memory only: after a
// restart the agent would have started in the default account's home, where its conversation
// is not; hosta-go, 17 Nov).
func TestAgentKeepsItsAccountAcrossARestart(t *testing.T) {
	e := newEnv(t)
	p, err := accounts.OpenProfiles(filepath.Join(e.home, "ai-accounts"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Add(model.HarnessClaude, "second"); err != nil {
		t.Fatal(err)
	}
	e.accounts = p
	f := e.started()
	w, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: workerProject(t, f), Name: "w-second", Account: "second"})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "started", func() bool { return e.chat.Last(w.ID) != nil })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.Close(ctx); err != nil {
		t.Fatal(err)
	}

	f2 := e.started() // the daemon comes back on the same store
	got, err := f2.Agent("w-second")
	if err != nil || got.Account != "second" {
		t.Fatalf("after the restart: account %q, %v", got.Account, err)
	}
	f2.mu.Lock()
	dir := f2.agentClaudeConfigDirLocked(got)
	f2.mu.Unlock()
	if dir != p.Home(model.HarnessClaude, "second") {
		t.Fatalf("after the restart the agent's Claude folder is %q, want the account's home", dir)
	}
}

func TestIsolatedDefaultAccountEnvironment(t *testing.T) {
	home := t.TempDir()
	p, err := accounts.OpenProfiles(filepath.Join(home, "ai-accounts"))
	if err != nil {
		t.Fatal(err)
	}
	p.IsolateDefaults(home)
	f := &Fleet{opts: Options{Accounts: p}}
	for _, h := range []model.Harness{model.HarnessClaude, model.HarnessCodex, model.HarnessGemini, "agy"} {
		for _, name := range []string{"", accounts.DefaultName} {
			a := model.Agent{Harness: h, Account: name}
			if got := f.accountEnvLocked(a); len(got) == 0 || !slices.Equal(got, p.Env(AccountHarness(h), name)) {
				t.Fatalf("%s/%s: %v", h, name, got)
			}
			if got := f.accountHomeLocked(a); got == "" || got != p.Home(AccountHarness(h), name) {
				t.Fatalf("%s/%s: %s", h, name, got)
			}
		}
	}
}
