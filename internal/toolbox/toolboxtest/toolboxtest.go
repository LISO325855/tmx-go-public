// Package toolboxtest gives tests a declared Resources list that names no real person,
// machine or account, so the same tests pass in a build with or without a list of its own.
package toolboxtest

import (
	"encoding/json"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/toolbox"
)

// Node is the Go node that measures the fixture's live lines; BoxB and BoxC are the other
// machines its holders live on.
const (
	Node = "node-a"
	BoxB = "box-b"
	BoxC = "box-c"
)

// Lines is how many records the fixture has.
const Lines = 11

// Install makes the fixture the declared list for the rest of the test and returns it.
// Not for parallel tests: the list is one per process.
func Install(t testing.TB) []toolbox.Resource {
	t.Helper()
	before, err := json.Marshal(toolbox.Seed())
	if err != nil {
		t.Fatal(err)
	}
	if err := toolbox.SetSeed([]byte(fixture)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := toolbox.SetSeed(before); err != nil {
			t.Error(err)
		}
	})
	return toolbox.Seed()
}

const fixture = `[
  {
    "id": "chat-node",
    "name": "Chat, the owner's number, in the node",
    "kind": "communication",
    "summary": "Read and send chat messages on the owner's number from the node's own linked device.",
    "identity": {"who": "owner", "label": "The owner's chat number"},
    "machine": "node-a",
    "holder_note": "Built into the node (a linked device, no separate agent).",
    "maintainer": "lead@box-b",
    "how": "Tools whatsapp_chats, whatsapp_read and whatsapp_send.",
    "actions": [
      {"name": "list chats", "type": "read", "route": {"kind": "tool", "tool": "whatsapp_chats"}},
      {"name": "read a chat", "type": "read", "route": {"kind": "tool", "tool": "whatsapp_read"}},
      {"name": "send a message", "type": "send_as_owner", "route": {"kind": "tool", "tool": "whatsapp_send"}, "note": "Waits for the owner unless the chat is on the allowed list."}
    ],
    "rule": {"text": "The agent profiles named in Settings."},
    "state": "works",
    "state_note": "Code read.",
    "verified_at": "2026-11-17",
    "verified_by": "spec-writer",
    "live": "whatsapp",
    "page": "/whatsapp"
  },
  {
    "id": "chat-browser",
    "name": "Chat, the owner's number, in the assistant's browser",
    "kind": "communication",
    "summary": "Read and send chat messages on the owner's number through the assistant's browser.",
    "identity": {"who": "owner", "label": "The owner's chat number"},
    "machine": "box-b",
    "holder": "sender@box-b",
    "holder_note": "The sender types; a watcher reads the work chats.",
    "maintainer": "assistant-lead@box-b",
    "parent": "assistant",
    "how": "Fleet mail to the sender with the chat, the exact text and the order behind it.",
    "actions": [
      {"name": "read a chat", "type": "read", "route": {"kind": "mail", "to": "sender@box-b"}},
      {"name": "send a message", "type": "send_as_owner", "route": {"kind": "mail", "to": "sender@box-b"}, "note": "Carries the owner's order: the words and the time."}
    ],
    "rule": {"text": "Any agent may ask. Only the sender types."},
    "state": "works",
    "verified_at": "2026-11-17",
    "verified_by": "assistant-lead@box-b"
  },
  {
    "id": "fleet-phone",
    "name": "Fleet phone number and SMS",
    "kind": "communication",
    "summary": "The fleet's own mobile number: receive codes and send texts.",
    "identity": {"who": "fleet", "label": "The agents' own mobile number"},
    "machine": "box-c",
    "holder": "phone@box-c",
    "holder_note": "A shell with no model.",
    "maintainer": "lead@box-b",
    "parent": "sim-stick",
    "how": "Fleet mail to the phone shell.",
    "actions": [
      {"name": "read SMS and codes", "type": "read", "route": {"kind": "mail", "to": "phone@box-c"}},
      {"name": "send an SMS", "type": "send_as_fleet", "route": {"kind": "mail", "to": "phone@box-c"}}
    ],
    "rule": {"text": "For accounts the agents create themselves."},
    "state": "partly",
    "state_note": "The service answered its health call; no SMS was sent as a test.",
    "verified_at": "2026-11-17",
    "verified_by": "builder@box-c"
  },
  {
    "id": "voice",
    "name": "Voice calls",
    "kind": "communication",
    "summary": "Phone calls made by an agent on the owner's behalf.",
    "identity": {"who": "owner", "label": "An agent calling for the owner"},
    "parent": "virtual-phone",
    "how": "None yet.",
    "actions": [
      {"name": "make a call", "type": "send_as_owner", "route": {"kind": "none"}}
    ],
    "rule": {"text": "Planned: one approval per call."},
    "state": "planned"
  },
  {
    "id": "assistant",
    "name": "The assistant app and its signed-in browser",
    "kind": "tool",
    "summary": "The owner's assistant app, with a browser signed in as the owner.",
    "identity": {"who": "owner", "label": "The owner's signed-in browser sessions"},
    "machine": "box-b",
    "node": "node-a",
    "holder": "assistant-lead@box-b",
    "maintainer": "assistant-lead@box-b",
    "how": "Tools companion_task and, for the head, companion_message.",
    "actions": [
      {"name": "hand it a task", "type": "act", "route": {"kind": "tool", "tool": "companion_task"}},
      {"name": "talk with it (the head)", "type": "act", "route": {"kind": "tool", "tool": "companion_message"}}
    ],
    "rule": {"text": "All agents read. Only the sender types."},
    "state": "partly",
    "verified_at": "2026-11-17",
    "verified_by": "assistant-lead@box-b",
    "secrets": [{"name": "Companion agent key"}, {"name": "Companion link token"}],
    "live": "companion",
    "page": "/companion"
  },
  {
    "id": "connector",
    "name": "Own mail connector",
    "kind": "tool",
    "summary": "Our own connector to mail, calendar and files.",
    "identity": {"who": "other", "label": "The person it is installed for"},
    "machine": "box-b",
    "holder": "assistant-lead@box-b",
    "maintainer": "assistant-lead@box-b",
    "parent": "assistant",
    "how": "Not installed for agents yet.",
    "actions": [
      {"name": "read and send mail", "type": "act", "route": {"kind": "none"}}
    ],
    "rule": {"text": "To be set when it is installed."},
    "state": "partly",
    "verified_at": "2026-11-17",
    "verified_by": "assistant-lead@box-b"
  },
  {
    "id": "network",
    "name": "Private network",
    "kind": "tool",
    "summary": "The private network between the machines.",
    "identity": {"who": "owner", "label": "The owner's network"},
    "holder_note": "Each machine.",
    "how": "No agent call.",
    "actions": [],
    "rule": {"text": "Do not change its settings: it is the owner's way in."},
    "state": "not_verified"
  },
  {
    "id": "ai-accounts",
    "name": "AI accounts",
    "kind": "account",
    "summary": "The AI subscriptions the agents run on.",
    "identity": {"who": "owner", "label": "The owner's AI subscriptions"},
    "machine": "node-a",
    "holder_note": "The node's accounts service.",
    "how": "Used by launching an agent on that account.",
    "actions": [
      {"name": "launch an agent on an account", "type": "act", "route": {"kind": "tool", "tool": "agent_launch"}}
    ],
    "rule": {"text": "Hard work on the strong model, easy work on the cheap one."},
    "state": "works",
    "state_note": "Code read.",
    "verified_at": "2026-11-17",
    "verified_by": "spec-writer",
    "live": "accounts",
    "page": "/usage"
  },
  {
    "id": "sim-stick",
    "name": "4G stick with the fleet SIM",
    "kind": "device",
    "summary": "The 4G stick that carries the fleet's SMS line.",
    "identity": {"who": "fleet", "label": "The fleet SIM"},
    "machine": "box-c",
    "holder": "phone@box-c",
    "how": "Not used directly.",
    "actions": [],
    "rule": {"text": "Hardware: only the phone shell touches it."},
    "state": "not_verified"
  },
  {
    "id": "virtual-phone",
    "name": "Virtual phone",
    "kind": "device",
    "summary": "A phone running in software, for voice calls.",
    "identity": {"who": "none"},
    "holder_note": "Not built.",
    "actions": [],
    "rule": {"text": "None yet."},
    "state": "planned"
  },
  {
    "id": "disks",
    "name": "Disks",
    "kind": "storage",
    "summary": "The disks of the main machine.",
    "identity": {"who": "none"},
    "machine": "box-b",
    "how": "Files.",
    "actions": [
      {"name": "read and write files", "type": "act", "route": {"kind": "tool", "tool": "the agent's own file tools"}}
    ],
    "rule": {"text": "Nothing may fill a disk."},
    "state": "not_verified"
  }
]`
