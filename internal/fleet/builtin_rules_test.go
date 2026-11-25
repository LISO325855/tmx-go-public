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
	"github.com/blackcode-ch/tmx-go/internal/rules"
	"github.com/blackcode-ch/tmx-go/internal/rules/rulestest"
	"github.com/blackcode-ch/tmx-go/internal/runner/claude"
)

func TestBuiltInRulesWithoutFleetRulesSource(t *testing.T) {
	for _, h := range []model.Harness{model.HarnessClaude, model.HarnessCodex, model.HarnessOpenCode, model.HarnessPi, model.HarnessGemini, "agy"} {
		t.Run(string(h), func(t *testing.T) {
			e := newUnguardedEnv(t)
			f := e.started()
			if f.Rules() != nil {
				t.Fatal("test requires a node without a fleet-rules source")
			}
			a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{
				ProjectID: workerProject(t, f), Name: "rules-worker", Profile: "worker", Harness: h,
			})
			if err != nil {
				t.Fatal(err)
			}
			proc := e.chat.Last(a.ID)
			if a.Mode == model.ModeTerminal {
				proc = e.term.Last(a.ID)
			}
			spec := proc.Spec
			check := func(where, text string) {
				t.Helper()
				for _, heading := range []string{
					"Working code is the only rule: first it works, then you make it safe.",
					"Calibration for work at blackcode: caution LOW, performance and reaching the goal HIGH.",
				} {
					if !strings.Contains(text, heading) {
						t.Errorf("%s lacks %q", where, heading)
					}
				}
			}
			check("built-in profile spec", spec.SystemPrompt)
			for _, body := range []string{worksFirstRules, calibrationRules} {
				if body == "" || !strings.Contains(spec.SystemPrompt, body) {
					t.Fatal("spec must include both complete embedded texts verbatim")
				}
			}
			switch h {
			case model.HarnessClaude:
				check("Claude arguments", strings.Join(claude.TerminalArgs(spec), "\n"))
			case model.HarnessCodex:
				check("Codex developer instructions", strings.Join(harness.CodexArgs(spec, ""), "\n"))
			default:
				f.mu.Lock()
				instructions := f.instructionsLocked(f.agents[a.ID])
				f.mu.Unlock()
				check("harness instructions", instructions)
			}
		})
	}
}

func TestOversizedInstructionsReachHarnessesWithoutLosingProfile(t *testing.T) {
	for _, h := range []model.Harness{model.HarnessClaude, model.HarnessCodex, model.HarnessOpenCode, model.HarnessPi, model.HarnessGemini, "agy"} {
		t.Run(string(h), func(t *testing.T) {
			e := newUnguardedEnv(t)
			f := e.started()
			profile, err := f.GetProfile("worker")
			if err != nil {
				t.Fatal(err)
			}
			profile.Instructions = strings.Repeat("whole profile\n", 2000) + "😀 LAST INSTRUCTION"
			if _, err := f.UpdateProfile(OwnerCaller, "worker", profile); err != nil {
				t.Fatal(err)
			}
			a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: workerProject(t, f), Name: "large", Profile: "worker", Harness: h})
			if err != nil {
				t.Fatal(err)
			}
			proc := e.chat.Last(a.ID)
			if a.Mode == model.ModeTerminal {
				proc = e.term.Last(a.ID)
			}
			spec := proc.Spec
			full, err := os.ReadFile(spec.SystemPromptFile)
			if err != nil {
				t.Fatal(err)
			}
			for _, body := range []string{profile.Instructions, worksFirstRules, calibrationRules} {
				if !strings.Contains(string(full), body) {
					t.Fatal("harness's full instructions file lost content")
				}
			}
			if rules.Units(spec.SystemPrompt) > PromptCap-promptMargin || !strings.Contains(spec.SystemPrompt, "read them first") {
				t.Fatal("expected bounded inline instructions with a file pointer")
			}
			// Command construction only: no real harness or model is launched.
			spec.Command, err = os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd, err := harness.TerminalCommand(spec)
			if err != nil {
				t.Fatal(err)
			}
			switch h {
			case model.HarnessClaude, model.HarnessPi:
				flag := "--append-system-prompt-file"
				if h == model.HarnessPi {
					flag = "--append-system-prompt"
				}
				i := slices.Index(cmd.Args, flag)
				if i < 0 || i+1 >= len(cmd.Args) || cmd.Args[i+1] != spec.SystemPromptFile {
					t.Fatal("native prompt-file argument missing")
				}
			case model.HarnessOpenCode:
				var cfg struct {
					Instructions []string `json:"instructions"`
				}
				for _, value := range cmd.Env {
					if strings.HasPrefix(value, "OPENCODE_CONFIG_CONTENT=") {
						if err := json.Unmarshal([]byte(strings.TrimPrefix(value, "OPENCODE_CONFIG_CONTENT=")), &cfg); err != nil {
							t.Fatal(err)
						}
					}
				}
				if !slices.Contains(cfg.Instructions, spec.SystemPromptFile) {
					t.Fatal("native OpenCode instructions file missing")
				}
			case model.HarnessCodex:
				if !strings.Contains(strings.Join(cmd.Args, "\n"), "read them first") {
					t.Fatal("Codex developer instructions lack the file pointer")
				}
			}
		})
	}
}

func TestBuiltInRulesPreservedWithNearCapProfile(t *testing.T) {
	for _, source := range []string{"none", "live"} {
		t.Run(source, func(t *testing.T) {
			var f *Fleet
			if source == "live" {
				re := newRulesEnv(t, rulestest.Files())
				re.refresh()
				re.f = re.started()
				f = re.f
			} else {
				f = newEnv(t).started()
			}
			profile, err := f.GetProfile("head")
			if err != nil {
				t.Fatal(err)
			}
			// Size the local profile just below the old cap, before the embedded
			// rules were added. The new rules must not displace its tail.
			profile.Instructions = ""
			if _, err := f.UpdateProfile(OwnerCaller, "head", profile); err != nil {
				t.Fatal(err)
			}
			baseline := promptOf(f, f.HeadID())
			oldUnits := rules.Units(baseline) - rules.Units(worksFirstRules) - rules.Units(calibrationRules) - 4
			profile.Instructions = strings.Repeat("p", PromptCap-promptMargin-oldUnits-128) + "😀 PROFILE TAIL"
			if _, err := f.UpdateProfile(OwnerCaller, "head", profile); err != nil {
				t.Fatal(err)
			}
			prompt := promptOf(f, f.HeadID())
			if got := rules.Units(prompt); got > PromptCap-promptMargin {
				t.Fatalf("prompt uses %d units, budget %d", got, PromptCap-promptMargin)
			}
			for _, body := range []string{worksFirstRules, calibrationRules} {
				if !strings.Contains(prompt, body) {
					t.Fatal("mandatory rules were truncated")
				}
			}
			path := filepath.Join(f.opts.Paths.UploadsDir(), f.HeadID(), instructionsFileName)
			full, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(prompt, "read them first") || !strings.Contains(prompt, filepath.Base(path)) {
				t.Fatal("inline instructions lack the full-file pointer")
			}
			for _, body := range []string{profile.Instructions, worksFirstRules, calibrationRules, mailRules, authorityRules} {
				if !strings.Contains(string(full), body) {
					t.Fatal("full instructions file lost content")
				}
			}
			if source == "live" {
				for _, body := range []string{"Report what you verified", "How the fleet runs day to day.", "## apps/tickets.md"} {
					if !strings.Contains(string(full), body) {
						t.Fatalf("full instructions lack live rules %q", body)
					}
				}
			}
		})
	}
}

func TestOversizedInstructionsWriteFailureIsNotSilentlyDropped(t *testing.T) {
	f := newEnv(t).started()
	profile, err := f.GetProfile("head")
	if err != nil {
		t.Fatal(err)
	}
	profile.Instructions = strings.Repeat("full profile\n", 2000) + "LAST INSTRUCTION"
	if _, err := f.UpdateProfile(OwnerCaller, "head", profile); err != nil {
		t.Fatal(err)
	}
	// An existing directory at the file path makes the atomic write fail.
	path := filepath.Join(f.opts.Paths.UploadsDir(), f.HeadID(), instructionsFileName)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	head := f.HeadID()
	f.mu.Lock()
	r := f.agents[head]
	text, file, writeErr := f.agentInstructionsLocked(r.a, "")
	_, specErr := f.specLocked(r, extServers{})
	f.mu.Unlock()
	if writeErr == nil || specErr == nil || file != "" || !strings.Contains(text, profile.Instructions) {
		t.Fatalf("failed write must preserve text and fail launch: file=%q write=%v spec=%v", file, writeErr, specErr)
	}
}
