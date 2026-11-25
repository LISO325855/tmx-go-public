package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

func TestProfileAuthCleanupOnEveryExit(t *testing.T) {
	for _, ending := range []string{"stop", "natural", "crash", "failed-start", "failed-prepare"} {
		t.Run(ending, func(t *testing.T) {
			e := newUnguardedEnv(t)
			source := t.TempDir()
			t.Setenv("PI_CODING_AGENT_DIR", source)
			for _, name := range []string{"auth.json", "models.json", "settings.json"} {
				if err := os.WriteFile(filepath.Join(source, name), []byte(`{"fixture":true}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if ending == "failed-prepare" {
				if err := os.Remove(filepath.Join(source, "models.json")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(source, "models.json"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			f := e.started()
			if _, err := f.CreateProject(OwnerCaller, ProjectRequest{Name: "alpha"}); err != nil {
				t.Fatal(err)
			}
			if ending == "failed-start" {
				e.term.FailStarts(errors.New("fixture start failure"))
			}
			a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "cleanup", Harness: model.HarnessPi, Mode: model.ModeTerminal, ProjectID: "alpha"})
			if strings.HasPrefix(ending, "failed-") {
				if err != nil {
					t.Fatal(err)
				}
				if a.State != model.StateError {
					t.Fatal("expected launch error state", a.State)
				}
				a, err = f.Agent("cleanup")
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if ending == "stop" {
					if _, err := f.StopAgent(OwnerCaller, a.ID); err != nil {
						t.Fatal(err)
					}
				} else {
					// Prevent automatic recovery from recreating the files being checked.
					f.mu.Lock()
					f.agents[a.ID].a.Desired = "stopped"
					f.mu.Unlock()
					if ending == "crash" {
						e.term.Last(a.ID).Exit(1, errors.New("fixture crash"))
					} else {
						e.term.Last(a.ID).Exit(0, nil)
					}
				}
			}
			eventually(t, "private auth copies removed", func() bool {
				for _, name := range []string{"auth.json", "models.json", "settings.json"} {
					if _, err := os.Stat(filepath.Join(f.opts.Paths.AgentDir(a.ID), "profile-mcp", "pi", name)); !os.IsNotExist(err) {
						return false
					}
				}
				return true
			})
			if _, err := os.Stat(filepath.Join(source, "auth.json")); err != nil {
				t.Fatal("source auth removed", err)
			}
		})
	}
}

func TestPiWorkspaceCannotOverrideManagedMCP(t *testing.T) {
	for _, name := range []string{"tmxg", "arc-desktop", "arc_desktop"} {
		t.Run(name, func(t *testing.T) {
			e := newUnguardedEnv(t)
			e.ext = fakeClaudeExt(t)
			t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
			f := e.started()
			if _, err := f.CreateProject(OwnerCaller, ProjectRequest{Name: "alpha"}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.CreateProfile(OwnerCaller, model.Profile{Slug: "tools", Name: "Tools", Level: model.LevelWorker, Model: sonnet, MCPServers: []string{"arc-desktop"}}); err != nil {
				t.Fatal(err)
			}
			a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "pi-tools", Harness: model.HarnessPi, Mode: model.ModeTerminal, Profile: "tools", ProjectID: "alpha"})
			if err != nil {
				t.Fatal(err)
			}
			spec := e.term.Last(a.ID).Spec
			if _, err := f.StopAgent(OwnerCaller, a.ID); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(spec.Cwd, ".pi")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "mcp.json")
			body := []byte(`{"mcpServers":{"` + name + `":{"enabled":false}}}`)
			if err := os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := f.StartAgent(OwnerCaller, a.ID); err == nil {
				t.Fatal("accepted workspace override")
			}
			a, _ = f.Agent(a.ID)
			p, _ := f.GetProfile("tools")
			if len(a.MCPWarnings) == 0 || len(p.MCPWarnings) == 0 {
				t.Fatal("missing visible warning")
			}
			if got, _ := os.ReadFile(path); string(got) != string(body) {
				t.Fatal("changed user workspace config")
			}
		})
	}
}

func TestDetachedProfileAuthSurvivesAndStaleExitCannotDeleteReplacement(t *testing.T) {
	e := newUnguardedEnv(t)
	source := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", source)
	if err := os.WriteFile(filepath.Join(source, "auth.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	f := e.started()
	if _, err := f.CreateProject(OwnerCaller, ProjectRequest{Name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "detached", Harness: model.HarnessPi, Mode: model.ModeTerminal, ProjectID: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	proc := e.term.Last(a.ID)
	f.mu.Lock()
	r := f.agents[a.ID]
	r.detaching = true
	f.mu.Unlock()
	f.handleExit(r, proc, 0, nil)
	path := filepath.Join(f.opts.Paths.AgentDir(a.ID), "profile-mcp", "pi", "auth.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatal("detached host lost auth", err)
	}
	// A stale process exit cannot remove the current generation's files.
	f.handleExit(r, proc, 0, nil)
	if _, err := os.Stat(path); err != nil {
		t.Fatal("stale exit removed auth", err)
	}
	f.mu.Lock()
	r.proc = proc // let normal test shutdown stop the fixture process
	f.mu.Unlock()
}
