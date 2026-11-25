import test from "node:test";
import assert from "node:assert/strict";
import {
  buildCell,
  changeWords,
  contractCell,
  dayWords,
  emailCell,
  figures,
  linksCell,
  phoneCell,
  scopeWords,
  seenCell,
  shareSentence,
  unasked,
  whatsappCell,
} from "../src/lib/team-format.ts";

const now = new Date(2026, 10, 17, 15, 0).getTime();

const report = (over = {}) => ({
  report: 1,
  contract: 1,
  sent_at: now,
  node: { label: "studio-mac", os: "darwin", build: "v0.1.18", based_on: "v0.1.18", personal: true, update: "auto", started_at: 0 },
  fleet: { agents_running: 1, waiting_for_owner: 0, ai_accounts: {} },
  links: [],
  phone: { state: "connected", paired: 2, notifications: true, owner_gate: "on" },
  whatsapp: { state: "connected", authorised_chats: 1 },
  email: { state: "connected", via: "mcp" },
  health: { disk_ok: true, memory_ok: true },
  ...over,
});

const row = (over = {}) => ({
  peer_id: "p1",
  has_report: true,
  label: "studio-mac",
  build_state: "current",
  seen: "now",
  reported_at: now,
  links: { direct: 3, relayed: 1, offline: 0, members: 4 },
  linked: true,
  channels: 3,
  report: report(),
  checks: [
    { id: "C2", title: "Same peer ID", result: "pass" },
    { id: "C3", title: "Recent", result: "fail", detail: "older than 45 minutes" },
    { id: "C4", title: "Channels", result: "pass" },
    { id: "C6", title: "Floor", result: "pass" },
  ],
  passed: 3,
  checked: 4,
  ...over,
});

test("build: current, old, below the floor, a team's own build", () => {
  assert.deepEqual(buildCell(row(), "v0.1.14"), { tone: "ok", label: "Current", detail: "v0.1.18" });
  assert.equal(buildCell(row({ build_state: "old" }), "v0.1.14").label, "Old");
  assert.deepEqual(buildCell(row({ build_state: "below_floor" }), "v0.1.14").label, "Below v0.1.14");
  const own = row({ report: report({ node: { ...report().node, build: "v0.1.18-team.2", based_on: "v0.1.18" } }) });
  assert.equal(buildCell(own, "v0.1.14").detail, "v0.1.18-team.2, based on v0.1.18");
  assert.equal(buildCell(row({ has_report: false, report: undefined }), "v0.1.14").tone, "none");
});

test("last seen: now, today, offline since a day", () => {
  assert.equal(seenCell(row(), now).label, "Now");
  const morning = new Date(2026, 10, 17, 9, 5).getTime();
  assert.deepEqual(seenCell(row({ seen: "today", reported_at: morning }), now), { tone: "warn", label: "Today", detail: "last at 09:05" });
  const before = new Date(2026, 10, 14, 18, 0).getTime();
  assert.deepEqual(seenCell(row({ seen: "offline", reported_at: before }), now), { tone: "bad", label: "Offline", detail: "since 14 Nov" });
  assert.equal(seenCell(row({ seen: "never", has_report: false }), now).label, "No report");
  assert.equal(dayWords(new Date(2025, 0, 2).getTime(), now), "2 Jan 2025");
});

test("connections, channels and the contract, in words", () => {
  assert.deepEqual(linksCell(row()), { tone: "warn", label: "4 of 4", detail: "1 through a relay" });
  assert.deepEqual(linksCell(row({ links: { direct: 2, relayed: 0, offline: 2, members: 4 }, linked: false })), { tone: "bad", label: "2 of 4", detail: "2 not reached" });
  assert.deepEqual(phoneCell(report()), { tone: "ok", label: "Connected", detail: "2 paired" });
  assert.equal(phoneCell(report({ phone: { state: "not_set", paired: 0 } })).label, "Not set up");
  assert.deepEqual(emailCell(report()), { tone: "ok", label: "Connected", detail: "through an MCP server" });
  assert.equal(emailCell(report({ email: { state: "error", via: "mcp" } })).label, "Problem");
  assert.deepEqual(whatsappCell(report()), { tone: "ok", label: "Connected", detail: "1 chat allowed" });
  assert.deepEqual(contractCell(row()), { tone: "bad", label: "1 of 4 fail", detail: "C3" });
  assert.equal(contractCell(row({ passed: 4, checks: [] })).label, "4 of 4 pass");
  assert.equal(contractCell(row({ has_report: false })).label, "No report");
});

test("the four numbers and who sees the team", () => {
  const g = {
    chat_id: "g",
    name: "Studio",
    admin_name: "Sam",
    admin: true,
    open: false,
    scope: "all",
    reports: 3,
    people: [],
    summary: { people: 4, passing: 2, builds: ["v0.1.16", "v0.1.18"], linked: 4, channels: 0 },
  };
  const f = figures(g);
  assert.deepEqual(
    f.map((x) => [x.key, x.value, x.label, x.tone]),
    [
      ["passing", "2 of 4", "people pass", "warn"],
      ["builds", "2", "builds in use", "warn"],
      ["linked", "4 of 4", "linked to everyone online", "ok"],
      ["channels", "0 of 4", "have all three channels", "bad"],
    ],
  );
  assert.equal(f[1].detail, "v0.1.16, v0.1.18");
  assert.match(scopeWords(g), /Members see only their own row/);
  assert.match(scopeWords({ ...g, admin: false, scope: "own", admin_name: "Alex" }), /Only you and Alex/);
  assert.equal(
    shareSentence("Alex"),
    "Share this computer's status with Alex: build, online, links to team members, phone / e-mail / WhatsApp connected yes or no. Never any content.",
  );
  const page = { sharing: [{ chat_id: "a", admin: false, collects: true, share: "unasked" }, { chat_id: "b", admin: false, collects: true, share: "on" }, { chat_id: "c", admin: true, collects: true, share: "unasked" }] };
  assert.deepEqual(unasked(page).map((s) => s.chat_id), ["a"]);
});

test("recorded changes in plain words", () => {
  assert.equal(changeWords("whatsapp", "connected", "error"), "WhatsApp: connected → a problem");
  assert.equal(changeWords("link.p2", "direct", "relayed", { p2: "Alex" }), "Link to Alex: direct → through a relay");
  assert.equal(changeWords("ai_accounts.claude", "signed_in", "none"), "Claude account: signed in → signed out");
  assert.equal(changeWords("build", "v0.1.17", "v0.1.18"), "Build: v0.1.17 → v0.1.18");
});

// ---- The fleet build's columns: Companion, Agents, Activity. Counts and states only.
test("companionCell: linked, not linked (no failure), a problem, or an older tmx", async () => {
  const { companionCell } = await import("../src/lib/team-format.ts");
  assert.deepEqual(companionCell({ companion: { state: "connected" } }), { tone: "ok", label: "Connected" });
  assert.deepEqual(companionCell({ companion: { state: "not_set" } }), { tone: "none", label: "Not linked" });
  assert.deepEqual(companionCell({ companion: { state: "error" } }), { tone: "bad", label: "Problem" });
  assert.deepEqual(companionCell({ companion: { state: "???" } }), { tone: "none", label: "Unknown" });
  assert.deepEqual(companionCell({}), { tone: "none", label: "Not reported", detail: "an older tmx" });
  assert.deepEqual(companionCell(undefined), { tone: "none", label: "Not reported", detail: "an older tmx" });
});

test("agentsCell: working of all, waiting for the owner; an older tmx says only how many run", async () => {
  const { agentsCell } = await import("../src/lib/team-format.ts");
  const fleet = (f) => ({ fleet: { agents_running: 3, waiting_for_owner: 0, ai_accounts: null, ...f } });
  assert.deepEqual(agentsCell(fleet({ agents: 12, agents_working: 4 })), { tone: "ok", label: "4 working", detail: "of 12 agents" });
  // 0 working is a reading, not "not reported".
  assert.deepEqual(agentsCell(fleet({ agents: 1, agents_working: 0, waiting_for_owner: 2 })), { tone: "none", label: "0 working", detail: "of 1 agent · 2 waiting for the owner" });
  assert.deepEqual(agentsCell(fleet({})), { tone: "none", label: "3 running", detail: "total not reported" });
  assert.deepEqual(agentsCell(fleet({ agents: 5 })), { tone: "none", label: "5 agents", detail: "3 running" });
  assert.deepEqual(agentsCell(undefined), { tone: "none", label: "Unknown" });
});

test("activityCell and localDay: time with agents working today; another day is named", async () => {
  const { activityCell, localDay } = await import("../src/lib/team-format.ts");
  const act = (a) => ({ activity: { day: "2026-11-17", active_minutes: 0, agent_minutes: 0, ...a } });
  assert.deepEqual(activityCell(act({ active_minutes: 310, agent_minutes: 1240 }), "2026-11-17"), { tone: "ok", label: "5 h 10 min", detail: "20 h 40 min of agent time today" });
  assert.deepEqual(activityCell(act({ active_minutes: 45, agent_minutes: 60 }), "2026-11-17"), { tone: "ok", label: "45 min", detail: "1 h of agent time today" });
  assert.deepEqual(activityCell(act({}), "2026-11-17"), { tone: "none", label: "No work yet", detail: "today" });
  assert.deepEqual(activityCell(act({ active_minutes: 120, agent_minutes: 120 }), "2026-11-18"), { tone: "ok", label: "2 h", detail: "2 h of agent time on 2026-11-17" });
  assert.deepEqual(activityCell({}, "2026-11-17"), { tone: "none", label: "Not reported", detail: "an older tmx" });
  assert.equal(localDay(new Date(2026, 10, 7, 23, 59).getTime()), "2026-11-07");
});

test("buildCell: a fleet build is its own line, not 'not a release'", () => {
  const n = { has_report: true, build_state: "fleet", report: { node: { build: "fleet-v1.4.0", based_on: "fleet" } } };
  assert.deepEqual(buildCell(n, "v0.1.14"), { tone: "ok", label: "Fleet build", detail: "fleet-v1.4.0" });
});
