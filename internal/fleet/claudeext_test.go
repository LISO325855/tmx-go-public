package fleet

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/claudeext"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// fakeClaudeExt: Claude Code with the playwright plugin (server "playwright"), a user server
// "docs", a user server "desk" whose env holds a value, and the Python fleet's "tmx" server
// (which the fleet line never offers or copies).
func fakeClaudeExt(t *testing.T) *claudeext.Service {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".claude.json"),
		[]byte(`{"mcpServers": {"docs": {"type": "http", "url": "https://docs.example/mcp"},
			"arc-desktop": {"command": "arc-mcp", "args": ["--stdio"], "env": {"ARC_AGENT": "${TMXG_AGENT_NAME}"}},
			"desk": {"command": "desk-mcp", "args": ["--stdio"], "env": {"DESK_MODE": "guarded"}},
			"companion": {"command": "C:\\Python311\\python.exe", "args": ["-I", "C:\\tools\\companion-mcp\\run.py"],
				"env": {"COMPANION_MCP_AGENT": "${TMXG_AGENT_NAME}", "COMPANION_MCP_ID": "${TMXG_AGENT_ID}"}},
			"tmx": {"command": "python", "args": ["-m", "tmx.mcp"]},
			"tmxg": {"command": "evil"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	list := `[{"id": "playwright@official", "enabled": true, "installPath": "/p/pw",
		"mcpServers": {"playwright": {"command": "npx", "args": ["@playwright/mcp@latest"]}}}]`
	return claudeext.New(claudeext.Options{Home: home,
		Claude: func() (string, error) { return "/bin/claude", nil },
		Run:    func(context.Context, string, ...string) ([]byte, string, error) { return []byte(list), "", nil }})
}

func TestProfileAddsClaudeServers(t *testing.T) {
	e := newEnv(t)
	e.ext = fakeClaudeExt(t)
	f := e.started()
	if _, err := f.CreateProject(OwnerCaller, ProjectRequest{Name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	p, err := f.CreateProfile(OwnerCaller, model.Profile{Slug: "tester", Name: "Tester", Level: model.LevelWorker,
		Model: sonnet, MCPServers: []string{"plugin:playwright:playwright", "docs", "gone", "docs", "desk"}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.MCPServers, []string{"plugin:playwright:playwright", "docs", "gone", "desk"}) {
		t.Fatalf("profile: %+v", p)
	}
	a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "t1", Profile: "tester", ProjectID: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	spec := e.chat.Last(a.ID).Spec
	b, _ := os.ReadFile(spec.MCPConfigPath)
	var cfg struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	pw, docs, desk := cfg.MCPServers["playwright"], cfg.MCPServers["docs"], cfg.MCPServers["desk"]
	if pw["command"] != "npx" || docs["url"] != "https://docs.example/mcp" || desk["command"] != "desk-mcp" || len(cfg.MCPServers) != 4 {
		t.Fatalf("mcp.json: %s", b)
	}
	// tmx's own server always stays, as tmx wrote it; the Python fleet's "tmx" never comes along.
	own := cfg.MCPServers["tmxg"]
	if own == nil || own["command"] != "/opt/tmx/tmx" || cfg.MCPServers["tmx"] != nil {
		t.Fatalf("tmxg entry: %s", b)
	}
	if env, _ := own["env"].(map[string]any); env[EnvAgent] != a.ID || env[EnvToken] == "" {
		t.Fatalf("tmxg env: %s", b)
	}
	if env, _ := desk["env"].(map[string]any); env["DESK_MODE"] != "guarded" {
		t.Fatalf("a user server's config is copied as Claude Code has it: %s", b)
	}
	// The profile itself stores ids only, never a server's config.
	stored, _ := json.Marshal(p)
	if strings.Contains(string(stored), "desk-mcp") || strings.Contains(string(stored), "guarded") {
		t.Fatalf("server config in the profile: %s", stored)
	}
	if env, _ := pw["env"].(map[string]any); env["CLAUDE_PLUGIN_ROOT"] != "/p/pw" {
		t.Fatalf("plugin root not set: %s", b)
	}
	tl, _ := f.Timeline(a.ID, 0, 50)
	found := false
	for _, en := range tl {
		found = found || (en.Kind == model.EntrySystem && strings.Contains(en.Text, "no MCP server gone"))
	}
	if !found {
		t.Fatalf("no note about the missing server: %+v", tl)
	}

	// A profile without them changes nothing.
	w, _ := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "w1", Level: model.LevelWorker, ProjectID: "alpha"})
	ws := e.chat.Last(w.ID).Spec
	b, _ = os.ReadFile(ws.MCPConfigPath)
	if strings.Contains(string(b), "playwright") || strings.Contains(string(b), "desk") || !strings.Contains(string(b), `"tmxg"`) {
		t.Fatalf("plain worker got extensions: %s", b)
	}
}

// A plain stdio server (command, args array, env map) reaches the agent as written, with the
// agent's own name and id filled in: each agent of the profile gets its own value.
func TestProfileServerFilledPerAgent(t *testing.T) {
	e := newEnv(t)
	e.ext = fakeClaudeExt(t)
	f := e.started()
	if _, err := f.CreateProject(OwnerCaller, ProjectRequest{Name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.CreateProfile(OwnerCaller, model.Profile{Slug: "helper", Name: "Helper", Level: model.LevelWorker,
		Model: sonnet, MCPServers: []string{"companion"}}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"h1", "h2"} {
		a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: name, Profile: "helper", ProjectID: "alpha"})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(e.chat.Last(a.ID).Spec.MCPConfigPath)
		var cfg struct {
			MCPServers map[string]struct {
				Command string            `json:"command"`
				Args    []string          `json:"args"`
				Env     map[string]string `json:"env"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal(b, &cfg); err != nil {
			t.Fatal(err)
		}
		c := cfg.MCPServers["companion"]
		if c.Command != `C:\Python311\python.exe` || !slices.Equal(c.Args, []string{"-I", `C:\tools\companion-mcp\run.py`}) {
			t.Fatalf("%s: command/args: %s", name, b)
		}
		if len(c.Env) != 2 || c.Env["COMPANION_MCP_AGENT"] != name || c.Env["COMPANION_MCP_ID"] != a.ID {
			t.Fatalf("%s: env: %s", name, b)
		}
		if cfg.MCPServers["tmxg"].Command != "/opt/tmx/tmx" || len(cfg.MCPServers) != 2 {
			t.Fatalf("%s: mcp.json: %s", name, b)
		}
	}
}

// The fill is JSON-safe: a value with a quote or backslash stays one string.
func TestFilledForEscapes(t *testing.T) {
	ext := extServers{ids: []string{"s"}, configs: map[string]json.RawMessage{
		"s": json.RawMessage(`{"command": "x", "env": {"A": "${TMXG_AGENT_NAME}", "B": "id=${TMXG_AGENT_ID}"}}`)}}
	out := ext.filledFor(model.Agent{ID: `a"1`, Name: `we\ird "name"`})
	var c struct {
		Command string            `json:"command"`
		Env     map[string]string `json:"env"`
	}
	if err := json.Unmarshal(out.configs["s"], &c); err != nil {
		t.Fatalf("not JSON any more: %s (%v)", out.configs["s"], err)
	}
	if c.Command != "x" || c.Env["A"] != `we\ird "name"` || c.Env["B"] != `id=a"1` || len(c.Env) != 2 {
		t.Fatalf("filled: %+v", c)
	}
	if strings.Contains(string(ext.configs["s"]), "ird") {
		t.Fatal("the looked-up configs were changed in place")
	}
}

// Without the service (Options.ClaudeExt nil) the agent starts with tmxg alone and says so.
func TestProfileServersWithoutClaudeCode(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	if _, err := f.CreateProject(OwnerCaller, ProjectRequest{Name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.CreateProfile(OwnerCaller, model.Profile{Slug: "tester", Name: "Tester", Level: model.LevelWorker,
		Model: sonnet, MCPServers: []string{"docs"}}); err != nil {
		t.Fatal(err)
	}
	a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "t1", Profile: "tester", ProjectID: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(e.chat.Last(a.ID).Spec.MCPConfigPath)
	var cfg struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil || len(cfg.MCPServers) != 1 || cfg.MCPServers["tmxg"] == nil {
		t.Fatalf("mcp.json: %s (%v)", b, err)
	}
	tl, _ := f.Timeline(a.ID, 0, 50)
	if !slices.ContainsFunc(tl, func(en model.TimelineEntry) bool {
		return en.Kind == model.EntrySystem && strings.Contains(en.Text, "no MCP server docs")
	}) {
		t.Fatalf("no note about the missing server: %+v", tl)
	}
}

// A plugin's server never takes tmx's own name, and a user server of the same name as a
// plugin's keeps it.
func TestWriteMCPConfigKeys(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	raw := func(cmd string) json.RawMessage { return json.RawMessage(`{"command": "` + cmd + `"}`) }
	ext := extServers{
		ids: []string{"plugin:pw:browser", "browser", "plugin:x:tmxg", "plugin:y:tmx", "tmxg"},
		configs: map[string]json.RawMessage{"plugin:pw:browser": raw("a"), "browser": raw("b"),
			"plugin:x:tmxg": raw("c"), "plugin:y:tmx": raw("d"), "tmxg": raw("e")},
	}
	path, err := f.writeMCPConfig("agent-1", []string{EnvAgent + "=agent-1"}, ext)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var cfg struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	for k, v := range cfg.MCPServers {
		got[k] = v["command"]
	}
	want := map[string]any{"tmxg": "/opt/tmx/tmx", "pw-browser": "a", "browser": "b", "x-tmxg": "c", "y-tmx": "d"}
	if len(got) != len(want) {
		t.Fatalf("mcp.json: %s", b)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %v, want %v: %s", k, got[k], v, b)
		}
	}
}

// Codex agents use the same profile inventory as Claude.
func TestProfileServersIncludeCodex(t *testing.T) {
	e := newEnv(t)
	e.ext = fakeClaudeExt(t)
	f := e.started()
	f.mu.Lock()
	f.agents["cx"] = &agentRec{a: model.Agent{ID: "cx", Name: "cx", Level: model.LevelWorker, Harness: model.HarnessCodex, Profile: "worker"}}
	f.profiles["worker"].MCPServers = []string{"docs"}
	f.mu.Unlock()
	if ext := f.profileServers("cx"); len(ext.ids) != 1 || len(ext.configs) != 1 || len(ext.missing) != 0 {
		t.Fatalf("codex agent did not get profile servers: %+v", ext)
	}
	f.mu.Lock()
	delete(f.agents, "cx")
	f.mu.Unlock()
}

func TestProfileRejectsBadServers(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	for _, ids := range [][]string{{"tmx"}, {"tmxg"}, {claudeext.ChromeServer}, {"has space"}, {"plugin:x"}} {
		_, err := f.CreateProfile(OwnerCaller, model.Profile{Slug: "bad", Name: "Bad", Level: model.LevelWorker,
			Model: sonnet, MCPServers: ids})
		if KindOf(err) != KindInvalid {
			t.Errorf("%v: err %v", ids, err)
		}
	}
	many := make([]string, 0, maxMCPServers+1)
	for i := 0; i <= maxMCPServers; i++ {
		many = append(many, "s"+strings.Repeat("x", i))
	}
	if _, err := f.CreateProfile(OwnerCaller, model.Profile{Slug: "bad", Name: "Bad", Level: model.LevelWorker,
		Model: sonnet, MCPServers: many}); KindOf(err) != KindInvalid {
		t.Errorf("%d servers: err %v", len(many), err)
	}
}

// The head gives a profile only servers the owner installed: an installed one is taken, a name
// that is not installed or one of tmx's own is refused, and leaving the field out keeps what
// the profile has. A lead or a worker changes no profile at all.
func TestHeadSetsOnlyInstalledServers(t *testing.T) {
	e := newEnv(t)
	e.ext = fakeClaudeExt(t)
	f := e.started()
	head := AgentCaller(f.HeadID())
	p, err := f.CreateProfile(head, model.Profile{Slug: "docsy", Name: "Docsy", Level: model.LevelWorker,
		Model: sonnet, MCPServers: []string{"docs", "plugin:playwright:playwright"}})
	if err != nil || len(p.MCPServers) != 2 {
		t.Fatalf("the head creating a profile with installed servers: %v %v", p.MCPServers, err)
	}
	for _, ids := range [][]string{{"gone"}, {"docs", "gone"}, {"tmx"}, {"tmxg"}, {claudeext.ChromeServer}} {
		if _, err := f.CreateProfile(head, model.Profile{Slug: "bad", Name: "Bad", Level: model.LevelWorker,
			Model: sonnet, MCPServers: ids}); KindOf(err) != KindForbidden {
			t.Errorf("the head creating a profile with %v: %v", ids, err)
		}
	}
	w, err := f.GetProfile("docsy")
	if err != nil {
		t.Fatal(err)
	}
	w.MCPServers = []string{"desk"}
	if got, err := f.UpdateProfile(head, "docsy", w); err != nil || len(got.MCPServers) != 1 || got.MCPServers[0] != "desk" {
		t.Fatalf("the head changing to another installed server: %v %v", got.MCPServers, err)
	}
	w.MCPServers = []string{"desk", "tmx"}
	if _, err := f.UpdateProfile(head, "docsy", w); KindOf(err) != KindForbidden {
		t.Fatalf("the head adding the Python fleet's server: %v", err)
	}
	w.MCPServers, w.Description = nil, "left out"
	if got, err := f.UpdateProfile(head, "docsy", w); err != nil || len(got.MCPServers) != 1 || got.MCPServers[0] != "desk" {
		t.Fatalf("leaving the field out: %v %v", got.MCPServers, err)
	}
	if _, err := f.CreateProject(OwnerCaller, ProjectRequest{Name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	lead := AgentCaller(mustAgent(t, f, "alpha-lead").ID)
	w.MCPServers = []string{"docs"}
	if _, err := f.UpdateProfile(lead, "docsy", w); KindOf(err) != KindForbidden {
		t.Fatalf("a lead changed a profile: %v", err)
	}
}

// Without any installed server (no Claude Code service here) the head can name none.
func TestProfileServersOwnerOnly(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	head := AgentCaller(f.HeadID())
	if _, err := f.CreateProfile(head, model.Profile{Slug: "mine", Name: "Mine", Level: model.LevelWorker,
		Model: sonnet, MCPServers: []string{"docs"}}); KindOf(err) != KindForbidden {
		t.Fatalf("agent created a profile: %v", err)
	}
	w, err := f.GetProfile("worker")
	if err != nil {
		t.Fatal(err)
	}
	w.MCPServers = []string{"docs"}
	if _, err := f.UpdateProfile(head, "worker", w); KindOf(err) != KindForbidden {
		t.Fatalf("agent changed a profile: %v", err)
	}
	if w, _ := f.GetProfile("worker"); len(w.MCPServers) != 0 {
		t.Fatalf("worker profile changed: %v", w.MCPServers)
	}
}
