// Which view of an agent is showing under the classic UI profile (lib/ui-profile.ts): Chat,
// Terminal or Details (the owner #7, 17 Nov: "a click only changes the view", as in tmx Python;
// the default dashboard keeps its own tabs). The view is the browser's business alone: nothing here
// talks to the daemon, so looking at the terminal never changes how the agent runs. The last of
// Chat / Terminal is remembered per agent on this device; Details is a visit, not a place to
// come back to.
export type AgentView = "conversation" | "terminal" | "details";

/** The tab names in the address (#/agents/<id>/<view>); "conversation" is kept from before. */
export const AGENT_VIEWS: AgentView[] = ["conversation", "terminal", "details"];

const KEY = "tmx.agentViews";
/** Agents come and go; the map keeps the most recently chosen ones. */
export const MAX_REMEMBERED = 300;

export function isAgentView(v: unknown): v is AgentView {
  return typeof v === "string" && (AGENT_VIEWS as string[]).includes(v);
}

/** A view worth coming back to. */
function isSticky(v: unknown): v is "conversation" | "terminal" {
  return v === "conversation" || v === "terminal";
}

/** Where an agent opens when nothing is remembered: Chat; a harness with only its screen, there. */
export function defaultAgentView(chatCapable: boolean): AgentView {
  return chatCapable ? "conversation" : "terminal";
}

/**
 * The view to show: the one named in the address wins (a link to a tab), then the one
 * remembered for this agent, then the default.
 */
export function resolveAgentView(tab: string | undefined | null, remembered: AgentView | undefined | null, chatCapable: boolean): AgentView {
  if (isAgentView(tab)) return tab;
  if (isSticky(remembered)) return remembered;
  return defaultAgentView(chatCapable);
}

/** The saved map, tolerant of anything a browser may hand back. Insertion order = oldest first. */
export function parseAgentViews(raw: string | null | undefined): Record<string, AgentView> {
  if (!raw) return {};
  let v: unknown;
  try {
    v = JSON.parse(raw);
  } catch {
    return {};
  }
  if (!v || typeof v !== "object" || Array.isArray(v)) return {};
  const out: Record<string, AgentView> = {};
  for (const [id, view] of Object.entries(v as Record<string, unknown>)) {
    if (id && isSticky(view)) out[id] = view;
  }
  return out;
}

/** The map with this agent's view put last (newest); Details is not kept; the oldest fall off. */
export function withAgentView(map: Record<string, AgentView>, id: string, view: AgentView, max = MAX_REMEMBERED): Record<string, AgentView> {
  if (!id || !isSticky(view)) return map;
  const out: Record<string, AgentView> = {};
  for (const [k, v] of Object.entries(map)) if (k !== id) out[k] = v;
  out[id] = view;
  const keys = Object.keys(out);
  for (const k of keys.slice(0, Math.max(0, keys.length - max))) delete out[k];
  return out;
}

let cache: Record<string, AgentView> | null = null;

function load(): Record<string, AgentView> {
  if (cache) return cache;
  try {
    cache = parseAgentViews(localStorage.getItem(KEY));
  } catch {
    cache = {}; // private mode: remembered for this visit only
  }
  return cache;
}

/** The view last chosen for this agent on this device, if any. */
export function rememberedAgentView(id: string): AgentView | undefined {
  return load()[id];
}

/** Remembers a click on Chat or Terminal for this agent (Details is not remembered). */
export function rememberAgentView(id: string, view: AgentView) {
  const next = withAgentView(load(), id, view);
  if (next === cache) return;
  cache = next;
  try {
    localStorage.setItem(KEY, JSON.stringify(next));
  } catch {
    // private mode or a full store: it still holds until the page reloads
  }
}

// ---- side by side ------------------------------------------------------------------------------
// Classic, on a wide screen (parity B1; Python's "Split workspace" / "Focus view"): Chat and
// Terminal sit side by side with the message box full width underneath. Each of the two buttons
// shows or hides its pane, as in Python; "Focus view" goes back to one view at a time (the tabs
// above). The layout is one choice for all agents, remembered on the device, as in Python.

export type AgentPane = "chat" | "terminal";

export interface PaneLayout {
  /** side by side (Python's default on a wide screen); false = Focus view, one at a time */
  split: boolean;
  chat: boolean;
  terminal: boolean;
}

/** From this width the panes can sit side by side. */
export const SPLIT_MIN_WIDTH = 1280;
export const DEFAULT_PANES: PaneLayout = { split: true, chat: true, terminal: true };
const PANES_KEY = "tmx.agentPanes";

/** The saved layout, tolerant of anything; a layout with no pane shown is not one. */
export function parsePanes(raw: string | null | undefined): PaneLayout {
  if (!raw) return DEFAULT_PANES;
  let v: unknown;
  try {
    v = JSON.parse(raw);
  } catch {
    return DEFAULT_PANES;
  }
  if (!v || typeof v !== "object" || Array.isArray(v)) return DEFAULT_PANES;
  const o = v as Record<string, unknown>;
  const chat = o.chat !== false;
  const terminal = o.terminal !== false;
  return { split: o.split !== false, chat: chat || !terminal, terminal };
}

/** Shows or hides a pane; the last pane showing stays (there is always something to look at). */
export function togglePane(l: PaneLayout, pane: AgentPane): PaneLayout {
  const next = { ...l, [pane]: !l[pane] };
  return next.chat || next.terminal ? next : l;
}

/** Makes sure a pane shows (coming back from Details by its button). */
export function showPane(l: PaneLayout, pane: AgentPane): PaneLayout {
  return l[pane] ? l : { ...l, [pane]: true };
}

/**
 * Side by side only where it makes sense: the classic profile, a wide screen, the owner's
 * choice, and an agent with both a chat and a screen (not a shell or a terminal-only harness).
 */
export function splitApplies(o: { classic: boolean; wide: boolean; layout: PaneLayout; chatCapable: boolean; harness?: string }): boolean {
  return o.classic && o.wide && o.layout.split && o.chatCapable && o.harness !== "shell";
}

export function savedPanes(): PaneLayout {
  try {
    return parsePanes(localStorage.getItem(PANES_KEY));
  } catch {
    return DEFAULT_PANES;
  }
}

export function savePanes(l: PaneLayout) {
  try {
    localStorage.setItem(PANES_KEY, JSON.stringify(l));
  } catch {
    // private mode: the layout holds until the page goes
  }
}

/** The single view must follow the pane actually visible before a split collapses. */
export function visiblePaneView(layout: PaneLayout, preferred: AgentView): AgentView {
  if (preferred === "details") return preferred;
  if (!layout.terminal) return "conversation";
  if (!layout.chat) return "terminal";
  return preferred;
}
