// The classic UI profile on a phone (hooks/use-phone-classic.ts): the Python dashboard's mobile
// app, one to one. A bottom bar with Home, Projects, Agents and Your turn instead of a top bar and
// a menu; nothing fixed above a page; one card per screen on Your turn; Python's own sizes.
// Pure, so node can test it. The words and rules it shares with the computer's classic screens
// come from lib/classic-agents.ts and lib/ui-profile.ts; the components are in components/phone/,
// pages/phone-home.tsx and pages/phone-lists.tsx.
import type { Agent, AgentState, Project } from "./types";
import { agentMatches, agentSecondLine, stateWords } from "./classic-agents.ts";
import { agentLine } from "./ui-profile.ts";

/** Python's own sizes, in px (its phone app is laid out in px at a 14 px body). */
export const PHONE_PX = { bottomBar: 76, titleRow: 66, agentRow: 66, plainRow: 48, choice: 48, target: 44, smallestText: 12, column: 480 } as const;

// ---------------------------------------------------------------------------------------------
// The bottom bar and what stands above a page.

export type PhoneTab = "home" | "projects" | "agents" | "turn";

/** The bottom bar, in the Python dashboard's order. */
export const PHONE_TABS: { id: PhoneTab; label: string; path: string }[] = [
  { id: "home", label: "Home", path: "/" },
  { id: "projects", label: "Projects", path: "/projects" },
  { id: "agents", label: "Agents", path: "/agents" },
  { id: "turn", label: "Your turn", path: "/inbox" },
];

/** Which tab a page belongs to (a project's page stays under Projects, an agent's under Agents);
 * null for the pages reached from Home's rows. */
export function phoneTabOf(routeName: string): PhoneTab | null {
  switch (routeName) {
    case "overview":
      return "home";
    case "projects":
    case "project":
      return "projects";
    case "agents":
    case "agent":
      return "agents";
    case "inbox":
      return "turn";
    default:
      return null;
  }
}

/** The count on Your turn: "9+" from ten, so the badge keeps its size. */
export function tabCount(n: number): string {
  return n > 9 ? "9+" : n > 0 ? String(n) : "";
}

/** The pages the bottom bar opens: they start with their headline, without a title row. */
export function isTabRoot(routeName: string): boolean {
  return routeName === "overview" || routeName === "projects" || routeName === "agents" || routeName === "inbox";
}

/** Where the back arrow of a page's title row leads: an agent back to Agents, a project to
 * Projects, a page inside a section to that section, everything else to Home. */
export function phoneBackPath(route: { name: string; id?: string; jid?: string }): string {
  switch (route.name) {
    case "agent":
      return "/agents";
    case "project":
      return "/projects";
    case "chat":
      return "/chats";
    case "profile":
      return "/settings";
    case "recordings":
      return route.id ? "/recordings" : "/";
    case "whatsapp":
      return route.jid ? "/whatsapp" : "/";
    default:
      return "/";
  }
}

// ---------------------------------------------------------------------------------------------
// An agent in words.

const PAUSE_WORDS: Record<string, string> = { write_budget: "write budget", memory: "memory cap", disk: "disk saturated" };

function ordinal(n: number): string {
  const s = n % 100 >= 11 && n % 100 <= 13 ? "th" : ({ 1: "st", 2: "nd", 3: "rd" } as Record<number, string>)[n % 10] ?? "th";
  return `${n}${s}`;
}

type StateFields = Pick<Agent, "state" | "shelved_at" | "queue_pos" | "paused_reason">;

/** An agent's state in Python's words (lib/classic-agents.ts: "working", "needs you", "asks you",
 * "failed"), with what a phone has no other room for: "queued · 2nd", "paused · write budget". */
export function agentStateWords(a: StateFields, asks = false): string {
  const words = stateWords(a, asks);
  if (a.shelved_at) return words;
  if (a.state === "queued" && a.queue_pos) return `${words} · ${ordinal(a.queue_pos)}`;
  if (a.state === "paused" && a.paused_reason && PAUSE_WORDS[a.paused_reason]) return `${words} · ${PAUSE_WORDS[a.paused_reason]}`;
  return words;
}

/** Whose turn it is, for the colour of the words: yours (amber), the agent's (green), a fault, or nobody's. */
export function stateTone(a: Pick<Agent, "state" | "shelved_at">): "you" | "moving" | "fault" | "quiet" {
  if (a.shelved_at) return "quiet";
  if (a.state === "waiting" || a.state === "paused") return "you";
  if (a.state === "working" || a.state === "starting") return "moving";
  if (a.state === "error") return "fault";
  return "quiet";
}

/** The line under an agent's name in its header on a phone: the state in words first, then
 * Python's "project · machine · role" (the same words as the computer's classic header).
 * "working · Clinic site · studio · frontend". */
export function agentHeaderLine(
  a: StateFields & Pick<Agent, "role" | "level" | "project_id">,
  asks: boolean,
  project: string | undefined,
  machine: string | undefined,
): { state: string; rest: string } {
  return { state: agentStateWords(a, asks), rest: agentLine(a, project, machine).join(" · ") };
}

// ---------------------------------------------------------------------------------------------
// The Agents and Projects tabs, and Home.

export interface PhoneAgentRow {
  agent: Agent;
  /** the line under the name: project and role */
  sub: string;
  state: string;
}

const RANK: Record<AgentState, number> = { waiting: 0, paused: 0, error: 1, working: 2, starting: 2, queued: 3, idle: 4, stopped: 5, exited: 5 };

/** The Agents tab: one flat list. What needs you first, then what is working, the rest by last
 * activity. The search is the computer's (agents, projects, machines) and also finds a state in
 * words. asks: the ids of the agents that put a question ("asks you"). */
export function phoneAgentRows(
  agents: Agent[],
  projects: Record<string, Pick<Project, "name">>,
  query = "",
  opts: { machine?: string; asks?: ReadonlySet<string> } = {},
): PhoneAgentRow[] {
  const q = query.trim().toLowerCase();
  const rank = (a: Agent) => (a.shelved_at ? 6 : RANK[a.state] ?? 5);
  return agents
    .map((agent) => {
      const project = agent.project_id ? projects[agent.project_id]?.name ?? "" : "";
      return { row: { agent, sub: [project, agentSecondLine(agent, "")].filter(Boolean).join(" · "), state: agentStateWords(agent, opts.asks?.has(agent.id)) }, project };
    })
    .filter(({ row, project }) => !q || agentMatches(row.agent, q, project, opts.machine) || row.state.toLowerCase().includes(q))
    .map(({ row }) => row)
    .sort(
      (a, b) =>
        rank(a.agent) - rank(b.agent) ||
        Number(b.agent.level === "head") - Number(a.agent.level === "head") ||
        (b.agent.last_activity_at || 0) - (a.agent.last_activity_at || 0) ||
        a.agent.name.localeCompare(b.agent.name),
    );
}

/** The agents that put a question to the owner: their state reads "asks you". */
export function askingAgents(approvals: Iterable<{ agent_id: string; state: string; deferred?: boolean; kind: string }>): Set<string> {
  const ids = new Set<string>();
  for (const a of approvals) if (a.state === "pending" && !a.deferred && a.kind === "question") ids.add(a.agent_id);
  return ids;
}

/** Home's "Pick up a conversation": the Architect first (it is the conversation you came for),
 * then the agents the list puts on top (what needs you, what is working), three in all. */
export function homeAgents(rows: PhoneAgentRow[], headId: string | null | undefined, max = 3): PhoneAgentRow[] {
  const head = rows.find((r) => r.agent.id === headId);
  return (head ? [head, ...rows.filter((r) => r !== head)] : rows).slice(0, max);
}

export interface PhoneProjectTile {
  project: Project;
  working: number;
  agents: number;
  needsYou: number;
}

/** The Projects tab: one tile per project with "N working · N agents"; active projects first,
 * the most recently touched on top. Shelved agents are not counted. */
export function phoneProjectTiles(projects: Record<string, Project>, agents: Agent[]): PhoneProjectTile[] {
  const order = { active: 0, paused: 1, archived: 2 } as const;
  return Object.values(projects)
    .map((project) => {
      const team = agents.filter((a) => a.project_id === project.id && !a.shelved_at);
      return {
        project,
        agents: team.length,
        working: team.filter((a) => a.state === "working" || a.state === "starting").length,
        needsYou: team.filter((a) => a.state === "waiting" || a.state === "paused").length,
      };
    })
    .sort((a, b) => order[a.project.status] - order[b.project.status] || b.project.updated_at - a.project.updated_at || a.project.name.localeCompare(b.project.name));
}

/** "2 working · 5 agents", plus what waits for the owner. */
export function projectTileLine(t: Pick<PhoneProjectTile, "working" | "agents" | "needsYou">): string {
  const parts = [`${t.working} working`, `${t.agents} ${t.agents === 1 ? "agent" : "agents"}`];
  if (t.needsYou > 0) parts.push(`${t.needsYou} ${t.needsYou === 1 ? "needs" : "need"} you`);
  return parts.join(" · ");
}

export interface PhoneHomeRow {
  label: string;
  path: string;
  /** what the row is for, in a few words */
  hint: string;
}

/** Home's plain rows on a phone: what Python's Home links to (Notifications, Companion,
 * WhatsApp, People; not Promises, which the Matrix replaced: the owner, 17 Nov 18:55), the Go pages that are kept (Recordings, the Matrix, Usage and
 * accounts, the conversations with contacts) and Settings: the classic sidebar's entries. They
 * replace the menu. */
export const PHONE_HOME_ROWS: PhoneHomeRow[] = [
  { label: "Notifications", path: "/settings?section=phone-notifications", hint: "Approvals on this phone" },
  { label: "Companion", path: "/companion", hint: "Your desktop assistant" },
  { label: "WhatsApp", path: "/whatsapp", hint: "Chats and drafts" },
  { label: "Matrix", path: "/matrix", hint: "Every thread with everything on it" },
  { label: "People", path: "/people", hint: "Team, clients and partners" },
  { label: "Chats", path: "/chats", hint: "Conversations with your contacts" },
  { label: "Recordings", path: "/recordings", hint: "Meetings and summaries" },
  { label: "Shells", path: "/shells", hint: "Shells and scripts running on this machine" },
  { label: "Resources", path: "/resources", hint: "What agents can use, and whether it works" },
  { label: "Usage and accounts", path: "/usage", hint: "AI accounts and their quota" },
  { label: "Settings", path: "/settings", hint: "This machine, safety, appearance" },
];

// ---------------------------------------------------------------------------------------------
// Your turn: one card per screen (components/phone/turn-deck.tsx). The order of the choices and
// the project chips are the computer's (lib/card-choices.ts).

/** Which card the deck shows: the chosen one while it is in the list; once it is answered and
 * gone, the card that took its place (the last one when it was the last). -1 for an empty list. */
export function deckIndex(ids: string[], chosen: string | null | undefined, last: number): number {
  if (ids.length === 0) return -1;
  const i = chosen ? ids.indexOf(chosen) : -1;
  if (i >= 0) return i;
  return Math.min(Math.max(0, last), ids.length - 1);
}

/** A sideways swipe moves one card: +1 (next) to the left, -1 (previous) to the right. A short,
 * slanted or mostly vertical drag is a scroll, not a swipe. */
export function swipeStep(dx: number, dy: number): -1 | 0 | 1 {
  if (Math.abs(dx) < 56 || Math.abs(dx) < 2 * Math.abs(dy)) return 0;
  return dx < 0 ? 1 : -1;
}
