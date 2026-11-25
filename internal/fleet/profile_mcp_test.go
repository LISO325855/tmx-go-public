package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

func TestCodexProfileMCPLaunchAndRestart(t *testing.T) {
	e := newUnguardedEnv(t)
	e.ext = fakeClaudeExt(t)
	source := t.TempDir()
	t.Setenv("CODEX_HOME", source)
	if err := os.WriteFile(filepath.Join(source, "auth.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	f := e.started()
	if _, err := f.CreateProject(OwnerCaller, ProjectRequest{Name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.CreateProfile(OwnerCaller, model.Profile{Slug: "tester", Name: "Tester", Level: model.LevelWorker, Model: sonnet, MCPServers: []string{"arc-desktop", "desk", "companion", "docs", "gone"}}); err != nil {
		t.Fatal(err)
	}
	a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "cx", Harness: model.HarnessCodex, Profile: "tester", ProjectID: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	check := func() {
		spec := e.chat.Last(a.ID).Spec
		if spec.MCP == nil || spec.MCP.Command != "/opt/tmx/tmx" {
			t.Fatal("tmxg missing")
		}
		if got := spec.ProfileMCP["desk"]; got.Command != "desk-mcp" || !slices.Equal(got.Args, []string{"--stdio"}) || got.Env["DESK_MODE"] != "guarded" {
			t.Fatalf("wrong server: %+v", got)
		}
		if got := spec.ProfileMCP["arc-desktop"]; got.Command != "arc-mcp" || !slices.Equal(got.Args, []string{"--stdio"}) || got.Env["ARC_AGENT"] != "cx" {
			t.Fatalf("arc-desktop not propagated: %+v", got)
		}
		if spec.ProfileMCP["companion"].Env["COMPANION_MCP_AGENT"] != "cx" {
			t.Fatal("agent placeholder not filled")
		}
		b, err := os.ReadFile(filepath.Join(f.opts.Paths.AgentDir(a.ID), "codex-profile", "config.toml"))
		if err != nil || !strings.Contains(string(b), `[mcp_servers."arc-desktop"]`) || !strings.Contains(string(b), `[mcp_servers."tmxg"]`) {
			t.Fatalf("generated config missing: %v", err)
		}
	}
	check()
	if err := os.WriteFile(filepath.Join(f.opts.Paths.AgentDir(a.ID), "codex-profile", "config.toml"), []byte("stale config"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.RestartAgent(OwnerCaller, a.ID); err != nil {
		t.Fatal(err)
	}
	check()
	tl, _ := f.Timeline(a.ID, 0, 100)
	for _, note := range []string{"no MCP server gone", "docs cannot be translated"} {
		if !slices.ContainsFunc(tl, func(en model.TimelineEntry) bool { return strings.Contains(en.Text, note) }) {
			t.Errorf("missing timeline note: %s", note)
		}
	}
}

func TestProfileMCPReservedAndPluginNames(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	raw := json.RawMessage(`{"command":"arc","args":["--stdio"]}`)
	ext := extServers{ids: []string{"arc-desktop", "plugin:p:arc-desktop", "tmxg", "tmx"}, configs: map[string]json.RawMessage{"arc-desktop": raw, "plugin:p:arc-desktop": raw, "tmxg": raw, "tmx": raw}}
	f.mu.Lock()
	defer f.mu.Unlock()
	got := f.profileStdioLocked(&agentRec{a: model.Agent{Harness: model.HarnessCodex}}, ext)
	if len(got) != 2 || got["arc-desktop"].Command != "arc" || got["p-arc-desktop"].Command != "arc" {
		t.Fatalf("wrong server selection: %+v", got)
	}
}
