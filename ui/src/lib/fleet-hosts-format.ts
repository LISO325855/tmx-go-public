// Pure text/decision logic for the fleet-hosts strip (WP4, lib/fleet-hosts.ts). No runtime
// project imports, so this stays trivially unit-testable (test/fleet-hosts.test.mjs) without a
// daemon connection. Every string here is composed from another install's own data: callers
// must render it as plain text only, never as HTML or instructions.
import type { HostSummary } from "./fleet-hosts";

/** A failed request on these statuses just means the strip has nothing to show: an agent
 * caller (403, owner-only), or an older daemon without the route (404/501). Anything else
 * keeps the last known hosts. */
export function hidesOnStatus(status: number | undefined): boolean {
  return status === 403 || status === 404 || status === 501;
}

export function fleetHostsResult(hosts: HostSummary[] | null | undefined): { loaded: true; hosts: HostSummary[] } {
  return { loaded: true, hosts: hosts ?? [] };
}

/** "testbox · 3 agents · 1 running · 2 need you" - machine and needs-you are left out when empty. */
export function hostStatusLine(h: Pick<HostSummary, "machine" | "agents" | "running" | "needs_you">): string {
  const bits: string[] = [];
  if (h.machine) bits.push(h.machine);
  bits.push(`${h.agents} ${h.agents === 1 ? "agent" : "agents"}`);
  bits.push(`${h.running} running`);
  if (h.needs_you > 0) bits.push(`${h.needs_you} need you`);
  return bits.join(" · ");
}

/** The head's latest activity line, labelled with its name - or null when there is none yet. */
export function hostHeadLine(h: Pick<HostSummary, "head_name" | "head_line">): string | null {
  if (!h.head_line) return null;
  return h.head_name ? `${h.head_name}: ${h.head_line}` : h.head_line;
}

// ---- The Machines bar (components/machines-bar.tsx): the owner's own machines as one fleet.

/** online = answered its last poll; offline = configured and paired, but not answering;
 * unpaired = no pair token with it yet, so nothing can be read from it at all. */
export type MachineTone = "online" | "offline" | "unpaired";

export function machineTone(h: Pick<HostSummary, "kind" | "reachable" | "paired">): MachineTone {
  if (h.reachable) return "online";
  return h.kind === "peer" && !h.paired ? "unpaired" : "offline";
}

/** This machine first, then the others in the order the daemon lists them. A row named like
 * this machine is dropped from the others: it is never shown twice. */
export function machineRows(self: HostSummary | null | undefined, hosts: HostSummary[]): HostSummary[] {
  if (!self) return hosts;
  return [self, ...hosts.filter((h) => !(h.kind === "peer" && h.name === self.name))];
}

/** The bar only earns its row when there is more than this machine to show. */
export function showsMachinesBar(self: HostSummary | null | undefined, hosts: HostSummary[]): boolean {
  return hosts.length > 0 && machineRows(self, hosts).length > 1;
}

/** "2/7": agents running of all its agents. */
export function machineAgents(h: Pick<HostSummary, "agents" | "running">): string {
  return `${h.running}/${h.agents}`;
}

function wholePct(n: number): string {
  return n > 0 && n < 1 ? "<1%" : `${Math.round(n)}%`;
}

function gb(n: number): string {
  return n >= 100 ? `${Math.round(n)}` : n.toFixed(1);
}

function ago(ms: number, now: number): string {
  const m = Math.floor(Math.max(0, now - ms) / 60_000);
  if (m < 1) return "less than a minute ago";
  if (m < 60) return `${m} min ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h} h ago`;
  return `${Math.floor(h / 24)} d ago`;
}

/** "12%" - or null when that machine sent no reading (never a made-up 0 %). */
export function machineCpu(h: Pick<HostSummary, "measured" | "cpu" | "reachable">): string | null {
  return h.reachable && h.measured ? wholePct(h.cpu ?? 0) : null;
}

/** The lines of a machine's hover card, top to bottom: who it is and whether it answers, its
 * agents, then each reading it actually sent. Plain text, one line each. */
export function machineDetail(h: HostSummary, now: number): string[] {
  const tone = machineTone(h);
  const head = [h.kind === "self" ? `${h.name} (this machine)` : h.name];
  if (h.version) head.push(`tmx ${h.version}`);
  const lines = [head.join(" · ")];
  if (tone === "unpaired") {
    lines.push("Not paired with this machine yet: nothing can be read from it.");
    return lines;
  }
  if (tone === "offline") {
    lines.push(h.seen ? `Not reachable · last seen ${ago(h.seen, now)}` : "Not reachable · never seen");
    if (h.error) lines.push(h.error);
    return lines;
  }
  const agents = [`${h.agents} ${h.agents === 1 ? "agent" : "agents"}`, `${h.running} running`];
  if (h.paused > 0) agents.push(`${h.paused} paused`);
  if (h.needs_you > 0) agents.push(`${h.needs_you} need you`);
  lines.push(agents.join(" · "));
  if (h.measured) {
    lines.push(`CPU ${wholePct(h.cpu ?? 0)}`);
    if (h.mem_total_mb) lines.push(`Memory ${gb((h.mem_free_mb ?? 0) / 1024)} GB free of ${gb(h.mem_total_mb / 1024)} GB`);
  }
  if (h.disk_total_gb) lines.push(`Disk ${gb(h.disk_free_gb ?? 0)} GB free of ${gb(h.disk_total_gb)} GB`);
  if (h.disk_busy) lines.push("Disk busy: work is being held back");
  if (!h.measured && !h.disk_total_gb) lines.push("No machine reading from it yet");
  return lines;
}
