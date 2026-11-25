package api_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/blackcode-ch/tmx-go/internal/claudeext"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
)

func TestClaudeExtensionsAPI(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers": {"docs": {"url": "https://d.example/mcp"}}}`), 0o600)
	var calls [][]string
	ext := claudeext.New(claudeext.Options{Home: home, Claude: func() (string, error) { return "/bin/claude", nil },
		Run: func(_ context.Context, _ string, args ...string) ([]byte, string, error) {
			calls = append(calls, args)
			return []byte(`[{"id": "playwright@official", "enabled": true, "mcpServers": {"playwright": {"command": "npx"}}}]`), "", nil
		}})
	d := apitest.New(t, func(o *fleet.Options) { o.ClaudeExt = ext })
	owner := client{t: t, base: d.URL, token: d.OwnerToken}
	agent := client{t: t, base: d.URL, token: d.AgentToken(t, "head")}

	var inv claudeext.Inventory
	if s := owner.do("GET", "/api/claude/extensions", nil, &inv); s != 200 || !inv.Available || len(inv.Servers) != 2 ||
		inv.Servers[0].ID != "docs" || inv.Servers[1].ID != "plugin:playwright:playwright" {
		t.Fatalf("GET: %d %+v", s, inv)
	}
	if s := owner.do("POST", "/api/claude/plugins", map[string]string{"plugin": "firebase@official"}, &inv); s != 200 {
		t.Fatalf("install: %d", s)
	}
	if s := owner.do("POST", "/api/claude/mcp", map[string]any{"name": "x", "config": map[string]string{"url": "https://x/mcp"}}, nil); s != 200 {
		t.Fatalf("add: %d", s)
	}
	if !slices.ContainsFunc(calls, func(a []string) bool { return slices.Equal(a[:3], []string{"plugin", "install", "firebase@official"}) }) ||
		!slices.ContainsFunc(calls, func(a []string) bool { return slices.Equal(a[:2], []string{"mcp", "add-json"}) }) {
		t.Fatalf("calls %q", calls)
	}
	if s := owner.do("POST", "/api/claude/plugins", map[string]string{"plugin": "-rf"}, nil); s != http.StatusBadRequest {
		t.Fatalf("bad plugin: %d", s)
	}
	for _, r := range []struct{ m, p string }{{"GET", "/api/claude/extensions"}, {"POST", "/api/claude/plugins"}, {"POST", "/api/claude/mcp"}} {
		if s := agent.do(r.m, r.p, map[string]string{"plugin": "x"}, nil); s != http.StatusForbidden {
			t.Errorf("agent %s %s: %d", r.m, r.p, s)
		}
	}

	none := apitest.New(t)
	if s := (client{t: t, base: none.URL, token: none.OwnerToken}).do("GET", "/api/claude/extensions", nil, nil); s != http.StatusNotImplemented {
		t.Fatalf("without the service: %d", s)
	}
}
