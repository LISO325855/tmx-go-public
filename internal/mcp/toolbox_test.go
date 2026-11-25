package mcp

import (
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/blackcode-ch/tmx-go/internal/toolbox"
	"github.com/blackcode-ch/tmx-go/internal/toolbox/toolboxtest"
)

// Every agent reads the Resources list and one record; nothing in an answer is a number, an
// address or a key.
func TestResourceTools(t *testing.T) {
	toolboxtest.Install(t)
	d := apitest.New(t)
	cs := session(t, d, "head")

	out, isErr := call(t, cs, "resources", nil)
	if isErr {
		t.Fatalf("resources: %s", out)
	}
	for _, want := range []string{"11 resources", "COMMUNICATION", "TOOL", "ACCOUNT", "DEVICE", "STORAGE",
		"- chat-node | Chat, the owner's number, in the node | works | node-a",
		"- fleet-phone | Fleet phone number and SMS | partly | phone@box-c",
		"- virtual-phone | Virtual phone | planned | Not built.",
		"- network | Private network | not verified | Each machine."} {
		if !strings.Contains(out, want) {
			t.Errorf("resources: missing %q in:\n%s", want, out)
		}
	}
	for _, l := range strings.Split(out, "\n") {
		if shape := toolbox.SecretShape(l); shape != "" {
			t.Errorf("resources: %s in %q", shape, l)
		}
	}

	out, isErr = call(t, cs, "resources", map[string]any{"kind": "device"})
	if isErr || !strings.Contains(out, "2 resources") || strings.Contains(out, "chat-node") {
		t.Fatalf("devices: %s", out)
	}
	out, isErr = call(t, cs, "resources", map[string]any{"state": "down"})
	if isErr || out != "No resources match." {
		t.Fatalf("down: %s", out)
	}
	if out, isErr = call(t, cs, "resources", map[string]any{"kind": "gadget"}); !isErr || !strings.Contains(out, "unknown kind") {
		t.Fatalf("unknown kind: %v %s", isErr, out)
	}

	out, isErr = call(t, cs, "resource", map[string]any{"id": "chat-browser"})
	if isErr {
		t.Fatalf("resource: %s", out)
	}
	for _, want := range []string{"Chat, the owner's number, in the assistant's browser (chat-browser, communication)",
		"State: works. Checked by assistant-lead@box-b on 2026-11-17.",
		"Held by: sender@box-b. The sender types; a watcher reads the work chats.",
		"Depends on: assistant",
		"- send a message (send as the owner): mail to sender@box-b. Carries the owner's order: the words and the time.",
		"Rule: Any agent may ask.",
		"Source: declared"} {
		if !strings.Contains(out, want) {
			t.Errorf("resource: missing %q in:\n%s", want, out)
		}
	}
	out, isErr = call(t, cs, "resource", map[string]any{"id": "ai-accounts"})
	if isErr || !strings.Contains(out, "Rule: Hard work on the strong model, easy work on the cheap one.") ||
		!strings.Contains(out, "- launch an agent on an account (act): tool agent_launch") {
		t.Fatalf("ai-accounts: %s", out)
	}
	out, isErr = call(t, cs, "resource", map[string]any{"id": "assistant"})
	if isErr || !strings.Contains(out, "Secrets it needs (names only): Companion agent key, Companion link token") {
		t.Fatalf("companion: %s", out)
	}
	if out, isErr = call(t, cs, "resource", map[string]any{"id": "nothing"}); !isErr || !strings.Contains(out, "no such resource") {
		t.Fatalf("unknown id: %v %s", isErr, out)
	}
	if out, isErr = call(t, cs, "resource", map[string]any{"id": " "}); !isErr {
		t.Fatalf("empty id: %s", out)
	}
}

// The tools the declared routes name exist: a line must not send an agent to a tool that is
// not there.
func TestResourceRoutesNameRealTools(t *testing.T) {
	d := apitest.New(t)
	d.EnableWhatsApp(t)
	cs := session(t, d, "head")
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, tl := range res.Tools {
		have[tl.Name] = true
	}
	// companion_task is offered only to the agents the owner allows to use Companion.
	have["companion_task"] = true
	// The build's own list (when it has one) and the fixture.
	seed := append(toolbox.Seed(), toolboxtest.Install(t)...)
	for _, r := range seed {
		for _, a := range r.Actions {
			if a.Route.Kind != toolbox.RouteTool || strings.Contains(a.Route.Tool, " ") {
				continue // a described route ("git and gh (command line)"), not a tool name
			}
			if !have[a.Route.Tool] {
				t.Errorf("%s: action %q names the tool %q, which is not registered", r.ID, a.Name, a.Route.Tool)
			}
		}
	}
}
