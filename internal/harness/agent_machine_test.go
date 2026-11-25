package harness

import (
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/runner"
)

func TestAgentMachineCodexPolicy(t *testing.T) {
	old := codexGOOS
	t.Cleanup(func() { codexGOOS = old })
	for _, tc := range []struct {
		os, mode, sandbox, approval string
		full                        bool
	}{
		{"linux", "", "workspace-write", "never", false},
		{"linux", "", "workspace-write", "never", true},
		{"linux", "plan", "read-only", "never", false},
		{"windows", "", "danger-full-access", "on-request", false},
	} {
		codexGOOS = tc.os
		sandbox, approval := CodexPolicy(runner.Spec{AgentMachine: true, PermissionMode: tc.mode, CodexFullAccess: tc.full})
		if sandbox != tc.sandbox || approval != tc.approval {
			t.Errorf("%s %s: got %s/%s, want %s/%s", tc.os, tc.mode, sandbox, approval, tc.sandbox, tc.approval)
		}
	}
}
