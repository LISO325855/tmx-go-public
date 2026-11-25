package codexguard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/runner"
)

func TestManagedProfileMCPConfigRegenerated(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	servers := map[string]runner.MCPServer{
		"tmxg":        {Command: "tmx.exe", Args: []string{"mcp"}},
		"arc-desktop": {Command: `C:\tools\arc.exe`, Args: []string{"--stdio"}, Env: map[string]string{"AGENT": "worker"}},
		"tmx":         {Command: "refused"},
	}
	for _, command := range []string{`C:\tools\arc.exe`, `C:\tools\arc-v2.exe`} {
		server := servers["arc-desktop"]
		server.Command = command
		servers["arc-desktop"] = server
		lease, err := NewConfig(home, root, "instructions", servers)
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(home, "config.toml"))
		if err != nil {
			lease.Close()
			t.Fatal(err)
		}
		quoted, _ := json.Marshal(command)
		for _, want := range []string{`[mcp_servers."tmxg"]`, `[mcp_servers."arc-desktop"]`, string(quoted), `args = ["--stdio"]`, `"AGENT" = "worker"`, `default_tools_approval_mode = "approve"`} {
			if !strings.Contains(string(b), want) {
				t.Errorf("missing %s in config", want)
			}
		}
		if strings.Contains(string(b), "refused") || strings.Index(string(b), `[mcp_servers."tmxg"]`) > strings.Index(string(b), `[mcp_servers."arc-desktop"]`) {
			t.Error("reserved name or ordering violation")
		}
		lease.Close()
	}
	delete(servers, "arc-desktop")
	lease, err := NewConfig(home, root, "instructions", servers)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	b, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil || strings.Contains(string(b), "arc-desktop") || !strings.Contains(string(b), `[mcp_servers."tmxg"]`) {
		t.Fatalf("removed profile server persisted: %v", err)
	}

}

func TestEffectiveProfileMCPMatchesSelection(t *testing.T) {
	want := map[string]runner.MCPServer{"arc-desktop": {Command: "arc", Args: []string{"--stdio"}, Env: map[string]string{"AGENT": "worker"}}}
	server := map[string]any{"command": "arc", "args": []string{"--stdio"}, "env": map[string]string{"AGENT": "worker"}}
	cfg := map[string]any{"sandbox_mode": "danger-full-access", "approval_policy": "on-request", "web_search": "disabled", "notify": []string{}, "features": map[string]bool{"hooks": false, "plugins": false, "apps": false, "multi_agent": false}, "mcp_servers": map[string]any{"tmxg": map[string]any{"command": "tmx.exe", "args": []string{"mcp"}}, "arc-desktop": server}}
	check := func() error {
		b, _ := json.Marshal(map[string]any{"config": cfg})
		return ValidateEffective(b, "tmx.exe", want)
	}
	if err := check(); err != nil {
		t.Fatal(err)
	}
	servers := cfg["mcp_servers"].(map[string]any)
	for _, name := range []string{"tmxg", "arc-desktop"} {
		original := servers[name]
		delete(servers, name)
		if check() == nil {
			t.Errorf("accepted missing %s", name)
		}
		servers[name] = original
		entry := original.(map[string]any)
		entry["enabled"] = false
		if check() == nil {
			t.Errorf("accepted disabled %s", name)
		}
		delete(entry, "enabled")
	}
	own := servers["tmxg"].(map[string]any)
	want["tmxg"] = runner.MCPServer{Command: "tmx.exe", Args: []string{"mcp"}, EnvKeys: []string{"TMXG_URL", "TMXG_AGENT_TOKEN"}}
	if check() == nil {
		t.Error("accepted missing tmxg credential environment contract")
	}
	own["env_vars"] = want["tmxg"].EnvKeys
	if err := check(); err != nil {
		t.Fatal(err)
	}

	for key, bad := range map[string]any{"command": "other", "args": []string{"other"}, "env": map[string]string{"AGENT": "other"}, "url": "https://other.invalid", "env_vars": []string{"OTHER"}} {
		old, exists := server[key]
		server[key] = bad
		if check() == nil {
			t.Errorf("accepted changed %s", key)
		}
		if exists {
			server[key] = old
		} else {
			delete(server, key)
		}
	}
}
