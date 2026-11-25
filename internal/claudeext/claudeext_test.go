package claudeext

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const pluginList = `[
  {"id": "playwright@official", "version": "1", "scope": "user", "enabled": true, "installPath": "/p/pw",
   "mcpServers": {"playwright": {"command": "npx", "args": ["@playwright/mcp@latest"]}}},
  {"id": "local@mine", "enabled": false, "installPath": "/p/local",
   "mcpServers": {"srv": {"command": "${CLAUDE_PLUGIN_ROOT}/bin/srv", "env": {"K": "secret"}}}},
  {"id": "skills-only@official", "enabled": true}
]`

func newTest(t *testing.T, claudeJSON string, run func(args []string) ([]byte, string, error)) (*Service, *[][]string) {
	t.Helper()
	home := t.TempDir()
	if claudeJSON != "" {
		if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(claudeJSON), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var calls [][]string
	s := New(Options{Home: home, Claude: func() (string, error) { return "/bin/claude", nil },
		Run: func(_ context.Context, _ string, args ...string) ([]byte, string, error) {
			calls = append(calls, args)
			return run(args)
		}})
	return s, &calls
}

func TestInventory(t *testing.T) {
	s, _ := newTest(t, `{"oauthAccount": {"x": 1}, "mcpServers": {"docs": {"type": "http", "url": "https://docs.example/mcp", "headers": {"Authorization": "Bearer t"}}}}`,
		func([]string) ([]byte, string, error) { return []byte(pluginList), "", nil })
	inv := s.Inventory(context.Background(), false)
	if !inv.Available || inv.Error != "" {
		t.Fatalf("inventory: %+v", inv)
	}
	var ids []string
	for _, sv := range inv.Servers {
		ids = append(ids, sv.ID)
	}
	if !slices.Equal(ids, []string{"docs", "plugin:local:srv", "plugin:playwright:playwright"}) {
		t.Fatalf("servers %v", ids)
	}
	if inv.Servers[0].Detail != "docs.example" || inv.Servers[2].Detail != "npx @playwright/mcp@latest" || inv.Servers[1].Enabled {
		t.Fatalf("details: %+v", inv.Servers)
	}
	if len(inv.Plugins) != 3 || inv.Plugins[2].ID != "skills-only@official" || len(inv.Plugins[2].Servers) != 0 {
		t.Fatalf("plugins: %+v", inv.Plugins)
	}
	b, _ := json.Marshal(inv)
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "Bearer") {
		t.Fatalf("config values leak into the inventory: %s", b)
	}
	c, ok := inv.Config("plugin:local:srv")
	var m map[string]any
	json.Unmarshal(c, &m)
	env, _ := m["env"].(map[string]any)
	if !ok || m["command"] != "/p/local/bin/srv" || env["CLAUDE_PLUGIN_ROOT"] != "/p/local" || env["K"] != "secret" {
		t.Fatalf("plugin config: %s", c)
	}
	if c, _ := inv.Config("docs"); !strings.Contains(string(c), "Bearer t") {
		t.Fatalf("user config: %s", c)
	}
}

func TestInventoryCachesAndPartialFailure(t *testing.T) {
	var n atomic.Int32
	s, _ := newTest(t, "", func([]string) ([]byte, string, error) {
		n.Add(1)
		return nil, "boom\nmore", errors.New("exit 1")
	})
	inv := s.Inventory(context.Background(), false)
	if inv.Available != true || !strings.Contains(inv.Error, "boom") || strings.Contains(inv.Error, "more") {
		t.Fatalf("partial: %+v", inv) // no ~/.claude.json is fine, so still available
	}
	s.Inventory(context.Background(), false)
	if n.Load() != 1 {
		t.Fatalf("not cached: %d runs", n.Load())
	}
	s.Inventory(context.Background(), true)
	if n.Load() != 2 {
		t.Fatalf("fresh did not ask again: %d", n.Load())
	}

	none := New(Options{Claude: func() (string, error) { return "", errors.New("not found") }, TTL: time.Minute})
	if inv := none.Inventory(context.Background(), false); inv.Available || !strings.Contains(inv.Error, "not installed") {
		t.Fatalf("no claude: %+v", inv)
	}
}

func TestInstall(t *testing.T) {
	s, calls := newTest(t, "", func([]string) ([]byte, string, error) { return []byte("[]"), "", nil })
	ctx := context.Background()
	if err := s.InstallPlugin(ctx, "playwright@claude-plugins-official"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddServer(ctx, "docs", json.RawMessage(`{"type": "http", "url": "https://x/mcp"}`)); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"plugin", "install", "playwright@claude-plugins-official", "--scope", "user", "--json"},
		{"mcp", "add-json", "-s", "user", "docs", `{"type":"http","url":"https://x/mcp"}`},
	}
	if len(*calls) != 2 || !slices.Equal((*calls)[0], want[0]) || !slices.Equal((*calls)[1], want[1]) {
		t.Fatalf("calls %q", *calls)
	}
	for _, bad := range []string{"", "-rf", "a b", "x;y"} {
		if s.InstallPlugin(ctx, bad) == nil {
			t.Errorf("installed %q", bad)
		}
	}
	for _, bad := range []string{"tmx", "tmxg", ChromeServer, "-x", "a b"} {
		if s.AddServer(ctx, bad, json.RawMessage(`{}`)) == nil {
			t.Errorf("added %q", bad)
		}
	}
	if s.AddServer(ctx, "ok", json.RawMessage(`[1]`)) == nil {
		t.Error("added a non-object config")
	}

	fail, _ := newTest(t, "", func([]string) ([]byte, string, error) { return nil, "needs confirmation\n...", errors.New("exit 1") })
	if err := fail.InstallPlugin(ctx, "x@y"); err == nil || !strings.Contains(err.Error(), "needs confirmation") {
		t.Fatalf("error: %v", err)
	}
}

// tmx's own add refuses a config that writes a credential out; a named variable passes, and so
// does a plain setting.
func TestAddServerRefusesWrittenSecrets(t *testing.T) {
	s, calls := newTest(t, "", func([]string) ([]byte, string, error) { return []byte("[]"), "", nil })
	ctx := context.Background()
	for _, bad := range []string{
		`{"command": "x", "env": {"ARC_API_KEY": "sk-123"}}`,
		`{"command": "x", "env": {"MODE": "guarded", "github_token": "ghp_x"}}`,
		`{"type": "http", "url": "https://d/mcp", "headers": {"Authorization": "Bearer abc"}}`,
		`{"command": "x", "env": {"DB_PASSWORD": "hunter2"}}`,
	} {
		err := s.AddServer(ctx, "srv", json.RawMessage(bad))
		if err == nil || !strings.Contains(err.Error(), "writes out") {
			t.Errorf("%s: %v", bad, err)
		}
		if err != nil && (strings.Contains(err.Error(), "sk-123") || strings.Contains(err.Error(), "ghp_x") || strings.Contains(err.Error(), "abc") || strings.Contains(err.Error(), "hunter2")) {
			t.Errorf("the error repeats the value: %v", err)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("claude ran for a refused config: %q", *calls)
	}
	for _, ok := range []string{
		`{"command": "arc-desktop-mcp", "args": ["--stdio"], "env": {"ARC_MODE": "guarded"}}`,
		`{"command": "x", "env": {"ARC_API_KEY": "${ARC_API_KEY}"}}`,
		`{"type": "http", "url": "https://d/mcp", "headers": {"Authorization": "Bearer ${DOCS_TOKEN}"}}`,
		`{"command": "x", "env": {"TOKEN_FILE": ""}}`,
	} {
		if err := s.AddServer(ctx, "srv", json.RawMessage(ok)); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	if len(*calls) != 4 {
		t.Fatalf("calls %q", *calls)
	}
}

// The fleet line: tmx's own server and the Python fleet's are never offered, whoever lists them.
func TestInventorySkipsReserved(t *testing.T) {
	list := `[{"id": "p@m", "enabled": true, "mcpServers": {"tmxg": {"command": "x"}, "tmx": {"command": "y"}, "ok": {"command": "z"}}}]`
	s, _ := newTest(t, `{"mcpServers": {"tmx": {"command": "python"}, "tmxg": {"command": "evil"}, "claude-in-chrome": {"command": "c"}, "desk": {"command": "desk-mcp"}}}`,
		func([]string) ([]byte, string, error) { return []byte(list), "", nil })
	inv := s.Inventory(context.Background(), false)
	var ids []string
	for _, sv := range inv.Servers {
		ids = append(ids, sv.ID)
	}
	if !slices.Equal(ids, []string{"desk", "plugin:p:ok"}) {
		t.Fatalf("servers %v", ids)
	}
	for _, id := range []string{"tmx", "tmxg", ChromeServer, "plugin:p:tmxg", "plugin:p:tmx"} {
		if _, ok := inv.Config(id); ok {
			t.Errorf("config for %q", id)
		}
	}
	if !slices.Equal(inv.Plugins[0].Servers, []string{"plugin:p:ok"}) {
		t.Fatalf("plugin servers %v", inv.Plugins[0].Servers)
	}
}

// With CLAUDE_CONFIG_DIR, claude keeps its user servers in that folder's .claude.json.
func TestUserConfigFollowsConfigDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(`{"mcpServers": {"desk": {"command": "desk-mcp"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(Options{UserHome: t.TempDir(), Env: []string{"PATH=/bin", "CLAUDE_CONFIG_DIR=" + dir}, Claude: func() (string, error) { return "/bin/claude", nil },
		Run: func(context.Context, string, ...string) ([]byte, string, error) { return []byte("[]"), "", nil }})
	inv := s.Inventory(context.Background(), false)
	if len(inv.Servers) != 1 || inv.Servers[0].ID != "desk" {
		t.Fatalf("servers: %+v", inv.Servers)
	}
}

func TestValidServerID(t *testing.T) {
	for id, ok := range map[string]bool{"docs": true, "plugin:playwright:playwright": true, "plugin:x": false,
		"plugin::y": false, "a b": false, "": false, "-x": false} {
		if ValidServerID(id) != ok {
			t.Errorf("%q: %v", id, !ok)
		}
	}
}
