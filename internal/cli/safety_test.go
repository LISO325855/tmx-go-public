package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/client"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

func TestGateAndGuardCommands(t *testing.T) {
	d := cliDaemon(t)
	if out, _, code := tmx(t, "gate"); code != 0 || !strings.Contains(out, "owner gate: off") {
		t.Fatalf("gate status: %d %s", code, out)
	}
	out, errs, code := tmx(t, "gate", "on")
	if code != 0 || !strings.Contains(out, "owner gate: on") || !strings.Contains(errs, "Confirm on this machine with a test prompt: tmx is trying to turn on the owner gate") {
		t.Fatalf("gate on: %d %s %s", code, out, errs)
	}

	// A guard hold, approved from the CLI through the gate.
	agent := client.New(d.URL, d.AgentToken(t, "head"))
	var ap model.Approval
	if err := agent.Post(context.Background(), "/api/guard/holds",
		map[string]string{"tool": "Bash", "command": "sudo true", "rule": "elevation", "match": "sudo true"}, &ap); err != nil {
		t.Fatal(err)
	}
	if out, _, _ := tmx(t, "approvals"); !strings.Contains(out, "held by the guard for head (Administrator rights): sudo true") {
		t.Fatalf("approvals: %s", out)
	}
	out, errs, code = tmx(t, "approve", ap.ID)
	if code != 0 || !strings.Contains(out, "allowed") || !strings.Contains(errs, "allow “sudo true” for head") {
		t.Fatalf("approve: %d %s %s", code, out, errs)
	}

	// The guard has no on/off switch: status lists the rules by state, on|off is refused with
	// the reason, and nothing was asked of the owner or changed.
	out, _, code = tmx(t, "guard")
	if code != 0 || !strings.Contains(out, "guard: on for every agent") ||
		!strings.Contains(out, "blocked: Disk wipe or format; Delete a root, home or system folder; Permissions on system folders; Boot and system protection\n") ||
		!strings.Contains(out, "held for you: ") || !strings.Contains(out, "Administrator rights") || !strings.Contains(out, "allowed: ") {
		t.Fatalf("guard: %d %s", code, out)
	}
	if again, _, code := tmx(t, "guard", "status"); code != 0 || again != out {
		t.Fatalf("guard status: %d %s", code, again)
	}
	prompts := len(d.GateAuth.Prompts())
	for _, args := range [][]string{{"guard", "off"}, {"guard", "on"}, {"guard", "off", "--for", "worker"}} {
		out, errs, code := tmx(t, args...)
		if code != 2 || out != "" || !strings.Contains(errs, "no on/off switch") {
			t.Fatalf("%v: %d %q %q", args, code, out, errs)
		}
	}
	if len(d.GateAuth.Prompts()) != prompts {
		t.Fatal("a removed guard command asked the owner for a proof")
	}
	d.GateAuth.SetAnswer(func(context.Context, string) error { return context.Canceled })
	if _, errs, code := tmx(t, "gate", "off"); code != 1 || !strings.Contains(errs, "not confirmed") {
		t.Fatalf("gate off refused: %d %s", code, errs)
	}
	d.GateAuth.SetAnswer(nil)
	if out, _, code := tmx(t, "gate", "off"); code != 0 || !strings.Contains(out, "owner gate: off") {
		t.Fatalf("gate off: %d %s", code, out)
	}
	if _, errs, code := tmx(t, "hook", "nope"); code != 2 || !strings.Contains(errs, "pretooluse") {
		t.Fatalf("hook usage: %d %s", code, errs)
	}
}
