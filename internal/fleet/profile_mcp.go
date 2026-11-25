package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/codexguard"
	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/runner"
)

// Only stdio entries have a lossless translation to the managed harness config.
func (f *Fleet) profileStdioLocked(r *agentRec, ext extServers) map[string]runner.MCPServer {
	out := map[string]runner.MCPServer{}

	for name, raw := range ext.namedConfigs() {
		var server struct {
			Type    string            `json:"type"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			URL     string            `json:"url"`
		}
		if json.Unmarshal(raw, &server) != nil || strings.TrimSpace(server.Command) == "" || (server.Type != "" && server.Type != "stdio") || server.URL != "" {
			f.mcpWarningLocked(r, name+" not available for "+string(r.a.Harness)+" agents: "+name+" cannot be translated to stdio configuration.")
			continue
		}
		out[name] = runner.MCPServer{Command: server.Command, Args: server.Args, Env: server.Env}
	}
	return out
}

// Non-parity Codex launches also need a private config: never edit the account's
// config and never expose a profile server's environment on the command line.
func (f *Fleet) codexProfileHomeLocked(r *agentRec, spec *runner.Spec, selected bool) error {
	home := filepath.Join(f.opts.Paths.AgentDir(r.a.ID), "codex-profile")
	if _, err := os.Stat(home); !selected && os.IsNotExist(err) {
		return nil
	}
	source := os.Getenv("CODEX_HOME")
	for _, kv := range spec.Env {
		if value, ok := strings.CutPrefix(kv, "CODEX_HOME="); ok && value != "" {
			source = value
		}
	}
	if source == "" {
		user, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		source = filepath.Join(user, ".codex")
	}
	if err := os.MkdirAll(home, 0700); err != nil {
		return err
	}
	servers := make(map[string]runner.MCPServer, len(spec.ProfileMCP)+1)
	for name, server := range spec.ProfileMCP {
		servers[name] = server
	}
	if spec.MCP != nil {
		servers["tmxg"] = *spec.MCP
	}
	if err := config.WriteSecretFile(filepath.Join(home, "config.toml"), []byte(codexguard.MCPConfig(servers))); err != nil {
		return err
	}
	if err := codexguard.SyncOwnerAuth(source, home); err != nil {
		return err
	}
	f.carryCodexRolloutLocked(r, home)
	spec.Env = append(spec.Env, "CODEX_HOME="+home)
	return nil
}
