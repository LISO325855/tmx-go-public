// Pure logic for a machine's page (pages/machine.tsx): the agents of one of the owner's other
// machines, read from the fleet roster (GET /api/fleet), and the requests that work them through
// the relay (POST /api/remote). No runtime project imports, so it stays unit-testable without a
// daemon (test/machines.test.mjs). Everything in a roster row is another machine's own data:
// callers render it as plain text only.

/** One agent of the roster, as GET /api/fleet lists it (the Python daemons' shape). */
export interface RosterSession {
  name: string;
  host: string;
  state: string;
  project?: string;
  role?: string;
  tier?: string;
  cwd?: string;
  adapter?: string;
  queued_messages?: number;
}

/** A dot colour for a roster state. Go machines answer the Python words for waiting ("dialog")
 * and stopped ("exited"); Python machines have a few words of their own. An unknown word reads
 * as idle: never as working or as needing the owner. */
export type DotState = "working" | "starting" | "waiting" | "paused" | "queued" | "idle" | "stopped" | "error";

export function dotState(state: string): DotState {
  switch (state) {
    case "working":
    case "busy":
      return "working";
    case "starting":
      return "starting";
    case "dialog":
    case "waiting":
    case "approval":
    case "question":
      return "waiting";
    case "paused":
    case "braked":
      return "paused";
    case "queued":
      return "queued";
    case "exited":
    case "stopped":
      return "stopped";
    case "error":
      return "error";
    default:
      return "idle";
  }
}

/** Not running: it can be started, not stopped. */
export function resting(state: string): boolean {
  const s = dotState(state);
  return s === "stopped" || s === "error";
}

/** The agents of one machine: running ones first, then by name. Host names compare without case. */
export function machineSessions(sessions: RosterSession[] | null | undefined, host: string): RosterSession[] {
  const h = host.toLowerCase();
  return (sessions ?? [])
    .filter((s) => (s.host ?? "").toLowerCase() === h)
    .sort((a, b) => Number(resting(a.state)) - Number(resting(b.state)) || a.name.localeCompare(b.name));
}

export interface ProjectGroup {
  /** "" = agents without a project. */
  project: string;
  agents: RosterSession[];
}

/** Grouped by project name, alphabetically; agents without a project come last. */
export function byProject(agents: RosterSession[]): ProjectGroup[] {
  const groups = new Map<string, RosterSession[]>();
  for (const a of agents) {
    const key = a.project ?? "";
    const list = groups.get(key);
    if (list) list.push(a);
    else groups.set(key, [a]);
  }
  return [...groups.entries()]
    .sort(([a], [b]) => (a === "" ? 1 : b === "" ? -1 : a.localeCompare(b)))
    .map(([project, list]) => ({ project, agents: list }));
}

/** "31 agents · 9 running · 2 need you" for the page's header. */
export function sessionsLine(agents: RosterSession[]): string {
  const running = agents.filter((a) => !resting(a.state)).length;
  const needs = agents.filter((a) => dotState(a.state) === "waiting").length;
  const bits = [`${agents.length} ${agents.length === 1 ? "agent" : "agents"}`, `${running} running`];
  if (needs > 0) bits.push(`${needs} need you`);
  return bits.join(" · ");
}

// ---- The relay. Its allow-list (internal/api/remote.go) admits these session routes only, and
// only for the owner; a name outside its pattern is never sent.

export type SessionAction = "stop" | "start" | "restart" | "keys" | "screen";

const NAME = /^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}$/;

export interface RemoteRequest {
  host: string;
  method: "GET" | "POST";
  path: string;
  body?: unknown;
}

/** The relay request for one action on one agent of another machine; null when the agent's name
 * could not be part of a path the relay admits. */
export function sessionRequest(host: string, name: string, action: SessionAction, arg?: string): RemoteRequest | null {
  if (!NAME.test(name) || !host) return null;
  const base = `/api/sessions/${name}/`;
  switch (action) {
    case "screen":
      return { host, method: "GET", path: `${base}screen?history=200` };
    case "keys":
      return arg ? { host, method: "POST", path: `${base}keys`, body: { keys: arg } } : null;
    default:
      return { host, method: "POST", path: base + action };
  }
}

export interface LaunchForm {
  name: string;
  /** The folder on that machine the agent works in. */
  cwd: string;
  role: string;
  /** What it should do: its first turn. */
  task: string;
}

/** Why the form cannot be sent yet, in plain words; null when it can. A Python machine needs
 * the folder (it has no default workspace for a new name). */
export function launchProblem(f: LaunchForm, goDaemon: boolean): string | null {
  const name = f.name.trim();
  if (!name) return "Give the agent a name.";
  if (!/^[a-z0-9][a-z0-9-]{0,39}$/.test(name)) return "A name is lowercase letters, digits and dashes, up to 40.";
  if (!goDaemon && !f.cwd.trim()) return "Give the folder on that machine it should work in.";
  return null;
}

/** The launch request. A Go machine takes Go's own launch body (the task is the brief, so it is
 * also the first turn); a Python machine takes Python's (adapter, prompt). Empty fields are left
 * out, so that machine's own defaults apply. */
export function launchRequest(host: string, f: LaunchForm, goDaemon: boolean): RemoteRequest {
  const body: Record<string, string> = { name: f.name.trim() };
  if (f.cwd.trim()) body.cwd = f.cwd.trim();
  if (f.role.trim()) body.role = f.role.trim();
  if (goDaemon) {
    if (f.task.trim()) body.brief = f.task.trim();
  } else {
    body.adapter = "claude";
    if (f.task.trim()) body.prompt = f.task.trim();
  }
  return { host, method: "POST", path: "/api/sessions", body };
}

/** The keys offered under a remote screen: enough to answer a prompt, nothing that types text. */
export const QUICK_KEYS: { label: string; keys: string }[] = [
  { label: "Enter", keys: "enter" },
  { label: "Esc", keys: "esc" },
  { label: "1", keys: "1" },
  { label: "2", keys: "2" },
  { label: "3", keys: "3" },
  { label: "y", keys: "y" },
  { label: "n", keys: "n" },
];
