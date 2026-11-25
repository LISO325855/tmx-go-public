package fleet

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/guard"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
)

func TestOwnerShellSurvivesAgentAdmissionFailure(t *testing.T) {
	oldOS := launchGOOS
	t.Cleanup(func() { launchGOOS = oldOS })
	for _, platform := range []string{"windows", "linux", "darwin"} {
		t.Run(platform, func(t *testing.T) {
			launchGOOS = platform
			e := newEnv(t)
			f := e.fleet()
			f.opts.Elevated = func() bool { return true }
			if err := f.Start(); err != nil {
				t.Fatal(err)
			}
			head := mustAgent(t, f, HeadName)
			if head.State != model.StateError || !strings.Contains(head.Error, "elevated") || e.chat.Last(head.ID) != nil {
				t.Fatalf("head not quarantined: %+v", head)
			}
			sh, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "owner-shell", Harness: model.HarnessShell})
			if err != nil || e.term.Last(sh.ID) == nil {
				t.Fatalf("owner shell unavailable: %v", err)
			}
			if e.term.Last(sh.ID).Spec.SettingsPath != "" {
				t.Fatal("agent guard installed on owner shell")
			}
			if _, err := f.RestartAgent(OwnerCaller, sh.ID); err != nil {
				t.Fatalf("owner shell restart: %v", err)
			}
			if guardWarnings(t, f, sh.ID) != 0 {
				t.Fatal("the owner's shell got a guard warning")
			}
			if _, err := f.LaunchAgent(AgentCaller(head.ID), LaunchRequest{Name: "agent-shell", Harness: model.HarnessShell}); KindOf(err) != KindForbidden {
				t.Fatalf("agent launched shell: %v", err)
			}
		})
	}
}

func TestStartupQuarantinesUnsafeWorkspace(t *testing.T) {
	e := newEnv(t)
	e.workspace = t.TempDir()
	t.Setenv("SystemRoot", e.workspace)
	f := e.started()
	head := mustAgent(t, f, HeadName)
	if head.State != model.StateError || head.Error == "" || e.chat.Last(head.ID) != nil {
		t.Fatalf("unsafe head not quarantined: %+v", head)
	}
	if _, err := f.StartAgent(OwnerCaller, head.ID); KindOf(err) != KindForbidden {
		t.Fatalf("unsafe head restart allowed: %v", err)
	}
	sh, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "recovery-shell", Harness: model.HarnessShell})
	if err != nil || e.term.Last(sh.ID) == nil {
		t.Fatalf("owner recovery shell unavailable: %v", err)
	}
}

func TestLaunchAdmissionAllHarnesses(t *testing.T) {
	e := newEnv(t)
	f := e.fleet()
	root := filepath.VolumeName(e.workspace) + string(filepath.Separator)
	for _, h := range []model.Harness{model.HarnessClaude, model.HarnessCodex, model.HarnessOpenCode, model.HarnessPi} {
		for _, cwd := range []string{root, filepath.Join(root, "Windows"), filepath.Join(root, "Program Files"), filepath.Join(root, "ProgramData"), filepath.Join(root, "Windows", "System32")} {
			t.Run(string(h)+"/"+cwd, func(t *testing.T) {
				f.mu.Lock()
				defer f.mu.Unlock()
				_, err := f.prepareLaunchLocked(OwnerCaller, LaunchRequest{Name: "unsafe", Level: model.LevelWorker, Harness: h, Cwd: cwd})
				if err == nil {
					t.Fatal("unsafe cwd admitted")
				}
			})
		}
	}
}

// API POST /agents and MCP tmx_launch both call LaunchAgent. These tests
// exercise that common boundary as owner and as an enrolled head, plus the
// independent creation, restart, spawn and paused-process entry points.
func TestLaunchAdmissionEntryPoints(t *testing.T) {
	e := newEnv(t)
	f := e.fleet()
	f.res.paused = map[string]*pauseRec{}
	home, system := t.TempDir(), t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Setenv("SystemRoot", system)
	f.agents["head"] = &agentRec{a: model.Agent{ID: "head", Name: "head", Harness: model.HarnessClaude, Level: model.LevelHead, Profile: "head", Cwd: e.workspace}}
	for _, h := range []model.Harness{model.HarnessClaude, model.HarnessCodex, model.HarnessOpenCode, model.HarnessPi} {
		for _, cwd := range []string{home, system, filepath.Join(system, "new-child")} {
			for _, entry := range []string{"api", "tmx_launch", "project", "start", "restart", "resume", "auto-resume", "spawn"} {
				t.Run(string(h)+"/"+filepath.Base(cwd)+"/"+entry, func(t *testing.T) {
					r := &agentRec{a: model.Agent{ID: "existing", Name: "existing", Harness: h, Mode: model.ModeTerminal, Level: model.LevelWorker, Cwd: cwd, State: model.StatePaused, SessionID: "keep-session"}}
					f.mu.Lock()
					f.agents[r.a.ID] = r
					f.res.paused[r.a.ID] = &pauseRec{prev: model.StateIdle}
					f.mu.Unlock()
					var err error
					switch entry {
					case "api", "tmx_launch":
						c := OwnerCaller
						if entry == "tmx_launch" {
							c = AgentCaller("head")
						}
						_, err = f.LaunchAgent(c, LaunchRequest{Name: "unsafe", Harness: h, Level: model.LevelWorker, Cwd: cwd})
					case "project":
						_, err = f.CreateProject(OwnerCaller, ProjectRequest{Name: "unsafe", Harness: h, Folder: cwd})
					case "start":
						_, err = f.StartAgent(OwnerCaller, r.a.ID)
					case "restart":
						_, err = f.RestartAgentWith(OwnerCaller, r.a.ID, RestartOptions{Fresh: true})
					case "resume":
						_, err = f.ResumeAgent(OwnerCaller, r.a.ID)
					case "auto-resume":
						// Watchdog restoration is not a new launch: changed policy
						// must not leave fleet bookkeeping paused indefinitely.
						if !f.UnbrakeAgent(r.a.ID, "", "safety recovery") {
							t.Fatal("automatic restoration refused")
						}
						f.mu.Lock()
						defer f.mu.Unlock()
						if f.res.paused[r.a.ID] != nil || r.a.State == model.StatePaused {
							t.Fatal("automatic restoration left stale pause state")
						}
						return
					case "spawn":
						f.mu.Lock()
						_, err = f.specLocked(r, extServers{})
						f.mu.Unlock()
					}
					f.mu.Lock()
					defer f.mu.Unlock()
					if err == nil {
						t.Fatal("unsafe launch accepted")
					}
					// Agent-owned shells and unsupported project modes can be rejected even earlier.
					if !(h == model.HarnessShell && entry == "tmx_launch") && !(entry == "project" && (h == model.HarnessShell || h == model.HarnessOpenCode || h == model.HarnessPi)) && !strings.Contains(err.Error(), "working folder") {
						t.Fatalf("did not reach cwd admission: %v", err)
					}
					if r.a.SessionID != "keep-session" || r.a.State != model.StatePaused || f.res.paused[r.a.ID] == nil {
						t.Fatal("rejection changed agent state")
					}
					if e.chat.Last(r.a.ID) != nil || e.term.Last(r.a.ID) != nil {
						t.Fatal("runner started")
					}
					if len(f.projects) != 0 {
						t.Fatal("project persisted")
					}
				})
			}
		}
	}
}

// Agents always start (the owner, 17 Nov): a harness the guard has no adapter for is not refused.
// It starts with an error row in its timeline; Codex on Windows gets its parity shims instead.
func TestUnguardedHarnessStartsWithWarning(t *testing.T) {
	old := launchGOOS
	t.Cleanup(func() { launchGOOS = old })
	for _, goos := range []string{"windows", "linux", "darwin"} {
		for _, h := range []model.Harness{model.HarnessCodex, model.HarnessOpenCode, model.HarnessPi, "agy"} {
			t.Run(goos+"/"+string(h), func(t *testing.T) {
				launchGOOS = goos
				e := newEnv(t)
				f := e.fleet()
				f.mu.Lock()
				defer f.mu.Unlock()
				if err := f.launchAdmissionLocked(model.Agent{Harness: h, Level: model.LevelWorker, Cwd: t.TempDir()}); err != nil {
					t.Fatalf("launch refused: %v", err)
				}
				r := &agentRec{a: model.Agent{ID: "ag-" + string(h), Name: "w-" + string(h), Harness: h, Level: model.LevelWorker, Cwd: t.TempDir()}}
				f.agents[r.a.ID] = r
				var spec runner.Spec
				if err := f.guardSpecLocked(r, &spec); err != nil {
					t.Fatalf("guard setup refused the launch: %v", err)
				}
				rows, _ := f.st.Timeline(r.a.ID, 0, 10)
				if h == model.HarnessCodex && goos == "windows" {
					if spec.CodexGuardHome == "" || spec.CodexGuardExecutable == "" || len(rows) != 0 {
						t.Fatalf("Codex parity guard not prepared (or warned anyway): %+v, %d rows", spec, len(rows))
					}
					return
				}
				if spec.CodexGuardHome != "" || spec.SettingsPath != "" {
					t.Fatalf("a guard was installed for %s on %s", h, goos)
				}
				if len(rows) != 1 || rows[0].Kind != model.EntryError || !rows[0].IsError || rows[0].Meta["guard_warning"] == nil ||
					!strings.Contains(rows[0].Text, "runs without the command guard") {
					t.Fatalf("no guard warning in the timeline: %+v", rows)
				}
			})
		}
	}
}

// The same through the public launch path: the agent runs, its timeline carries the warning.
func TestUnguardedHarnessLaunches(t *testing.T) {
	e := newUnguardedEnv(t)
	f := e.started()
	proj := workerProject(t, f)
	w, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: proj, Name: "cx", Harness: model.HarnessCodex})
	if err != nil {
		t.Fatalf("codex launch refused: %v", err)
	}
	if e.chat.Last(w.ID) == nil && e.term.Last(w.ID) == nil {
		t.Fatal("no process was started")
	}
	if a := mustAgent(t, f, "cx"); a.State == model.StateError || a.Error != "" {
		t.Fatalf("started in error: %+v", a)
	}
	if n := guardWarnings(t, f, w.ID); n != 1 {
		t.Fatalf("%d guard warnings, want 1", n)
	}
	if text := lastGuardWarning(t, f, w.ID); !strings.Contains(text, "Codex") {
		t.Fatalf("warning: %q", text)
	}
}

func TestAutomaticRestorationAfterAdmissionChanges(t *testing.T) {
	e := newEnv(t)
	sus := &fakeSuspend{}
	f := e.fleetWith(nil, sus)
	w := launch(t, f, "restore", "")
	if _, err := f.BrakeAgent(w.ID, BrakeDisk, "disk pressure", true); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.agents[w.ID].a.Cwd = filepath.VolumeName(e.workspace) + string(filepath.Separator)
	f.mu.Unlock()
	if _, err := f.ResumeAgent(OwnerCaller, w.ID); KindOf(err) != KindForbidden {
		t.Fatalf("user resume bypassed admission: %v", err)
	}
	if calls := sus.got(); len(calls) != 1 || calls[0] != "stop" {
		t.Fatalf("rejected user resume changed process: %v", calls)
	}
	if !f.UnbrakeAgent(w.ID, BrakeDisk, "maximum safe pause expired") {
		t.Fatal("automatic safety recovery refused")
	}
	if calls := sus.got(); len(calls) != 2 || calls[1] != "cont" {
		t.Fatalf("automatic recovery did not restore process: %v", calls)
	}
	if a := mustAgent(t, f, w.ID); a.State == model.StatePaused || a.PausedReason != "" || a.PausedAt != 0 {
		t.Fatalf("stale pause bookkeeping after recovery: %+v", a)
	}
	if pending, _ := f.Approvals(model.ApprovalPending); len(pending) != 0 {
		t.Fatal("stale brake approval after recovery")
	}
}

// No setting, and no leftover of the removed master switch, decides whether an agent starts:
// on Windows every harness is admitted, guarded or not.
func TestWindowsLaunchNeedsNoMasterSwitch(t *testing.T) {
	old := launchGOOS
	launchGOOS = "windows"
	t.Cleanup(func() { launchGOOS = old })
	e := newEnv(t)
	off := false
	for name, g := range map[string]*guard.Settings{
		"fresh install":          nil,
		"defaults":               {},
		"legacy switch off":      {LegacyEnabled: &off},
		"legacy off for workers": {LegacyOffFor: []string{"worker"}, DefaultsVersion: 1},
	} {
		e.guard = g
		f := e.fleet()
		for _, h := range []model.Harness{model.HarnessClaude, model.HarnessCodex, model.HarnessOpenCode, model.HarnessPi} {
			a := model.Agent{Harness: h, Level: model.LevelWorker, Cwd: t.TempDir()}
			f.mu.Lock()
			err := f.launchAdmissionLocked(a)
			warn := f.guardWarningLocked(a)
			f.mu.Unlock()
			if err != nil {
				t.Fatalf("%s: harness %s refused: %v", name, h, err)
			}
			guarded := h == model.HarnessClaude || h == model.HarnessCodex
			if (warn == nil) != guarded {
				t.Fatalf("%s: harness %s guarded=%v, warning %+v", name, h, guarded, warn)
			}
		}
	}
}

// Codex on Windows is guarded only in the modes its parity shims cover; the others start too,
// each with a warning that says which mode it is.
func TestWindowsCodexUnprotectedModesWarn(t *testing.T) {
	old := launchGOOS
	launchGOOS = "windows"
	t.Cleanup(func() { launchGOOS = old })
	e := newEnv(t)
	f := e.fleet()
	check := func(a model.Agent, want string) {
		t.Helper()
		f.mu.Lock()
		defer f.mu.Unlock()
		if err := f.launchAdmissionLocked(a); err != nil {
			t.Fatalf("launch refused: harness %s profile %s full %v: %v", a.Harness, a.Profile, a.CodexFullAccess, err)
		}
		warn := f.guardWarningLocked(a)
		if warn == nil || warn.Reason != guardWarnHarness || !strings.Contains(warn.Text, want) {
			t.Fatalf("harness %s profile %s full %v: warning %+v, want %q", a.Harness, a.Profile, a.CodexFullAccess, warn, want)
		}
	}
	check(model.Agent{Harness: model.HarnessCodex, Level: model.LevelWorker, Cwd: t.TempDir(), CodexFullAccess: true}, "full-access")
	check(model.Agent{Harness: model.HarnessCodex, Level: model.LevelWorker, Cwd: t.TempDir(), Profile: "reviewer"}, "read-only")
	check(model.Agent{Harness: "agy", Level: model.LevelWorker, Cwd: t.TempDir()}, "agy")
	check(model.Agent{Harness: "antigravity", Level: model.LevelWorker, Cwd: t.TempDir()}, "antigravity")
	f.opts.Executable = ""
	check(model.Agent{Harness: model.HarnessCodex, Level: model.LevelWorker, Cwd: t.TempDir()}, "could not be set up")
}
