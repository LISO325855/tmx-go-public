package claudeext

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tmx's own servers are kept out by what they start, not only by their name.
func TestReservedConfig(t *testing.T) {
	for cfg, want := range map[string]bool{
		`{"command":"tmxg","args":["mcp"]}`:                                                                true,
		`{"command":"C:\\Users\\a\\AppData\\Local\\TiamatrixGo\\bin\\tmxg.exe","args":["mcp"]}`:            true,
		`{"command":"/usr/local/bin/tmx","args":["mcp"]}`:                                                  true,
		`{"command":"TMX.EXE","args":["mcp"]}`:                                                             true,
		`{"command":"python","args":["-m","tmx","mcp"]}`:                                                   true,
		`{"command":"python","args":["-m","tmx.mcp_server"]}`:                                              true,
		`{"command":"C:\\Program Files\\Python312\\python.exe","args":["C:\\src\\tmx\\src\\tmx\\mcp.py"]}`: true,
		`{"command":"cmd","args":["/c","tmxg.cmd","mcp"]}`:                                                 true,
		`{"command":"npx","args":["-y","@acme/docs-mcp"]}`:                                                 false,
		`{"command":"python","args":["-m","tmxtools"]}`:                                                    false,
		`{"command":"node","args":["C:\\tools\\tmx-notes\\server.js"]}`:                                    false,
		`{"type":"http","url":"https://example.com/tmx/mcp"}`:                                              false,
		`{"command":"python","args":["-m","http.server"]}`:                                                 false,
		`not json`: false,
	} {
		if got := ReservedConfig(json.RawMessage(cfg)); got != want {
			t.Errorf("%s: reserved %v, want %v", cfg, got, want)
		}
	}
}

// Adding tmx's own server under another name is refused before anything is installed.
func TestAddServerRefusesTmxUnderAnotherName(t *testing.T) {
	var s Service // no claude is started: the refusal comes first
	err := s.AddServer(context.Background(), "helper", json.RawMessage(`{"command":"python","args":["-m","tmx","mcp"]}`))
	if err == nil || !strings.Contains(err.Error(), "starts tmx itself") {
		t.Fatalf("tmx under another name: %v", err)
	}
}

// A plugin's server whose config only becomes tmx's own once ${CLAUDE_PLUGIN_ROOT} is filled in
// (the plugin is installed in a tmx folder) is kept out too: the check is made on the resolved
// config (review of build6, 18 Nov).
func TestPluginServerResolvingIntoTmxIsReserved(t *testing.T) {
	home := t.TempDir()
	tmxRoot := filepath.Join(home, "src", "tmx")
	list, _ := json.Marshal([]map[string]any{
		{"id": "helper@local", "enabled": true, "installPath": tmxRoot,
			"mcpServers": map[string]any{"helper": map[string]any{"command": "python", "args": []string{"${CLAUDE_PLUGIN_ROOT}/mcp.py"}}}},
		{"id": "notes@local", "enabled": true, "installPath": filepath.Join(home, "plugins", "notes"),
			"mcpServers": map[string]any{"notes": map[string]any{"command": "python", "args": []string{"${CLAUDE_PLUGIN_ROOT}/mcp.py"}}}},
	})
	s := New(Options{Home: home,
		Claude: func() (string, error) { return "/bin/claude", nil },
		Run:    func(context.Context, string, ...string) ([]byte, string, error) { return list, "", nil }})
	inv := s.Inventory(context.Background(), true)
	if _, ok := inv.Config("plugin:helper:helper"); ok {
		t.Error("a plugin server that resolves into the tmx tree is offered")
	}
	if _, ok := inv.Config("plugin:notes:notes"); !ok {
		t.Errorf("an ordinary plugin server is missing: %+v", inv.Servers)
	}
}

// The OS user's own ~/.claude.json is read besides the account home's (CLAUDE_CONFIG_DIR): the
// owner's servers registered in his own Claude Code are offered; where both name a server the
// account home's config stays; tmx's own are kept out of both.
func TestUsersOwnClaudeFileIsReadToo(t *testing.T) {
	account, own := t.TempDir(), t.TempDir()
	write := func(dir, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(account, `{"mcpServers": {"docs": {"type": "http", "url": "https://account.example/mcp"}}}`)
	write(own, `{"mcpServers": {"companion": {"command": "companion-mcp"}, "arc-desktop": {"command": "arc-desktop.exe", "args": ["mcp"]},
		"docs": {"type": "http", "url": "https://own.example/mcp"}, "tmx": {"command": "python", "args": ["-m", "tmx.mcp"]}}}`)
	s := New(Options{Env: []string{"CLAUDE_CONFIG_DIR=" + account}, UserHome: own,
		Claude: func() (string, error) { return "/bin/claude", nil },
		Run:    func(context.Context, string, ...string) ([]byte, string, error) { return []byte("[]"), "", nil }})
	inv := s.Inventory(context.Background(), true)
	for _, id := range []string{"companion", "arc-desktop", "docs"} {
		if _, ok := inv.Config(id); !ok {
			t.Errorf("%s is not offered: %+v", id, inv.Servers)
		}
	}
	if c, _ := inv.Config("docs"); !strings.Contains(string(c), "account.example") {
		t.Errorf("docs: %s, want the account home's entry", c)
	}
	if _, ok := inv.Config("tmx"); ok {
		t.Error("the Python fleet's server is offered")
	}
	n := 0
	for _, sv := range inv.Servers {
		if sv.ID == "docs" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("docs is listed %d times", n)
	}
}
