// Team status (the Team page, #/team): plain words for each cell. Every state has a word and a
// tone; the page adds an icon per tone, so nothing is told by colour alone. Only type imports,
// so node's test runner loads it.
import type { TeamGroup, TeamNodeRow, TeamPage, TeamReport, Tone } from "./team-types.ts";

export interface Cell {
  tone: Tone;
  /** the word (large) */
  label: string;
  /** a second, smaller line */
  detail?: string;
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

function hm(d: Date): string {
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

function sameDay(a: Date, b: Date): boolean {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
}

/** "14 Nov", or "14 Nov 2025" in another year. */
export function dayWords(at: number, now: number): string {
  const d = new Date(at);
  const s = `${d.getDate()} ${MONTHS[d.getMonth()]}`;
  return d.getFullYear() === new Date(now).getFullYear() ? s : `${s} ${d.getFullYear()}`;
}

export function buildCell(n: TeamNodeRow, floor: string): Cell {
  const r = n.report;
  if (!r) return { tone: "none", label: "Unknown" };
  const detail = r.node.based_on && r.node.based_on !== r.node.build ? `${r.node.build}, based on ${r.node.based_on}` : r.node.build;
  switch (n.build_state) {
    case "current":
      return { tone: "ok", label: "Current", detail };
    case "old":
      return { tone: "warn", label: "Old", detail };
    case "below_floor":
      return { tone: "bad", label: `Below ${floor}`, detail };
    case "fleet":
      // Its own release line: not measured against the upstream floor or the others' releases.
      return { tone: "ok", label: "Fleet build", detail: r.node.build };
  }
  return { tone: "warn", label: "Not a release", detail };
}

/** Last seen: now (a report in the last 45 minutes), today, or offline since a day. */
export function seenCell(n: TeamNodeRow, now: number): Cell {
  const at = n.reported_at ?? 0;
  switch (n.seen) {
    case "now":
      return { tone: "ok", label: "Now" };
    case "today":
      return { tone: "warn", label: "Today", detail: `last at ${hm(new Date(at))}` };
    case "offline": {
      const d = new Date(at);
      return { tone: "bad", label: "Offline", detail: `since ${sameDay(d, new Date(now)) ? hm(d) : dayWords(at, now)}` };
    }
  }
  return { tone: "none", label: "No report" };
}

export function linksCell(n: TeamNodeRow): Cell {
  const l = n.links;
  if (!n.has_report) return { tone: "none", label: "Unknown" };
  if (l.members === 0) return { tone: "none", label: "Alone", detail: "no other members" };
  const reached = l.direct + l.relayed;
  const parts = [];
  if (l.relayed > 0) parts.push(`${l.relayed} through a relay`);
  if (l.offline > 0) parts.push(`${l.offline} not reached`);
  const detail = parts.length ? parts.join(", ") : "all direct";
  return { tone: n.linked ? (l.relayed > 0 ? "warn" : "ok") : "bad", label: `${reached} of ${l.members}`, detail };
}

function channel(state: string | undefined): Cell {
  switch (state) {
    case "connected":
      return { tone: "ok", label: "Connected" };
    case "error":
      return { tone: "bad", label: "Problem" };
    case "not_set":
      return { tone: "bad", label: "Not set up" };
  }
  return { tone: "none", label: "Unknown" };
}

export function phoneCell(r: TeamReport | undefined): Cell {
  const c = channel(r?.phone.state);
  if (r && r.phone.state === "connected") c.detail = r.phone.paired === 1 ? "1 paired" : `${r.phone.paired} paired`;
  return c;
}

export function emailCell(r: TeamReport | undefined): Cell {
  const c = channel(r?.email.state);
  if (r?.email.via) c.detail = r.email.via === "mcp" ? "through an MCP server" : r.email.via === "companion" ? "through Companion" : undefined;
  return c;
}

/** WhatsApp: connected or not, and how many chats agents may write to (a count, never names). */
export function whatsappCell(r: TeamReport | undefined): Cell {
  const c = channel(r?.whatsapp.state);
  const n = r?.whatsapp.authorised_chats ?? 0;
  if (r?.whatsapp.state === "connected") c.detail = n === 0 ? "no chats allowed" : n === 1 ? "1 chat allowed" : `${n} chats allowed`;
  return c;
}

const OLDER: Cell = { tone: "none", label: "Not reported", detail: "an older tmx" };

/** Companion: linked or not. Not part of the contract, so "not linked" is no failure. */
export function companionCell(r: TeamReport | undefined): Cell {
  if (!r?.companion) return OLDER;
  switch (r.companion.state) {
    case "connected":
      return { tone: "ok", label: "Connected" };
    case "error":
      return { tone: "bad", label: "Problem" };
    case "not_set":
      return { tone: "none", label: "Not linked" };
  }
  return { tone: "none", label: "Unknown" };
}

function count(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`;
}

/** Agents: how many are working now, of how many there are; and how many wait for the owner.
 * Counts only. An older tmx reports only how many are running. */
export function agentsCell(r: TeamReport | undefined): Cell {
  if (!r) return { tone: "none", label: "Unknown" };
  const f = r.fleet;
  const waiting = f.waiting_for_owner > 0 ? ` · ${f.waiting_for_owner} waiting for the owner` : "";
  if (typeof f.agents !== "number") return { tone: "none", label: `${f.agents_running} running`, detail: `total not reported${waiting}` };
  if (typeof f.agents_working !== "number") return { tone: "none", label: count(f.agents, "agent", "agents"), detail: `${f.agents_running} running${waiting}` };
  return { tone: f.agents_working > 0 ? "ok" : "none", label: `${f.agents_working} working`, detail: `of ${count(f.agents, "agent", "agents")}${waiting}` };
}

function hoursMinutes(minutes: number): string {
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  return h === 0 ? `${m} min` : m === 0 ? `${h} h` : `${h} h ${m} min`;
}

/** Activity: the time today in which at least one agent worked, and the agents' working time
 * added up. `today` is the reader's local day (YYYY-MM-DD): a count from another day says so. */
export function activityCell(r: TeamReport | undefined, today: string): Cell {
  const a = r?.activity;
  if (!a) return OLDER;
  const when = a.day === today ? "today" : `on ${a.day}`;
  if (a.active_minutes <= 0) return { tone: "none", label: "No work yet", detail: when };
  return { tone: "ok", label: hoursMinutes(a.active_minutes), detail: `${hoursMinutes(a.agent_minutes)} of agent time ${when}` };
}

/** A local day as YYYY-MM-DD (what a report's activity.day is compared with). */
export function localDay(now: number): string {
  const d = new Date(now);
  const two = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${two(d.getMonth() + 1)}-${two(d.getDate())}`;
}

/** The contract column: the checks this computer passes, of those a report can show. */
export function contractCell(n: TeamNodeRow): Cell {
  if (!n.has_report) return { tone: "none", label: "No report", detail: "not shared, or an older tmx" };
  const failed = n.checked - n.passed;
  if (failed === 0) return { tone: "ok", label: `${n.passed} of ${n.checked} pass`, detail: "checked from its report" };
  const which = n.checks.filter((c) => c.result === "fail").map((c) => c.id).join(", ");
  return { tone: "bad", label: `${failed} of ${n.checked} fail`, detail: which };
}

export interface Figure {
  key: "passing" | "builds" | "linked" | "channels";
  value: string;
  label: string;
  detail: string;
  tone: Tone;
}

/** The four numbers above the rows. */
export function figures(g: TeamGroup): Figure[] {
  const s = g.summary;
  const of = (n: number) => `${n} of ${s.people}`;
  const tone = (n: number): Tone => (s.people === 0 ? "none" : n === s.people ? "ok" : n === 0 ? "bad" : "warn");
  const builds = s.builds ?? [];
  return [
    { key: "passing", value: of(s.passing), label: s.passing === 1 ? "person passes" : "people pass", detail: "every check a report can show", tone: tone(s.passing) },
    {
      key: "builds",
      value: String(builds.length),
      label: builds.length === 1 ? "build in use" : "builds in use",
      detail: builds.length ? builds.join(", ") : "no reports yet",
      tone: builds.length <= 1 ? (builds.length ? "ok" : "none") : "warn",
    },
    { key: "linked", value: of(s.linked), label: "linked to everyone online", detail: "directly or through a relay", tone: tone(s.linked) },
    { key: "channels", value: of(s.channels), label: "have all three channels", detail: "phone, e-mail and WhatsApp", tone: tone(s.channels) },
  ];
}

/** Who sees this group's team, in one sentence. */
export function scopeWords(g: TeamGroup): string {
  if (g.admin) return g.open ? "You run this group. Everyone who shares their status sees the whole team." : "You run this group. Members see only their own row.";
  if (g.scope === "all") return `${g.admin_name} runs this group and lets everyone who shares see the whole team.`;
  return `Only you and ${g.admin_name}, who runs this group, see your row.`;
}

/** The opt-in's sentence (one tap). */
export function shareSentence(adminName: string): string {
  return `Share this computer's status with ${adminName}: build, online, links to team members, phone / e-mail / WhatsApp connected yes or no. Never any content.`;
}

/** Groups whose admin's tmx takes reports and whose owner has not decided yet. */
export function unasked(p: TeamPage | null): TeamPage["sharing"] {
  return (p?.sharing ?? []).filter((s) => !s.admin && s.collects && s.share === "unasked");
}

const FIELD: Record<string, string> = {
  build: "Build",
  based_on: "Based on",
  update: "Updates",
  phone: "Phone",
  owner_gate: "Owner gate",
  email: "E-mail",
  whatsapp: "WhatsApp",
  companion: "Companion",
  disk_ok: "Disk space",
  memory_ok: "Memory",
};

const VALUE: Record<string, string> = {
  connected: "connected",
  not_set: "not set up",
  error: "a problem",
  direct: "direct",
  relayed: "through a relay",
  offline: "not reached",
  signed_in: "signed in",
  none: "signed out",
  on_with_pin: "on, with a PIN",
  on: "on",
  off: "off",
  auto: "automatic",
  pinned: "pinned",
  waiting: "waiting to install",
  yes: "fine",
  no: "low",
};

/** One recorded change in plain words; names is peer ID → name for link changes. */
export function changeWords(field: string, oldV: string, newV: string, names: Record<string, string> = {}): string {
  const v = (x: string) => (x === "" ? "nothing" : VALUE[x] ?? x);
  if (field.startsWith("link.")) {
    const who = names[field.slice(5)] ?? "a member";
    return `Link to ${who}: ${v(oldV)} → ${v(newV)}`;
  }
  if (field.startsWith("ai_accounts.")) {
    const h = field.slice(12);
    return `${h.charAt(0).toUpperCase() + h.slice(1)} account: ${v(oldV)} → ${v(newV)}`;
  }
  return `${FIELD[field] ?? field}: ${v(oldV)} → ${v(newV)}`;
}
