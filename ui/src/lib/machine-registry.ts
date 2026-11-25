// The machine registry as a project's "Shells & scripts" list (classic UI profile; the owner, 17 Nov:
// nothing left running that nobody knows about). The daemon's side is internal/fleet/registry.go:
// GET /api/registry?project=<id> answers {entries, orphans}; POST /api/registry/stop and /restart
// take {id}. An entry's `actions` say which buttons the caller may be shown; `command` is the
// executable's name only (arguments may hold secrets and are never listed). Pure: no imports.

export type MachineKind = "tool" | "system_tool" | "shell" | "process";

export interface MachineProcess {
  /** "tool:<id>", "shell:<agent id>" or "pid:<pid>:<start token>" */
  id: string;
  kind: MachineKind | string;
  what: string;
  command: string;
  agent_id?: string;
  agent?: string;
  project_id?: string;
  project?: string;
  /** Unix ms: started, or first seen */
  since: number;
  /** running | detached | orphan | stopped | exited | retrying | stopping */
  state: string;
  pid?: number;
  orphan?: boolean;
  why?: string;
  restarts?: number;
  actions: string[];
}

export interface MachineRegistry {
  entries: MachineProcess[];
  orphans: number;
}

/** what kind of thing it is, in the owner's words */
export function kindWord(kind: string): string {
  switch (kind) {
    case "shell":
      return "Shell";
    case "tool":
      return "Tool";
    case "system_tool":
      return "tmx tool";
    case "process":
      return "Script";
    default:
      return kind || "Process";
  }
}

/** its state, in plain words */
export function stateWord(e: Pick<MachineProcess, "state" | "orphan">): string {
  if (e.orphan || e.state === "orphan") return "Left running";
  switch (e.state) {
    case "running":
      return "Running";
    case "detached":
      return "Running on its own";
    case "stopped":
      return "Stopped";
    case "exited":
      return "Ended";
    case "retrying":
      return "Restarting";
    case "stopping":
      return "Stopping";
    default:
      return e.state || "Unknown";
  }
}

export const isOrphan = (e: Pick<MachineProcess, "state" | "orphan">) => !!e.orphan || e.state === "orphan";
/** still on the machine (anything but stopped or ended) */
export const isAlive = (e: Pick<MachineProcess, "state" | "orphan">) => isOrphan(e) || (e.state !== "stopped" && e.state !== "exited");

/** The list in reading order: what was left running first (flagged), then what runs, then what
 *  ended; inside each, the newest first. Never changes the array it was given. */
export function listedProcesses(entries: MachineProcess[] | null | undefined): MachineProcess[] {
  const rank = (e: MachineProcess) => (isOrphan(e) ? 0 : isAlive(e) ? 1 : 2);
  return [...(entries ?? [])].sort((a, b) => rank(a) - rank(b) || b.since - a.since || a.id.localeCompare(b.id));
}

/** may this button be shown? Only what the daemon offers the caller. */
export const canAct = (e: Pick<MachineProcess, "actions">, action: "stop" | "restart") => (e.actions ?? []).includes(action);

/** the card's one-line count: "3 running · 1 left running" */
export function registrySummary(entries: MachineProcess[] | null | undefined): string {
  const all = entries ?? [];
  if (all.length === 0) return "Nothing is running for this project.";
  const orphans = all.filter(isOrphan).length;
  const running = all.filter((e) => isAlive(e) && !isOrphan(e)).length;
  const ended = all.length - orphans - running;
  const parts: string[] = [];
  if (running > 0) parts.push(`${running} running`);
  if (orphans > 0) parts.push(`${orphans} left running`);
  if (ended > 0) parts.push(`${ended} ended`);
  return parts.join(" · ");
}

/** A daemon without the registry (an older build) answers 404: then there is no list to show. */
export function registryMissing(err: unknown): boolean {
  const status = (err as { status?: number } | null)?.status;
  return status === 404 || status === 405;
}
