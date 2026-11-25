// Resources (#/resources, docs/RESOURCES.md): everything the agents can reach to act in the
// world, one line each. The data is GET /api/toolbox ("toolbox" in code, because "resources"
// already means machine load: lib/resources.ts). Pure, so it is unit tested
// (test/toolbox-format.test.mjs); the API call is lib/toolbox.ts.

export type ResourceKind = "communication" | "tool" | "account" | "device" | "storage";
export type ResourceState = "works" | "partly" | "planned" | "not_verified" | "not_connected";
export type HealthLevel = "good" | "warning" | "down";
export type ActionType = "read" | "send_as_fleet" | "send_as_owner" | "act";

export interface ResourceRoute {
  kind: "tool" | "mail" | "none";
  tool?: string;
  to?: string;
}

export interface ResourceAction {
  name: string;
  type: ActionType;
  route: ResourceRoute;
  note?: string;
}

export interface Resource {
  id: string;
  name: string;
  kind: ResourceKind;
  summary: string;
  identity: { who: "owner" | "fleet" | "other" | "none"; label?: string; person?: string };
  machine?: string;
  holder?: string;
  holder_note?: string;
  node?: string;
  maintainer?: string;
  parent?: string;
  how?: string;
  actions: ResourceAction[];
  rule: { text: string; id?: string };
  state: ResourceState;
  state_note?: string;
  verified_at?: string;
  verified_by?: string;
  health?: { level: HealthLevel; checked_at: number; reason?: string };
  /** names only; stored is absent when the node cannot tell */
  secrets?: { name: string; stored?: boolean }[];
  meter?: { label: string; text: string; used_percent: number };
  live?: string;
  source: "live" | "declared";
  /** a page of this dashboard that shows more (a hash path) */
  page?: string;
}

export interface ResourceList {
  machine: string;
  resources: Resource[];
  updated_at: number;
}

export const KIND_ORDER: ResourceKind[] = ["communication", "tool", "account", "device", "storage"];
export const KIND_LABEL: Record<ResourceKind, string> = {
  communication: "Communication",
  tool: "Tools",
  account: "Accounts",
  device: "Devices",
  storage: "Storage",
};

export const STATE_LABEL: Record<ResourceState, string> = {
  works: "Works",
  partly: "Partly",
  planned: "Planned",
  not_verified: "Not verified",
  not_connected: "Not connected yet",
};

/** The one coloured word of a line, and its tone. A failing health check wins over the state. */
export type StatusTone = "ok" | "warn" | "down" | "idle" | "unknown";
export interface Status {
  word: string;
  tone: StatusTone;
}

export function statusOf(r: Resource): Status {
  if (r.health?.level === "down") return { word: "Down", tone: "down" };
  if (r.state === "not_verified") return { word: STATE_LABEL.not_verified, tone: "unknown" };
  if (r.state === "planned") return { word: STATE_LABEL.planned, tone: "idle" };
  if (r.state === "not_connected") return { word: STATE_LABEL.not_connected, tone: "idle" };
  if (r.health?.level === "warning") return { word: r.state === "works" ? "Works, with a warning" : "Partly", tone: "warn" };
  if (r.state === "partly") return { word: STATE_LABEL.partly, tone: "warn" };
  return { word: STATE_LABEL.works, tone: "ok" };
}

/** Lines that are down or not verified float to the top; nothing else asks for attention. */
export function needsAttention(r: Resource): boolean {
  return r.health?.level === "down" || r.state === "not_verified";
}

function attentionRank(r: Resource): number {
  if (r.health?.level === "down") return 0;
  if (r.state === "not_verified") return 1;
  return 2;
}

export interface ResourceGroup {
  kind: ResourceKind;
  label: string;
  resources: Resource[];
}

/** Grouped by kind in page order; inside a group down first, then not verified, then as listed. */
export function groupResources(list: Resource[]): ResourceGroup[] {
  const out: ResourceGroup[] = [];
  for (const kind of KIND_ORDER) {
    const rows = list
      .map((r, i) => ({ r, i }))
      .filter((x) => x.r.kind === kind)
      .sort((a, b) => attentionRank(a.r) - attentionRank(b.r) || a.i - b.i)
      .map((x) => x.r);
    if (rows.length) out.push({ kind, label: KIND_LABEL[kind], resources: rows });
  }
  return out;
}

export interface ResourceCounts {
  total: number;
  down: number;
  notVerified: number;
}

export function countResources(list: Resource[]): ResourceCounts {
  return {
    total: list.length,
    down: list.filter((r) => r.health?.level === "down").length,
    notVerified: list.filter((r) => r.state === "not_verified" && r.health?.level !== "down").length,
  };
}

/** "12 resources · 1 down · 5 not verified" */
export function countsLine(c: ResourceCounts): string {
  const parts = [`${c.total} ${c.total === 1 ? "resource" : "resources"}`];
  if (c.down) parts.push(`${c.down} down`);
  if (c.notVerified) parts.push(`${c.notVerified} not verified`);
  return parts.join(" · ");
}

/** Who holds it, short, for the line: the holder, else the machine, else the note. */
export function holderShort(r: Resource): string {
  if (r.holder) return r.holder;
  if (r.machine) return r.machine;
  return firstSentence(r.holder_note ?? "");
}

function firstSentence(s: string): string {
  const m = /^(.*?[.!?])(\s|$)/.exec(s.trim());
  return (m ? m[1] : s.trim()).replace(/\.$/, "");
}

/** Who holds it, in full, for the opened line. */
export function holderFull(r: Resource): string {
  return [r.holder, r.holder_note].filter(Boolean).join(". ").replace(/\.\.$/, ".");
}

export const ACTION_LABEL: Record<ActionType, string> = {
  read: "read",
  send_as_fleet: "send as the fleet",
  send_as_owner: "send as the owner",
  act: "act",
};

export function routeText(rt: ResourceRoute): string {
  if (rt.kind === "tool") return `tool ${rt.tool ?? ""}`.trim();
  if (rt.kind === "mail") return `fleet mail to ${rt.to ?? "the holder"}`;
  return "no route yet";
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** "17 Nov" from an ISO date or time; the input unchanged when it is neither. */
export function shortDate(iso: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(iso);
  if (!m) return iso;
  const month = MONTHS[Number(m[2]) - 1];
  return month ? `${Number(m[3])} ${month}` : iso;
}

/** Where the state comes from: "Measured by this node" or "Checked by X on 17 Nov". */
export function verifiedText(r: Resource): string {
  if (r.source === "live" && r.live) return "Measured by this node.";
  const parts: string[] = [];
  if (r.state_note) parts.push(r.state_note.replace(/\.$/, "") + ".");
  if (r.verified_by) parts.push(`Checked by ${r.verified_by}${r.verified_at ? " on " + shortDate(r.verified_at) : ""}.`);
  else if (r.state === "not_verified" && !r.state_note) parts.push("Nobody confirmed it.");
  return parts.join(" ");
}

/** A secret by name with "stored" or "missing"; the name alone when the node cannot tell. */
export function secretText(s: { name: string; stored?: boolean }): string {
  if (s.stored === undefined) return s.name;
  return `${s.name}: ${s.stored ? "stored" : "missing"}`;
}

export function findResource(list: Resource[], id: string | undefined): Resource | undefined {
  return id ? list.find((r) => r.id === id) : undefined;
}

/** Search over what a person would type: name, id, holder, machine, summary, rule. */
export function filterResources(list: Resource[], query: string): Resource[] {
  const q = query.trim().toLowerCase();
  if (!q) return list;
  return list.filter((r) =>
    [r.name, r.id, r.holder, r.machine, r.holder_note, r.summary, r.rule.text, r.how, STATE_LABEL[r.state]].some((v) => v?.toLowerCase().includes(q)),
  );
}

/** Whose face it shows to the world; "" when it shows none. */
export function identityText(r: Resource): string {
  if (r.identity.who === "none") return "";
  if (r.identity.label) return r.identity.label;
  return r.identity.who === "owner" ? "The owner" : r.identity.who === "fleet" ? "The fleet" : "Another person";
}
