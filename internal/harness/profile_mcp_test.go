package harness

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
)

func TestManagedCodexMCPKeepsOwnServer(t *testing.T) {
	spec := runner.Spec{MCP: &runner.MCPServer{Command: "tmx", Args: []string{"mcp"}}, ProfileMCP: map[string]runner.MCPServer{"tmxg": {Command: "other"}, "arc-desktop": {Command: "arc", Args: []string{"--stdio"}}}}
	got := CodexManagedServers(spec)
	if got["tmxg"].Command != "tmx" || got["arc-desktop"].Command != "arc" {
		t.Fatalf("wrong managed servers: %+v", got)
	}
}

func TestOpenCodeProfileMCPConfig(t *testing.T) {
	spec := runner.Spec{Harness: model.HarnessOpenCode, MCP: &runner.MCPServer{Command: "tmx", Args: []string{"mcp"}}, ProfileMCP: map[string]runner.MCPServer{"tmxg": {Command: "other"}, "arc-desktop": {Command: "arc", Args: []string{"--stdio"}, Env: map[string]string{"AGENT": "worker"}}}}
	spec.Command, _ = os.Executable()
	c, err := TerminalCommand(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range c.Env {
		if raw, ok := strings.CutPrefix(kv, "OPENCODE_CONFIG_CONTENT="); ok {
			var cfg struct {
				MCP map[string]struct {
					Command []string          `json:"command"`
					Env     map[string]string `json:"environment"`
				} `json:"mcp"`
			}
			if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
				t.Fatal(err)
			}
			if cfg.MCP["tmxg"].Command[0] != "tmx" || cfg.MCP["arc-desktop"].Command[0] != "arc" || cfg.MCP["arc-desktop"].Env["AGENT"] != "worker" {
				t.Fatalf("wrong config: %s", raw)
			}
			return
		}
	}
	t.Fatal("missing OpenCode config")
}
