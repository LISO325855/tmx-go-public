package codexguard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/claudeext"
	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/runner"
)

// ConfigLease pins the generated settings for a running agent. The directory
// must be agent-specific and outside the agent's workspace.
type ConfigLease struct {
	Home string
	Pins []*Pin
}

func (l *ConfigLease) Close() error {
	var first error
	for _, pin := range l.Pins {
		if err := pin.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (l *ConfigLease) Verify() error {
	for _, pin := range l.Pins {
		if err := pin.Verify(); err != nil {
			return err
		}
	}
	return nil
}

// NewConfig writes only tmx-owned configuration; it never imports the owner's
// config, hooks, plugins or notification commands. Only explicitly selected
// managed MCP servers are added. Authentication
// provisioning is a separate owner flow and is not copied by this function.
func NewConfig(home, cwd, instructions string, managed ...map[string]runner.MCPServer) (*ConfigLease, error) {
	if !filepath.IsAbs(home) || !filepath.IsAbs(cwd) {
		return nil, fmt.Errorf("Codex home and workspace must be absolute")
	}
	if err := os.MkdirAll(home, 0700); err != nil {
		return nil, err
	}
	quoted, _ := json.Marshal(cwd) // JSON strings are TOML basic strings.
	body := `sandbox_mode = "danger-full-access"
approval_policy = "on-request"
web_search = "disabled"
notify = []
check_for_update_on_startup = false
project_doc_max_bytes = 0

[mcp_servers]
[hooks]
[plugins]

[features]
apps = false
plugins = false
multi_agent = false
hooks = false

[projects.` + string(quoted) + `]
trust_level = "untrusted"
`
	if len(managed) > 0 {
		body += MCPConfig(managed[0])
		// This is a fresh launch after the prior lease closed. Rebuild from the
		// daemon's profile selection, never from the old on-disk contents.
		if err := config.WriteSecretFile(filepath.Join(home, "config.toml"), []byte(body)); err != nil {
			return nil, err
		}
	}
	l := &ConfigLease{Home: home}
	for _, entry := range []struct{ name, body string }{{"config.toml", body}, {"AGENTS.md", instructions}} {
		path := filepath.Join(home, entry.name)
		// Pin the generated settings for this launch. Legacy callers without a
		// managed selection must still match existing settings exactly.
		pin, err := writePinned(path, []byte(entry.body))
		if err != nil {
			l.Close()
			return nil, err
		}
		l.Pins = append(l.Pins, pin)
	}
	return l, nil
}

// MCPConfig serializes managed stdio servers, with tmxg first. Values stay in a
// private config file, never process arguments. Quoted keys support plugin names.
func MCPConfig(servers map[string]runner.MCPServer) string {
	quote := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	names := make([]string, 0, len(servers))
	for name := range servers {
		if name != "tmxg" && !claudeext.Reserved(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if _, ok := servers["tmxg"]; ok {
		names = append([]string{"tmxg"}, names...)
	}
	var b strings.Builder
	for _, name := range names {
		server := servers[name]
		args := server.Args
		if args == nil {
			args = []string{}
		}
		fmt.Fprintf(&b, "\n[mcp_servers.%s]\ncommand = %s\nargs = %s\n", quote(name), quote(server.Command), quote(args))
		if name != "tmxg" {
			b.WriteString("default_tools_approval_mode = \"approve\"\n")
		}
		if len(server.EnvKeys) > 0 {
			fmt.Fprintf(&b, "env_vars = %s\n", quote(server.EnvKeys))
		}
		if len(server.Env) > 0 {
			fmt.Fprintf(&b, "[mcp_servers.%s.env]\n", quote(name))
			keys := make([]string, 0, len(server.Env))
			for key := range server.Env {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				fmt.Fprintf(&b, "%s = %s\n", quote(key), quote(server.Env[key]))
			}
		}
	}
	return b.String()
}
