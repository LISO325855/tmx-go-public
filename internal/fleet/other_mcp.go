package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
)

// Pi's trusted workspace config overrides its user config, including enabled-only
// overrides and names that normalize '-' to '_'. Refuse conflicts rather than
// silently handing the agent a different or disabled managed server.
func (f *Fleet) checkPiWorkspaceMCPLocked(r *agentRec, spec *runner.Spec) error {
	path := filepath.Join(spec.Cwd, ".pi", "mcp.json")
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	var cfg struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err == nil {
		err = json.Unmarshal(b, &cfg)
	}
	if err != nil {
		f.mcpWarningLocked(r, "Profile MCP tools not available for pi agents: workspace .pi/mcp.json could not be validated; launch refused.")
		return fmt.Errorf("pi workspace MCP configuration could not be validated")
	}
	managed := map[string]string{"tmxg": "tmxg"}
	for name := range spec.ProfileMCP {
		managed[strings.ReplaceAll(name, "-", "_")] = name
	}
	for name := range cfg.Servers {
		if selected, ok := managed[strings.ReplaceAll(name, "-", "_")]; ok {
			message := selected + " not available for pi agents: workspace .pi/mcp.json overrides a managed server; launch refused."
			f.mcpWarningLocked(r, message)
			return fmt.Errorf("pi workspace MCP config overrides managed server %s", selected)
		}
	}
	return nil
}

func (f *Fleet) mcpWarningLocked(r *agentRec, text string) {
	r.a.MCPWarnings = append(r.a.MCPWarnings, text)
	f.appendLocked(r, model.TimelineEntry{Kind: model.EntrySystem, Text: text})
}

func launchEnv(spec *runner.Spec, key string) string {
	value := os.Getenv(key)
	for _, kv := range spec.Env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			value = v
		}
	}
	return value
}

// otherMCPConfigLocked uses each CLI's native configuration, isolated per agent.
// Gemini's system override preserves the original system policy. Pi and agy
// have no MCP-only override, so they get private homes with only known auth and
// preference files copied; no workspace or owner's MCP config is overwritten.
func (f *Fleet) otherMCPConfigLocked(r *agentRec, spec *runner.Spec) error {
	h := r.a.Harness
	if h != model.HarnessGemini && h != model.HarnessPi && h != "agy" && h != "antigravity" {
		return nil
	}
	base := filepath.Join(f.opts.Paths.AgentDir(r.a.ID), "profile-mcp")
	servers := map[string]any{}
	for name, server := range spec.ProfileMCP {
		servers[name] = map[string]any{"command": server.Command, "args": server.Args, "env": server.Env}
	}
	if spec.MCP != nil {
		env := map[string]string{}
		for _, key := range spec.MCP.EnvKeys {
			env[key] = launchEnv(spec, key)
		}
		servers["tmxg"] = map[string]any{"command": spec.MCP.Command, "args": spec.MCP.Args, "env": env}
	}
	cfg := map[string]any{"mcpServers": servers}
	path := ""
	user, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	switch h {
	case model.HarnessGemini:
		original := launchEnv(spec, "GEMINI_CLI_SYSTEM_SETTINGS_PATH")
		if original == "" {
			switch runtime.GOOS {
			case "windows":
				original = filepath.Join(os.Getenv("ProgramData"), "gemini-cli", "settings.json")
			case "darwin":
				original = "/Library/Application Support/GeminiCli/settings.json"
			default:
				original = "/etc/gemini-cli/settings.json"
			}
		}
		if b, err := os.ReadFile(original); err == nil {
			if err := json.Unmarshal(b, &cfg); err != nil {
				return err
			}
			if cfg == nil {
				cfg = map[string]any{}
			}
			old, _ := cfg["mcpServers"].(map[string]any)
			for name, server := range servers {
				if old == nil {
					old = map[string]any{}
				}
				old[name] = server
			}
			cfg["mcpServers"] = old
		} else if !os.IsNotExist(err) {
			return err
		}
		path = filepath.Join(base, "gemini-settings.json")
		spec.Env = append(spec.Env, "GEMINI_CLI_SYSTEM_SETTINGS_PATH="+path)
	case model.HarnessPi:
		if err := f.checkPiWorkspaceMCPLocked(r, spec); err != nil {
			return err
		}
		source := launchEnv(spec, "PI_CODING_AGENT_DIR")
		if source == "" {
			source = filepath.Join(user, ".pi", "agent")
		}
		home := filepath.Join(base, "pi")
		if err := copyMCPHomeFiles(source, home, []string{"auth.json", "models.json", "settings.json"}); err != nil {
			return err
		}
		path = filepath.Join(home, "mcp.json")
		spec.Env = append(spec.Env, "PI_CODING_AGENT_DIR="+home)
		if launchEnv(spec, "PI_CODING_AGENT_SESSION_DIR") == "" {
			spec.Env = append(spec.Env, "PI_CODING_AGENT_SESSION_DIR="+filepath.Join(source, "sessions"))
		}
	case "agy", "antigravity":
		source := launchEnv(spec, "USERPROFILE")
		if runtime.GOOS != "windows" {
			source = launchEnv(spec, "HOME")
		}
		if source == "" {
			source = user
		}
		home := filepath.Join(base, "agy")
		if err := copyMCPHomeFiles(filepath.Join(source, ".gemini"), filepath.Join(home, ".gemini"), agyMCPHomeFiles); err != nil {
			return err
		}
		path = filepath.Join(home, ".gemini", "config", "mcp_config.json")
		spec.Env = append(spec.Env, "HOME="+home, "USERPROFILE="+home)
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	spec.MCPConfigPath = path
	return config.WriteSecretFile(path, b)
}

var agyMCPHomeFiles = []string{"google_accounts.json", "oauth_creds.json", "gemini-credentials.json", "settings.json", "antigravity-cli/settings.json"}

func copyMCPHomeFiles(source, dest string, names []string) error {
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(source, name))
		if os.IsNotExist(err) {
			// A signed-out account must not leave a stale credential copy.
			if err := os.Remove(filepath.Join(dest, name)); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if err := config.WriteSecretFile(filepath.Join(dest, name), b); err != nil {
			return err
		}
	}
	return nil
}

func (f *Fleet) deleteProfileMCPAuth(id string) {
	base := filepath.Join(f.opts.Paths.AgentDir(id), "profile-mcp")
	paths := []string{filepath.Join(base, "pi", "auth.json"), filepath.Join(base, "pi", "models.json"), filepath.Join(base, "pi", "settings.json")}
	for _, name := range agyMCPHomeFiles {
		paths = append(paths, filepath.Join(base, "agy", ".gemini", name))
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			f.log.Warn("delete profile MCP auth copy", "agent", id, "err", err)
		}
	}
}
