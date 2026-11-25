// The Resources page in the mock daemon (GET /api/toolbox): a small made-up estate with
// every state the page shows - works, a warning, down, partly, planned, not verified, a
// meter, secrets by name, a parent. No real person, machine or account.
import { ApiError } from "../transport";
import type { Resource, ResourceList } from "../toolbox-format";
import { NOT_MINE } from "./resources";

const MACHINE = "studio";

function build(now: number): Resource[] {
  const checked = now - 40_000;
  return [
    {
      id: "chat-node",
      name: "WhatsApp, the owner's number, in tmx",
      kind: "communication",
      summary: "Read and send WhatsApp messages on the owner's number from this machine's own linked device.",
      identity: { who: "owner", label: "The owner's WhatsApp number" },
      machine: MACHINE,
      holder_note: "Built into tmx (a linked device, no separate agent).",
      maintainer: "platform-lead@studio",
      how: "Tools whatsapp_chats, whatsapp_read and whatsapp_send.",
      actions: [
        { name: "list chats", type: "read", route: { kind: "tool", tool: "whatsapp_chats" } },
        { name: "read a chat", type: "read", route: { kind: "tool", tool: "whatsapp_read" } },
        { name: "send a message", type: "send_as_owner", route: { kind: "tool", tool: "whatsapp_send" }, note: "Waits for the owner unless the chat is on the allowed list." },
      ],
      rule: { text: "The agent profiles named in Settings. A send waits for the owner unless the chat is on the allowed list." },
      state: "works",
      state_note: "Measured by this node.",
      verified_by: MACHINE,
      health: { level: "good", checked_at: checked, reason: "Linked and connected." },
      live: "whatsapp",
      source: "live",
      page: "/whatsapp",
    },
    {
      id: "fleet-mail",
      name: "Agents' mail alias",
      kind: "communication",
      summary: "The fleet's own mail identity, used to sign agents up on outside platforms.",
      identity: { who: "fleet", label: "The fleet's alias" },
      machine: "garage",
      holder_note: "A mail alias of the owner's workspace; it delivers to a shared mailbox.",
      maintainer: "TBD",
      parent: "assistant",
      how: "No direct call yet. Today only through the assistant's browser.",
      actions: [
        { name: "read and search", type: "read", route: { kind: "none" } },
        { name: "send", type: "send_as_fleet", route: { kind: "none" } },
      ],
      rule: { text: "All agents; it is the fleet's own identity, so sending from it needs no order." },
      state: "partly",
      state_note: "Found by a read-only mail search. No call from tmx yet.",
      verified_at: "2026-11-17",
      verified_by: "platform-lead@studio",
      source: "declared",
    },
    {
      id: "fleet-phone",
      name: "Agent phone number and SMS",
      kind: "communication",
      summary: "The fleet's own mobile number: receive SMS codes and send texts for accounts the agents create.",
      identity: { who: "fleet", label: "The agents' own mobile number" },
      machine: "garage",
      holder: "phone-shell@garage",
      holder_note: "A shell with no model.",
      maintainer: "platform-lead@studio",
      parent: "sim-stick",
      how: "Fleet mail to the phone shell; it mails an agent on each new SMS.",
      actions: [
        { name: "read SMS and codes", type: "read", route: { kind: "mail", to: "phone-shell@garage" } },
        { name: "send an SMS", type: "send_as_fleet", route: { kind: "mail", to: "phone-shell@garage" } },
      ],
      rule: { text: "For accounts the agents create themselves. Never move the codes of the owner's existing accounts to it." },
      state: "partly",
      state_note: "The service answered its health call; no SMS was sent as a test.",
      verified_at: "2026-11-17",
      verified_by: "builder@garage",
      health: { level: "down", checked_at: checked, reason: "Its holder phone-shell@garage is stopped." },
      source: "declared",
    },
    {
      id: "voice",
      name: "Voice calls",
      kind: "communication",
      summary: "Phone calls made by an agent on the owner's behalf.",
      identity: { who: "owner", label: "An agent calling for the owner" },
      parent: "virtual-phone",
      how: "None yet. The plan: a virtual phone with a voice model.",
      actions: [{ name: "make a call", type: "send_as_owner", route: { kind: "none" } }],
      rule: { text: "Planned: one approval per call; say it is an agent calling." },
      state: "planned",
      source: "declared",
    },
    {
      id: "assistant",
      name: "Companion (the assistant app and its signed-in browser)",
      kind: "tool",
      summary: "The owner's assistant app, with a browser signed in as the owner.",
      identity: { who: "owner", label: "The owner's signed-in browser sessions" },
      machine: MACHINE,
      holder: "assistant-lead@studio",
      maintainer: "assistant-lead@studio",
      how: "Tools companion_task and, for the head, companion_message.",
      actions: [
        { name: "hand a task to Companion", type: "act", route: { kind: "tool", tool: "companion_task" } },
        { name: "talk with Companion (the head)", type: "act", route: { kind: "tool", tool: "companion_message" } },
      ],
      rule: { text: "All agents read. Use only the data a job needs." },
      state: "partly",
      verified_at: "2026-11-17",
      verified_by: "assistant-lead@studio",
      health: { level: "good", checked_at: checked, reason: "The key is set; Companion read this node 3 minutes ago." },
      secrets: [
        { name: "Companion agent key", stored: true },
        { name: "Companion link token", stored: false },
      ],
      live: "companion",
      source: "live",
      page: "/companion",
    },
    {
      id: "network",
      name: "Private network",
      kind: "tool",
      summary: "The private network between the machines.",
      identity: { who: "owner", label: "The owner's network" },
      holder_note: "Each machine.",
      how: "The network between the machines; no agent call.",
      actions: [],
      rule: { text: "Do not change its settings: it is the owner's way in." },
      state: "not_verified",
      source: "declared",
    },
    {
      id: "ai-accounts",
      name: "AI accounts",
      kind: "account",
      summary: "The AI subscriptions the agents run on.",
      identity: { who: "owner", label: "The owner's AI subscriptions" },
      machine: MACHINE,
      holder_note: "The accounts service of tmx.",
      how: "Used by launching an agent on that account. The Usage page shows the quota left.",
      actions: [{ name: "launch an agent on an account", type: "act", route: { kind: "tool", tool: "agent_launch" } }],
      rule: { text: "Hard work on the strong model, easy work on the cheap one." },
      state: "works",
      state_note: "Measured by this node.",
      verified_by: MACHINE,
      health: { level: "warning", checked_at: checked, reason: "3 of 3 accounts signed in. One limit is almost used up." },
      meter: { label: "Claude: weekly limit", text: "8% left (the fullest limit)", used_percent: 92 },
      live: "accounts",
      source: "live",
      page: "/usage",
    },
    {
      id: "sim-stick",
      name: "4G stick with the agent SIM",
      kind: "device",
      summary: "The 4G stick that carries the agents' SMS line.",
      identity: { who: "fleet", label: "The agent SIM" },
      machine: "garage",
      holder: "phone-shell@garage",
      how: "Not used directly; it carries the SMS line. Prepaid, SMS only.",
      actions: [],
      rule: { text: "Hardware: only the phone shell touches it." },
      state: "not_verified",
      source: "declared",
    },
    {
      id: "virtual-phone",
      name: "Virtual Android",
      kind: "device",
      summary: "An Android running in software, for voice calls with a voice model.",
      identity: { who: "none" },
      holder_note: "Not built.",
      actions: [],
      rule: { text: "None yet." },
      state: "planned",
      source: "declared",
    },
    {
      id: "work-drive",
      name: "Work drive",
      kind: "storage",
      summary: "The shared work drive: agents list, read, upload, download and organise files there.",
      identity: { who: "owner", label: "The owner's work drive" },
      holder_note: "No agent or shell holds a connection yet.",
      maintainer: "platform-lead@studio",
      how: "No call for agents yet.",
      actions: [{ name: "list and read files", type: "read", route: { kind: "none" } }],
      rule: { text: "All agents, for work files." },
      state: "not_connected",
      state_note: "Ordered, never finished before.",
      verified_at: "2026-11-17",
      verified_by: "platform-lead@studio",
      source: "declared",
    },
    {
      id: "disks",
      name: "Disks",
      kind: "storage",
      summary: "The disks of the main machine.",
      identity: { who: "none" },
      machine: MACHINE,
      how: "Files. One disk for current work, one for long-term.",
      actions: [{ name: "read and write files", type: "act", route: { kind: "tool", tool: "the agent's own file tools" } }],
      rule: { text: "Copy, check, then delete. Nothing may fill a disk." },
      state: "not_verified",
      source: "declared",
    },
  ];
}

/** Answers GET /api/toolbox and /api/toolbox/{id}; NOT_MINE for everything else. */
export function mockToolbox(method: string, parts: string[], q: URLSearchParams, scenario: string): unknown {
  if (parts[0] !== "toolbox") return NOT_MINE;
  if (method !== "GET") throw new ApiError(405, "The resources are read only.");
  if (scenario === "resources-down") throw new ApiError(500, "the resources could not be read");
  const now = Date.now();
  const all = scenario === "resources-empty" ? [] : build(now);
  if (parts[1]) {
    const one = all.find((r) => r.id === parts[1]);
    if (!one) throw new ApiError(404, "no such resource; GET /api/toolbox lists them");
    return one;
  }
  const kind = q.get("kind");
  const state = q.get("state");
  const list: ResourceList = {
    machine: MACHINE,
    updated_at: now,
    resources: all.filter((r) => (!kind || r.kind === kind) && (!state || (state === "down" ? r.health?.level === "down" : r.state === state))),
  };
  return list;
}
