package mcp

import (
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

func TestFormatTreeShowsMCPWarnings(t *testing.T) {
	text := FormatTree(model.Snapshot{Agents: []model.Agent{{ID: "a", Name: "a", MCPWarnings: []string{"arc-desktop not available for shell agents: no MCP client"}}}})
	if !strings.Contains(text, "WARNING: arc-desktop not available for shell agents") {
		t.Fatalf("tmx ls hid MCP warning: %s", text)
	}
}
