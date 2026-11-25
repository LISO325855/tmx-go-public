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

func TestEveryHarnessProfileMCP(t *testing.T) {
	for _, h := range []model.Harness{model.HarnessClaude, model.HarnessCodex, model.HarnessOpenCode, model.HarnessGemini, model.HarnessPi, "agy", "antigravity", model.HarnessShell} {
		t.Run(string(h), func(t *testing.T) {
			e := newUnguardedEnv(t)
			e.ext = fakeClaudeExt(t)
			source := t.TempDir()
			for _, key := range []string{"HOME", "USERPROFILE", "CODEX_HOME", "PI_CODING_AGENT_DIR"} {
				t.Setenv(key, source)
			}
			t.Setenv("GEMINI_CLI_SYSTEM_SETTINGS_PATH", filepath.Join(source, "system.json"))
			if err := os.WriteFile(filepath.Join(source, "auth.json"), []byte(`{}`), 0600); err != nil {
				t.Fatal(err)
			}
			f := e.started()
			if _, err := f.CreateProject(OwnerCaller, ProjectRequest{Name: "alpha"}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.CreateProfile(OwnerCaller, model.Profile{Slug: "tools", Name: "Tools", Level: model.LevelWorker, Model: sonnet, MCPServers: []string{"arc-desktop", "gone"}}); err != nil {
				t.Fatal(err)
			}
			a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "tool-agent", Harness: h, Mode: model.ModeTerminal, Profile: "tools", ProjectID: "alpha"})
			if err != nil {
				t.Fatal(err)
			}
			spec := e.term.Last(a.ID).Spec
			if h != model.HarnessShell {
				var b []byte
				switch h {
				case model.HarnessCodex:
					b, err = os.ReadFile(filepath.Join(f.opts.Paths.AgentDir(a.ID), "codex-profile", "config.toml"))
				case model.HarnessOpenCode:
					spec.Command, _ = os.Executable()
					cmd, cmdErr := harness.TerminalCommand(spec)
					if cmdErr != nil {
						t.Fatal(cmdErr)
					}
					for _, kv := range cmd.Env {
						if raw, ok := strings.CutPrefix(kv, "OPENCODE_CONFIG_CONTENT="); ok {
							b = []byte(raw)
						}
					}
				default:
					b, err = os.ReadFile(spec.MCPConfigPath)
				}
				if err != nil {
					t.Fatal(err)
				}
				for _, want := range []string{"arc-desktop", "arc-mcp", "--stdio", "ARC_AGENT", "tool-agent", "tmxg"} {
					if !strings.Contains(string(b), want) {
						t.Errorf("%s config missing %s", h, want)
					}
				}
				if h == model.HarnessGemini && launchEnv(&spec, "GEMINI_CLI_SYSTEM_SETTINGS_PATH") != spec.MCPConfigPath {
					t.Fatal("Gemini not pointed at generated settings")
				}
				if h == model.HarnessPi && filepath.Join(launchEnv(&spec, "PI_CODING_AGENT_DIR"), "mcp.json") != spec.MCPConfigPath {
					t.Fatal("pi not pointed at generated settings")
				}
				if (h == "agy" || h == "antigravity") && filepath.Join(launchEnv(&spec, "HOME"), ".gemini", "config", "mcp_config.json") != spec.MCPConfigPath {
					t.Fatal("agy not pointed at generated settings")
				}
			}
			a, _ = f.Agent(a.ID)
			want := "gone not available for " + string(h) + " agents"
			if !slices.ContainsFunc(a.MCPWarnings, func(s string) bool { return strings.Contains(s, want) }) {
				t.Fatalf("missing visible agent warning: %+v", a.MCPWarnings)
			}
			if h == model.HarnessShell && !slices.ContainsFunc(a.MCPWarnings, func(s string) bool { return strings.Contains(s, "arc-desktop not available for shell agents") }) {
				t.Fatal("shell silently lost profile tools")
			}
			p, _ := f.GetProfile("tools")
			if !slices.ContainsFunc(p.MCPWarnings, func(s string) bool { return strings.Contains(s, want) }) {
				t.Fatalf("missing profile warning: %+v", p.MCPWarnings)
			}
			encoded, _ := json.Marshal(a)
			if !strings.Contains(string(encoded), `"mcp_warnings"`) {
				t.Fatal("agent API omitted warnings")
			}
		})
	}
}

func TestProfileMCPWarningsClearOnRestart(t *testing.T) {
	e := newUnguardedEnv(t)
	e.ext = fakeClaudeExt(t)
	f := e.started()
	if _, err := f.CreateProject(OwnerCaller, ProjectRequest{Name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	p, err := f.CreateProfile(OwnerCaller, model.Profile{Slug: "tools", Name: "Tools", Level: model.LevelWorker, Model: sonnet, MCPServers: []string{"gone", "docs"}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "tool-agent", Harness: model.HarnessOpenCode, Profile: "tools", ProjectID: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	a, _ = f.Agent(a.ID)
	if len(a.MCPWarnings) != 2 {
		t.Fatalf("missing skipped/missing warnings: %+v", a.MCPWarnings)
	}
	p.MCPServers = []string{"arc-desktop"}
	if _, err := f.UpdateProfile(OwnerCaller, p.Slug, p); err != nil {
		t.Fatal(err)
	}
	if _, err := f.RestartAgent(OwnerCaller, a.ID); err != nil {
		t.Fatal(err)
	}
	a, _ = f.Agent(a.ID)
	p, _ = f.GetProfile(p.Slug)
	if len(a.MCPWarnings) != 0 || len(p.MCPWarnings) != 0 {
		t.Fatal("stale MCP warnings after successful restart")
	}
}
