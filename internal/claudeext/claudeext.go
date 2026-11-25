// Package claudeext reads and installs the owner's Claude Code extensions (MCP servers and
// plugins) so a profile can give its agents some of them (DESIGN.md "Profiles", Claude Code
// extensions). tmx keeps no catalog of its own: whatever Claude Code has installed is offered,
// and installing goes through Claude Code's own commands.
//
// Claude Code (verified with 2.1.310 on 2 Nov 2026):
//   - `claude plugin list --json` → [{id, version, scope, enabled, installPath, mcpServers?}]; a
//     plugin's mcpServers are its servers' configs as written in the plugin (they may use
//     ${CLAUDE_PLUGIN_ROOT}).
//   - user-scope MCP servers (`claude mcp add -s user`) are the top-level "mcpServers" of
//     ~/.claude.json; that file holds much more (tmx reads only that key).
//   - agents run with --strict-mcp-config, which drops plugin and user servers alike; a server
//     written into the agent's --mcp-config file runs. --chrome turns on Claude in Chrome
//     (server "claude-in-chrome"), also in -p mode and with --strict-mcp-config.
//   - `claude plugin install <plugin> --json` and `claude mcp add-json -s user <name> <json>`
//     install.
//
// Fleet line: tmx's own server is "tmxg", and the Python fleet's "tmx" server is usually in the
// owner's ~/.claude.json. Neither is ever offered or copied (Reserved): a Go agent must not get
// the Python fleet's tools through a profile. With CLAUDE_CONFIG_DIR set for the daemon, user
// servers are read from that folder's .claude.json, where the same claude writes them.
package claudeext

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/hiddenexec"
)

// PluginPrefix starts the id of a plugin's server: "plugin:<plugin name>:<server>", as Claude
// Code names them. A user server's id is its name.
const PluginPrefix = "plugin:"

// ChromeServer is the name Claude Code gives Claude in Chrome's server.
const ChromeServer = "claude-in-chrome"

// Reserved reports a server name no profile can add and tmx never installs: tmx's own server
// ("tmxg"), the Python fleet's ("tmx"), and Claude in Chrome's.
func Reserved(name string) bool { return name == "tmxg" || name == "tmx" || name == ChromeServer }

// ReservedConfig reports a server config that starts tmx itself, whatever name it was given:
// the Python fleet's server or this node's, added under another name, would hand an agent the
// tools Reserved keeps out (review of build5, 17 Nov). It is tmx's when the command, or an
// argument, is a path with a part named tmx or tmxg (the program, with or without an
// extension, or a folder: ...\tmx\src\tmx\mcp.py), or when the arguments run the module
// (`-m tmx`, `-m tmx.mcp`). A server that merely lives in a folder of that name is kept out
// too: the owner can still give agents such a server under tmx's own rules, not through a
// profile.
func ReservedConfig(config json.RawMessage) bool {
	var c struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	if json.Unmarshal(config, &c) != nil {
		return false
	}
	words := append([]string{c.Command}, c.Args...)
	for i, w := range words {
		if i > 0 && words[i-1] == "-m" {
			if mod, _, _ := strings.Cut(strings.ToLower(w), "."); mod == "tmx" || mod == "tmxg" {
				return true
			}
		}
		for _, part := range strings.FieldsFunc(w, func(r rune) bool { return r == '/' || r == '\\' }) {
			name := strings.ToLower(part)
			if i := strings.LastIndexByte(name, '.'); i > 0 {
				name = name[:i]
			}
			if name == "tmx" || name == "tmxg" {
				return true
			}
		}
	}
	return false
}

// Server is one MCP server Claude Code has, as the owner sees it (no config values: env and
// headers can hold secrets).
type Server struct {
	ID        string `json:"id"`               // what a profile lists
	Name      string `json:"name"`             // the server's own name
	Source    string `json:"source"`           // "user" or "plugin"
	Plugin    string `json:"plugin,omitempty"` // the plugin's id (name@marketplace)
	Enabled   bool   `json:"enabled"`          // a plugin server: its plugin is enabled
	Transport string `json:"transport"`        // stdio, http, sse, ws
	Detail    string `json:"detail"`           // the command (stdio) or the URL's host
}

// Plugin is one installed Claude Code plugin.
type Plugin struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Scope   string   `json:"scope"`
	Enabled bool     `json:"enabled"`
	Servers []string `json:"servers"` // ids of its MCP servers
}

// Inventory is what Claude Code has installed.
type Inventory struct {
	Available bool     `json:"available"`       // claude was found and answered
	Error     string   `json:"error,omitempty"` // why not, or a partial failure
	Servers   []Server `json:"servers"`
	Plugins   []Plugin `json:"plugins"`

	configs map[string]json.RawMessage // id → the server's config, ready for --mcp-config
}

// Config returns a server's config for an agent's --mcp-config file.
func (inv Inventory) Config(id string) (json.RawMessage, bool) {
	c, ok := inv.configs[id]
	return c, ok
}

// Options configure a Service.
type Options struct {
	Claude func() (string, error) // the claude executable
	Home   string                 // the user's home ("" = os.UserHomeDir)
	// UserHome: the OS user's own home, whose .claude.json is read besides Home's or
	// CLAUDE_CONFIG_DIR's ("" = os.UserHomeDir, unless Home is set: tests).
	UserHome string
	Env      []string      // environment for claude (nil = os.Environ)
	TTL      time.Duration // how long an inventory is reused (0 = 30 s)
	Timeout  time.Duration // per claude command (0 = 20 s; installs get 5 minutes)
	// Run replaces running claude (tests).
	Run func(ctx context.Context, path string, args ...string) (stdout []byte, stderr string, err error)
}

// Service lists and installs Claude Code extensions; it caches the inventory briefly.
type Service struct {
	o Options

	mu   sync.Mutex
	inv  Inventory
	at   time.Time
	busy chan struct{} // closed when the running list ends
}

// New returns a Service.
func New(o Options) *Service {
	if o.TTL == 0 {
		o.TTL = 30 * time.Second
	}
	if o.Timeout == 0 {
		o.Timeout = 20 * time.Second
	}
	if o.Run == nil {
		o.Run = func(ctx context.Context, path string, args ...string) ([]byte, string, error) {
			cmd := hiddenexec.CommandContext(ctx, path, args...) // no console window (fleet rule)
			cmd.WaitDelay = 2 * time.Second
			cmd.Env = o.Env
			cmd.Dir = os.TempDir()
			var out, errb bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &errb
			err := cmd.Run()
			return out.Bytes(), errb.String(), err
		}
	}
	return &Service{o: o}
}

// Inventory returns what Claude Code has installed, at most TTL old (fresh = always ask now).
func (s *Service) Inventory(ctx context.Context, fresh bool) Inventory {
	for {
		s.mu.Lock()
		if !fresh && !s.at.IsZero() && time.Since(s.at) < s.o.TTL {
			inv := s.inv
			s.mu.Unlock()
			return inv
		}
		if s.busy != nil { // one list at a time; the others wait for it
			busy := s.busy
			s.mu.Unlock()
			select {
			case <-busy:
				fresh = false
				continue
			case <-ctx.Done():
				return Inventory{Error: ctx.Err().Error(), Servers: []Server{}, Plugins: []Plugin{}}
			}
		}
		s.busy = make(chan struct{})
		s.mu.Unlock()

		inv := s.list(ctx)

		s.mu.Lock()
		s.inv, s.at = inv, time.Now()
		close(s.busy)
		s.busy = nil
		s.mu.Unlock()
		return inv
	}
}

// Forget drops the cached inventory (after an install).
func (s *Service) Forget() {
	s.mu.Lock()
	s.at = time.Time{}
	s.mu.Unlock()
}

func (s *Service) list(ctx context.Context) Inventory {
	inv := Inventory{Servers: []Server{}, Plugins: []Plugin{}, configs: map[string]json.RawMessage{}}
	var errs []string
	path, err := s.o.Claude()
	if err != nil {
		inv.Error = "Claude Code is not installed: " + err.Error()
		return inv
	}
	cctx, cancel := context.WithTimeout(ctx, s.o.Timeout)
	out, stderr, err := s.o.Run(cctx, path, "plugin", "list", "--json")
	cancel()
	if err != nil {
		errs = append(errs, "claude plugin list: "+firstLine(stderr, err))
	} else if err := inv.addPlugins(out); err != nil {
		errs = append(errs, "claude plugin list: "+err.Error())
	} else {
		inv.Available = true
	}
	if err := inv.addUserServers(s.userConfig()); err != nil {
		errs = append(errs, err.Error())
	} else {
		inv.Available = true
	}
	// The user's own ~/.claude.json is always read too: the daemon's agents may run on an
	// account home of their own (CLAUDE_CONFIG_DIR), while the owner registers his servers in
	// his own Claude Code (Hosta, 17 Nov: companion and arc-desktop). Where both files name a
	// server, the first one read (the account home's) stays.
	if own := s.ownUserConfig(); own != "" && own != s.userConfig() {
		if err := inv.addUserServers(own); err != nil {
			errs = append(errs, err.Error())
		} else {
			inv.Available = true
		}
	}
	slices.SortFunc(inv.Servers, func(a, b Server) int {
		if a.Source != b.Source {
			return strings.Compare(b.Source, a.Source) // user servers first
		}
		return strings.Compare(a.ID, b.ID)
	})
	slices.SortFunc(inv.Plugins, func(a, b Plugin) int { return strings.Compare(a.ID, b.ID) })
	inv.Error = strings.Join(errs, "; ")
	return inv
}

// userConfig is the file Claude Code keeps its user-scope servers in: ~/.claude.json, or
// .claude.json in CLAUDE_CONFIG_DIR when claude runs with one.
func (s *Service) userConfig() string {
	if s.o.Home != "" {
		return filepath.Join(s.o.Home, ".claude.json")
	}
	env := s.o.Env
	if env == nil {
		env = os.Environ()
	}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, "CLAUDE_CONFIG_DIR") && v != "" {
			return filepath.Join(v, ".claude.json")
		}
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".claude.json")
}

// ownUserConfig is the OS user's own ~/.claude.json, whatever CLAUDE_CONFIG_DIR says (Options.
// UserHome for tests; "" when the home is unknown, or in a test that set Home only).
func (s *Service) ownUserConfig() string {
	if s.o.UserHome != "" {
		return filepath.Join(s.o.UserHome, ".claude.json")
	}
	if s.o.Home != "" {
		return ""
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".claude.json")
}

type listedPlugin struct {
	ID          string                     `json:"id"`
	Version     string                     `json:"version"`
	Scope       string                     `json:"scope"`
	Enabled     bool                       `json:"enabled"`
	InstallPath string                     `json:"installPath"`
	MCPServers  map[string]json.RawMessage `json:"mcpServers"`
}

func (inv *Inventory) addPlugins(out []byte) error {
	var list []listedPlugin
	if err := json.Unmarshal(out, &list); err != nil {
		return errors.New("not the JSON tmx expects")
	}
	for _, lp := range list {
		if lp.ID == "" {
			continue
		}
		name, _, _ := strings.Cut(lp.ID, "@")
		p := Plugin{ID: lp.ID, Name: name, Version: lp.Version, Scope: lp.Scope, Enabled: lp.Enabled, Servers: []string{}}
		for sname, raw := range lp.MCPServers {
			if Reserved(sname) || ReservedConfig(raw) {
				continue
			}
			cfg, err := pluginConfig(raw, lp.InstallPath)
			if err != nil || ReservedConfig(cfg) { // also once ${CLAUDE_PLUGIN_ROOT} is filled in
				continue
			}
			id := PluginPrefix + name + ":" + sname
			p.Servers = append(p.Servers, id)
			inv.configs[id] = cfg
			inv.Servers = append(inv.Servers, describe(Server{ID: id, Name: sname, Source: "plugin", Plugin: lp.ID, Enabled: lp.Enabled}, cfg))
		}
		slices.Sort(p.Servers)
		inv.Plugins = append(inv.Plugins, p)
	}
	return nil
}

// pluginConfig fills ${CLAUDE_PLUGIN_ROOT} (as Claude Code does) and sets it in a stdio
// server's environment too.
func pluginConfig(raw json.RawMessage, root string) (json.RawMessage, error) {
	if root != "" {
		esc, _ := json.Marshal(root)
		raw = bytes.ReplaceAll(raw, []byte("${CLAUDE_PLUGIN_ROOT}"), esc[1:len(esc)-1])
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, errors.New("not an object")
	}
	if root != "" && transport(m) == "stdio" {
		env, _ := m["env"].(map[string]any)
		if env == nil {
			env = map[string]any{}
		}
		if _, ok := env["CLAUDE_PLUGIN_ROOT"]; !ok {
			env["CLAUDE_PLUGIN_ROOT"] = root
		}
		m["env"] = env
	}
	return json.Marshal(m)
}

func (inv *Inventory) addUserServers(path string) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading ~/.claude.json: %w", err)
	}
	var top struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &top); err != nil {
		return errors.New("~/.claude.json is not JSON")
	}
	for name, raw := range top.MCPServers {
		if Reserved(name) || ReservedConfig(raw) {
			continue
		}
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil || m == nil {
			continue
		}
		if _, have := inv.configs[name]; have {
			continue // named in a file read before this one
		}
		inv.configs[name] = raw
		inv.Servers = append(inv.Servers, describe(Server{ID: name, Name: name, Source: "user", Enabled: true}, raw))
	}
	return nil
}

func transport(m map[string]any) string {
	t, _ := m["type"].(string)
	switch {
	case t != "":
		return t
	case m["url"] != nil:
		return "http"
	default:
		return "stdio"
	}
}

func describe(s Server, cfg json.RawMessage) Server {
	var m map[string]any
	_ = json.Unmarshal(cfg, &m)
	s.Transport = transport(m)
	if s.Transport == "stdio" {
		cmd, _ := m["command"].(string)
		s.Detail = filepath.Base(cmd)
		if args, ok := m["args"].([]any); ok {
			for _, a := range args {
				if as, ok := a.(string); ok && !strings.HasPrefix(as, "-") {
					if filepath.IsAbs(as) || strings.HasPrefix(as, ".") { // a script, not a package name
						as = filepath.Base(as)
					}
					s.Detail += " " + as
					break
				}
			}
		}
	} else if u, _ := m["url"].(string); u != "" {
		host := u
		if _, rest, ok := strings.Cut(u, "://"); ok {
			host = rest
		}
		host, _, _ = strings.Cut(host, "/")
		s.Detail = host
	}
	return s
}

// ---- installing

var (
	pluginRx = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}(@[A-Za-z0-9][A-Za-z0-9._/-]{0,199})?$`)
	nameRx   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
)

// ValidServerID reports whether id can name a server in a profile.
func ValidServerID(id string) bool {
	if rest, ok := strings.CutPrefix(id, PluginPrefix); ok {
		p, srv, ok := strings.Cut(rest, ":")
		return ok && nameRx.MatchString(p) && nameRx.MatchString(srv)
	}
	return nameRx.MatchString(id)
}

// InstallPlugin runs `claude plugin install <plugin> --json` (user scope). A plugin whose
// marketplace runs a command to install needs the owner's confirmation in a terminal: that
// comes back as the error.
func (s *Service) InstallPlugin(ctx context.Context, plugin string) error {
	if !pluginRx.MatchString(plugin) {
		return fmt.Errorf("%q is not a plugin name (name or name@marketplace)", plugin)
	}
	return s.install(ctx, "plugin", "install", plugin, "--scope", "user", "--json")
}

// AddServer runs `claude mcp add-json -s user <name> <config>`.
func (s *Service) AddServer(ctx context.Context, name string, config json.RawMessage) error {
	if !nameRx.MatchString(name) || Reserved(name) {
		return fmt.Errorf("%q is not a server name tmx can add (letters, digits, - and _; not tmx or tmxg)", name)
	}
	var m map[string]any
	if err := json.Unmarshal(config, &m); err != nil || m == nil {
		return errors.New("the server's config must be a JSON object")
	}
	if ReservedConfig(config) {
		return errors.New("this config starts tmx itself; tmx's own tools reach agents by tmx's rules, never as a server of a profile")
	}
	if where := writtenSecret(m); where != "" {
		return fmt.Errorf("the config writes out %s: name a variable (${NAME}) instead of its value; tmx adds no server with a key or password in its config", where)
	}
	compact, _ := json.Marshal(m)
	return s.install(ctx, "mcp", "add-json", "-s", "user", name, string(compact))
}

// refRx is a reference to an environment variable, as Claude Code expands it in a server's
// config: ${NAME} or ${NAME:-default}.
var refRx = regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*(:-[^}]*)?\}`)

// secretWords mark a name that holds a credential (agentpolicy's list for the agents'
// environment, plus the two HTTP headers that carry one).
var secretWords = []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "API_KEY", "APIKEY", "ACCESS_KEY", "PRIVATE_KEY", "CREDENTIAL", "AUTHORIZATION", "COOKIE"}

// writtenSecret names the first env or headers entry of a server's config that holds a
// credential by its name and writes the value out instead of naming a variable ("" = none).
// tmx's own way of adding a server refuses those (fleet rule: a config may name a secret, never
// hold its value); what the owner adds with claude itself is theirs.
func writtenSecret(m map[string]any) string {
	for _, part := range []string{"env", "headers"} {
		kv, _ := m[part].(map[string]any)
		keys := make([]string, 0, len(kv))
		for k := range kv {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			v, _ := kv[k].(string)
			if v == "" || refRx.MatchString(v) {
				continue
			}
			up := strings.ToUpper(k)
			if slices.ContainsFunc(secretWords, func(w string) bool { return strings.Contains(up, w) }) {
				return part + "." + k
			}
		}
	}
	return ""
}

func (s *Service) install(ctx context.Context, args ...string) error {
	path, err := s.o.Claude()
	if err != nil {
		return fmt.Errorf("Claude Code is not installed: %w", err)
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	out, stderr, err := s.o.Run(cctx, path, args...)
	s.Forget()
	if err != nil {
		msg := firstLine(stderr, nil)
		if msg == "" {
			msg = firstLine(string(out), err)
		}
		return fmt.Errorf("claude %s %s: %s", args[0], args[1], msg)
	}
	return nil
}

func firstLine(s string, err error) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300]
	}
	if s == "" && err != nil {
		return err.Error()
	}
	return s
}
