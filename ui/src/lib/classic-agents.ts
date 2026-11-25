// The agent list the Python way (classic UI profile; parity C1, C2, C4; architect, 17 Nov 15:05:
// "agent list: all except stopped"). Pure helpers for components/classic/agent-list.tsx and
// hooks/use-visible-agents.ts.
//
// - Which agents show: every agent except shelved ones; stopped agents hide behind one line,
//   "N stopped agents hidden · show", and that choice is remembered on the device. A quiet
//   agent and one that failed stay visible (the default dashboard's 2-hour rule hid both).
// - A row: face, name, the state in words ("needs you", "asks you"), a second line with the
//   role and what it is doing, a harness tag.
// - The search box above the list filters it: agents, projects, machines.

interface AgentLike {
  id: string;
  name: string;
  state: string;
  level: string;
  harness?: string;
  role?: string | null;
  profile?: string | null;
  project_id?: string | null;
  shelved_at?: number | null;
}

/** Stopped by someone or ended by itself: not running and not asking for anything. */
export function isStoppedState(state: string): boolean {
  return state === "stopped" || state === "exited";
}

/** Classic: every agent except shelved ones; stopped ones only when asked for. The head always shows. */
export function classicAgentVisible(a: Pick<AgentLike, "state" | "level" | "shelved_at">, showStopped: boolean): boolean {
  if (a.level === "head") return true;
  if (a.shelved_at) return false;
  return showStopped || !isStoppedState(a.state);
}

/** How many agents the "stopped agents hidden" line stands for (never the head, never shelved ones). */
export function stoppedAgentCount(agents: Iterable<Pick<AgentLike, "state" | "level" | "shelved_at">>): number {
  let n = 0;
  for (const a of agents) if (a.level !== "head" && !a.shelved_at && isStoppedState(a.state)) n++;
  return n;
}

/** Python's line under the list; "" when there is nothing to say. */
export function stoppedLine(count: number, showStopped: boolean): { text: string; action: string } | null {
  if (count <= 0) return null;
  const agents = count === 1 ? "1 stopped agent" : `${count} stopped agents`;
  return showStopped ? { text: `${agents} shown`, action: "hide" } : { text: `${agents} hidden`, action: "show" };
}

const SHOW_STOPPED_KEY = "tmx.showStoppedAgents";

export function savedShowStopped(): boolean {
  try {
    return localStorage.getItem(SHOW_STOPPED_KEY) === "1";
  } catch {
    return false;
  }
}

export function saveShowStopped(on: boolean) {
  try {
    if (on) localStorage.setItem(SHOW_STOPPED_KEY, "1");
    else localStorage.removeItem(SHOW_STOPPED_KEY);
  } catch {
    // private mode: the choice holds until the page goes
  }
}

/**
 * The state in Python's words. waiting = the agent waits for the owner: "asks you" when it put
 * a question, "needs you" when it wants a permission or anything else.
 */
export function stateWords(a: Pick<AgentLike, "state" | "shelved_at">, asks = false): string {
  if (a.shelved_at) return "shelved";
  switch (a.state) {
    case "waiting":
      return asks ? "asks you" : "needs you";
    case "working":
      return "working";
    case "starting":
      return "starting";
    case "idle":
      return "idle";
    case "queued":
      return "queued";
    case "paused":
      return "paused";
    case "error":
      return "failed";
    case "stopped":
      return "stopped";
    case "exited":
      return "ended";
  }
  return a.state;
}

/** The row's second line: the role (or the level in words), then what the agent is doing. */
export function agentSecondLine(a: Pick<AgentLike, "role" | "level" | "profile">, doing: string | undefined | null): string {
  const role = (a.role ?? "").trim() || (a.level === "lead" ? "lead" : a.profile && a.profile !== a.level ? a.profile : a.level === "head" ? "architect" : "worker");
  return [role, (doing ?? "").trim()].filter(Boolean).join(" · ");
}

function has(text: string | null | undefined, q: string): boolean {
  return !!text && text.toLowerCase().includes(q);
}

/**
 * The search box: an agent matches by its name, role, profile or harness, by its project's name,
 * or by the machine's name (which then shows everything on it). Empty = everything.
 */
export function agentMatches(a: AgentLike, query: string, projectName?: string, machine?: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return has(a.name, q) || has(a.role, q) || has(a.profile, q) || has(a.harness, q) || has(projectName, q) || has(machine, q);
}

/** A project shows when its name matches or one of its agents does (then only those agents). */
export function filterProjectAgents<T extends AgentLike>(agents: readonly T[], query: string, projectName: string, machine?: string): { show: boolean; agents: T[] } {
  const q = query.trim().toLowerCase();
  if (!q) return { show: true, agents: [...agents] };
  if (has(projectName, q) || has(machine, q)) return { show: true, agents: [...agents] };
  const hit = agents.filter((a) => agentMatches(a, q));
  return { show: hit.length > 0, agents: hit };
}

// ---- the projects list (the owner, 17 Nov 19:03: "the projects list with ~40 projects is messy…
// all of this is about organizing information, filtering it down to the human brain's
// capability") ----
// Collapsed by default, one line per project. A project opens by itself only while one of its
// agents is busy (working, starting, or waiting for the owner). One toggle: "collapse all" /
// "expand active". Projects with no agent to show hide behind "show all (N)".

/** The list's two modes: "active" opens the projects with a busy agent; "collapsed" opens none. */
export type ProjectsMode = "active" | "collapsed";

/** An agent that is doing something, or waiting for the owner: its project is worth opening. */
export function isBusyState(state: string): boolean {
  return state === "working" || state === "starting" || state === "waiting";
}

/**
 * Whether a project's agents show. A search opens what it found; the chevron (override) wins
 * for that project until the mode is switched; the project of the page being looked at is open
 * so the place is not lost; otherwise only a project with a busy agent, and none when collapsed.
 */
export function projectOpen(o: { mode: ProjectsMode; busy: boolean; current: boolean; searching: boolean; override?: boolean }): boolean {
  if (o.searching) return true;
  if (o.override !== undefined) return o.override;
  if (o.mode === "collapsed") return false;
  return o.busy || o.current;
}

/** Which projects the list shows: those with an agent to show, plus the one being looked at; all when asked or searching. */
export function projectListed(o: { members: number; current: boolean; showEmpty: boolean; searching: boolean }): boolean {
  return o.showEmpty || o.searching || o.members > 0 || o.current;
}

/** The toggle's words: what a click does. */
export function projectsModeAction(mode: ProjectsMode): string {
  return mode === "active" ? "collapse all" : "expand active";
}

/** The line under the list for the hidden empty projects; null when there are none. */
export function emptyProjectsLine(hidden: number, showEmpty: boolean): string | null {
  if (hidden <= 0) return null;
  return showEmpty ? `hide empty (${hidden})` : `show all (${hidden})`;
}

const PROJECTS_MODE_KEY = "tmx.sidebar.projectsMode";

export function savedProjectsMode(): ProjectsMode {
  try {
    return localStorage.getItem(PROJECTS_MODE_KEY) === "collapsed" ? "collapsed" : "active";
  } catch {
    return "active";
  }
}

export function saveProjectsMode(mode: ProjectsMode) {
  try {
    if (mode === "collapsed") localStorage.setItem(PROJECTS_MODE_KEY, "collapsed");
    else localStorage.removeItem(PROJECTS_MODE_KEY);
  } catch {
    // private mode: the choice holds until the page goes
  }
}
