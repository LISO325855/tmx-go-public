package guardhook

import "testing"

// The agent's own account home reaches the guard from the daemon's launch environment of
// Claude (FromEnv). A Codex shim's environment is the agent's own, so FromManifest never takes it.
func TestAccountHomeComesFromTheLaunchOnly(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/state/ai-accounts/homes/claude/second")
	if got := FromEnv().Context.AccountHome; got != "/state/ai-accounts/homes/claude/second" {
		t.Fatalf("FromEnv: %q", got)
	}
	if got := FromManifest("/own", "/ws", "/state").Context.AccountHome; got != "" {
		t.Fatalf("FromManifest took the account home from the shim's environment: %q", got)
	}
}
