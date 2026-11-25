import { reloadGate } from "./reload-guard";
// The UI's state: the daemon snapshot, kept current by BusEvents, plus per-agent timelines.
import { StreamHealth, HEALTH_INTERVAL_MS, bounded } from "./stream-health";
import { create } from "zustand";
import { ApiError, type EventStream, type Transport } from "./transport";
import { derivePolicy } from "./profiles";
import { loadResources, onResourceEvent } from "./resources";
import type { Agent, Approval, BusEvent, Message, Policy, Profile, Project, Snapshot, TimelineEntry } from "./types";
import { useUI } from "./ui";

/** locked: a paired phone the daemon answers 423 to; the lock screen covers the page (lib/phone-lock.ts). */
export type Status = "booting" | "ready" | "unauthorized" | "offline" | "locked";
export type Conn = "connecting" | "open" | "reconnecting";

export interface Timeline {
  error?: boolean; // last fetch failed; retained entries may be stale while retrying
  entries: TimelineEntry[];
  /** a full view (last 200+) has been fetched, not just the preview */
  full: boolean;
  loading: boolean;
  /** asked for N and got N: there may be more before */
  maybeMore: boolean;
  limit: number;
}

interface State {
  transport: Transport | null;
  status: Status;
  statusMessage: string;
  conn: Conn;
  /** when the event stream dropped (ms); 0 while open */
  droppedAt: number;

  machine: string;
  version: string;
  workspace: string;
  /** the daemon's UI profile: "classic" or "" (lib/ui-profile.ts; read it with useClassic) */
  uiProfile: string;
  policy: Policy;
  /** by slug */
  profiles: Record<string, Profile>;
  headId: string | null;
  agents: Record<string, Agent>;
  projects: Record<string, Project>;
  approvals: Record<string, Approval>;
  messages: Message[];
  timelines: Record<string, Timeline>;
}

export const useStore = create<State>(() => ({
  transport: null,
  status: "booting",
  statusMessage: "",
  conn: "connecting",
  droppedAt: 0,
  machine: "",
  version: "",
  workspace: "",
  uiProfile: "",
  policy: { levels: {} },
  profiles: {},
  headId: null,
  agents: {},
  projects: {},
  approvals: {},
  messages: [],
  timelines: {},
}));

const set = useStore.setState;
const get = useStore.getState;

// ---------------------------------------------------------------------------------------------
// listeners for things the shell reacts to (toasts)

type ApprovalListener = (a: Approval) => void;
const approvalListeners = new Set<ApprovalListener>();
export function onApprovalCreated(fn: ApprovalListener) {
  approvalListeners.add(fn);
  return () => {
    approvalListeners.delete(fn);
  };
}

// ---------------------------------------------------------------------------------------------
// snapshot + events

const PREVIEW_LIMIT = 12;
const FULL_LIMIT = 200;

/**
 * The dashboard's snapshot: without shelved agents (a fleet can hold hundreds of them, and they
 * are hidden unless "Show all" is on). With "Show all" on, every agent.
 */
function snapshotPath(): string {
  return useUI.getState().showAllAgents ? "/api/snapshot" : "/api/snapshot?shelved=0";
}

function applySnapshot(s: Snapshot) {
  const agents: Record<string, Agent> = {};
  // A snapshot without shelved agents (the event stream's, after a reconnect) must not drop
  // the shelved ones "Show all" already loaded.
  if (s.shelved_omitted !== undefined && useUI.getState().showAllAgents) {
    for (const a of Object.values(get().agents)) if (a.shelved_at) agents[a.id] = a;
  }
  for (const a of s.agents ?? []) agents[a.id] = a;
  if (s.head) agents[s.head.id] = s.head;
  const projects: Record<string, Project> = {};
  for (const p of s.projects ?? []) projects[p.id] = p;
  const approvals: Record<string, Approval> = {};
  for (const a of s.approvals ?? []) if (a.state === "pending") approvals[a.id] = a;
  const headId = s.head?.id ?? Object.values(agents).find((a) => a.level === "head")?.id ?? null;
  const profiles: Record<string, Profile> = {};
  for (const p of s.profiles ?? []) profiles[p.slug] = p;
  const timelines = { ...get().timelines };
  for (const id of Object.keys(timelines)) if (!agents[id]) delete timelines[id];
  set({
    machine: s.machine,
    version: s.version,
    workspace: s.workspace,
    uiProfile: s.ui_profile ?? "",
    policy: s.policy ?? { levels: {} },
    profiles,
    headId,
    agents,
    projects,
    approvals,
    timelines,
    status: "ready",
    statusMessage: "",
  });
  void loadResources();
  // Previews feed the overview's activity list and the head's "now" line; full timelines that
  // were open before a reconnect catch up from their last seq.
  for (const a of Object.values(agents)) {
    const tl = timelines[a.id];
    if (tl?.error) void loadTimeline(a.id, Math.max(FULL_LIMIT, tl.limit), true);
    else if (tl && tl.entries.length) void catchUp(a.id);
    else void loadTimeline(a.id, tl?.full ? FULL_LIMIT : PREVIEW_LIMIT, !!tl?.full);
  }
}

function appendEntries(agentId: string, incoming: TimelineEntry[]) {
  if (!incoming.length) return;
  set((st) => {
    const tl = st.timelines[agentId] ?? { entries: [], full: false, loading: false, maybeMore: false, limit: 0 };
    const last = tl.entries.length ? tl.entries[tl.entries.length - 1].seq : -1;
    let entries: TimelineEntry[];
    if (incoming.every((e) => e.seq > last)) entries = tl.entries.concat(incoming);
    else {
      const bySeq = new Map<number, TimelineEntry>();
      for (const e of tl.entries) bySeq.set(e.seq, e);
      for (const e of incoming) bySeq.set(e.seq, e);
      entries = [...bySeq.values()].sort((a, b) => a.seq - b.seq);
    }
    return { timelines: { ...st.timelines, [agentId]: { ...tl, entries } } };
  });
}

// Other stores (chats) follow the same event stream.
type EventListener = (ev: BusEvent) => void;
const eventListeners = new Set<EventListener>();
export function onBusEvent(fn: EventListener) {
  eventListeners.add(fn);
  return () => {
    eventListeners.delete(fn);
  };
}

function handleEvent(ev: BusEvent) {
  eventListeners.forEach((fn) => fn(ev));
  switch (ev.type) {
    case "snapshot":
      applySnapshot(ev.data as Snapshot);
      attempt = 0;
      set({ conn: "open", droppedAt: 0 });
      break;
    case "agent.updated": {
      const a = ev.data as Agent;
      if (!a?.id) break;
      set((st) => ({
        agents: { ...st.agents, [a.id]: a },
        headId: a.level === "head" ? a.id : st.headId,
      }));
      break;
    }
    case "agent.removed": {
      const id = ev.agent_id || (ev.data as { id?: string })?.id;
      if (!id) break;
      set((st) => {
        const agents = { ...st.agents };
        delete agents[id];
        const timelines = { ...st.timelines };
        delete timelines[id];
        return { agents, timelines, headId: st.headId === id ? null : st.headId };
      });
      break;
    }
    case "project.updated": {
      const p = ev.data as Project;
      if (p?.id) set((st) => ({ projects: { ...st.projects, [p.id]: p } }));
      break;
    }
    case "project.removed": {
      const id = ev.project_id || (ev.data as { id?: string })?.id;
      if (!id) break;
      set((st) => {
        const projects = { ...st.projects };
        delete projects[id];
        return { projects };
      });
      break;
    }
    case "timeline.appended": {
      const e = ev.data as TimelineEntry;
      const id = e?.agent_id || ev.agent_id;
      if (id && e) appendEntries(id, [e]);
      break;
    }
    case "approval.created": {
      const a = ev.data as Approval;
      if (!a?.id) break;
      const isNew = !get().approvals[a.id];
      set((st) => ({ approvals: { ...st.approvals, [a.id]: a } }));
      if (isNew) approvalListeners.forEach((fn) => fn(a));
      break;
    }
    case "approval.resolved": {
      const a = ev.data as Approval;
      const id = a?.id;
      if (!id) break;
      set((st) => {
        const approvals = { ...st.approvals };
        delete approvals[id];
        return { approvals };
      });
      break;
    }
    case "message.created":
    case "message.updated": {
      const m = ev.data as Message;
      if (!m?.id) break;
      set((st) => {
        const i = st.messages.findIndex((x) => x.id === m.id);
        const messages = i >= 0 ? st.messages.map((x, j) => (j === i ? m : x)) : [...st.messages, m].slice(-200);
        return { messages };
      });
      break;
    }
    case "resources.sampled":
    case "slots.updated":
      onResourceEvent(ev);
      break;
    case "profile.updated": {
      const p = ev.data as Profile;
      if (!p?.slug) break;
      set((st) => {
        const profiles = { ...st.profiles, [p.slug]: p };
        return { profiles, policy: derivePolicy(profiles) };
      });
      break;
    }
    case "profile.removed": {
      const slug = (ev.data as { slug?: string })?.slug;
      if (!slug) break;
      set((st) => {
        const profiles = { ...st.profiles };
        delete profiles[slug];
        return { profiles, policy: derivePolicy(profiles) };
      });
      break;
    }
    default:
    // Unknown event types are ignored, never fatal (DESIGN.md).
  }
}

// ---------------------------------------------------------------------------------------------
// connection manager

let stream: EventStream | null = null;
let retryTimer: ReturnType<typeof setTimeout> | undefined;
let attempt = 0;
const health = new StreamHealth();
let monitorTimer: ReturnType<typeof setInterval> | undefined;
let lastHealthAt = 0;
let checkingHealth = false;
let monitorCleanup: (() => void) | undefined;

function streamLost() {
  stream?.close();
  stream = null;
  set((st) => ({ conn: "reconnecting", droppedAt: st.droppedAt || Date.now() }));
  scheduleReconnect();
}

async function checkHealth() {
  const t = get().transport;
  if (!t || t.kind === "mock" || checkingHealth) return;
  checkingHealth = true;
  lastHealthAt = Date.now();
  try {
    const h = await bounded(t.request<{ version: string }>("GET", "/api/health"));
    if (t !== get().transport) return;
    if (health.versionChanged(h.version)) reloadGate.request(() => window.location.reload());
  } catch {
    if (t === get().transport && get().conn === "open") streamLost();
  } finally { checkingHealth = false; }
}

function monitorConnection() {
  clearInterval(monitorTimer);
  monitorCleanup?.();
  const tick = () => {
    if (get().transport?.kind === "mock" || get().status === "locked" || get().status === "unauthorized") return;
    if (stream && health.stale(Date.now())) streamLost();
    if (Date.now() - lastHealthAt >= HEALTH_INTERVAL_MS) void checkHealth();
  };
  monitorTimer = setInterval(tick, 5000);
  // Mobile browsers suspend timers while locked. Recheck immediately on return.
  if (typeof window !== "undefined") {
    const online = () => { tick(); retryNow(); void checkHealth(); };
    const visible = () => { if (!document.hidden) { tick(); void checkHealth(); } };
    window.addEventListener("online", online);
    document.addEventListener("visibilitychange", visible);
    monitorCleanup = () => { window.removeEventListener("online", online); document.removeEventListener("visibilitychange", visible); };
  }
}

export async function start(t: Transport) {
  set({ transport: t });
  try {
    const snap = await bounded(t.request<Snapshot>("GET", snapshotPath()));
    health.versionChanged(snap.version);
    applySnapshot(snap);
  } catch (e) {
    if (e instanceof ApiError && (e.status === 401 || e.status === 403)) {
      set({ status: "unauthorized", statusMessage: e.message });
      return;
    }
    if (e instanceof ApiError && e.status === 423) {
      set({ status: "locked", statusMessage: "" }); // unlocking calls retryNow()
      return;
    }
    set({ status: get().status === "ready" ? "ready" : "offline", statusMessage: (e as Error).message });
  }
  monitorConnection();
  connect();
}

function connect() {
  const t = get().transport;
  if (!t) return;
  stream?.close();
  health.frame(Date.now());
  set((st) => ({ conn: st.conn === "open" ? "reconnecting" : st.conn }));
  stream = t.openEvents({
    onOpen() {
      void checkHealth();
    },
    onMessage(ev) {
      health.frame(Date.now());
      handleEvent(ev);
    },
    onClose() {
      streamLost();
    },
  });
}

function scheduleReconnect() {
  clearTimeout(retryTimer);
  const delay = Math.min(10_000, 400 * 2 ** attempt) * (0.8 + Math.random() * 0.4);
  attempt++;
  retryTimer = setTimeout(async () => {
    const t = get().transport;
    if (!t) return;
    if (get().status !== "ready") {
      try {
        applySnapshot(await bounded(t.request<Snapshot>("GET", snapshotPath())));
      } catch (e) {
        if (e instanceof ApiError && (e.status === 401 || e.status === 403)) {
          set({ status: "unauthorized", statusMessage: e.message });
          return;
        }
        if (e instanceof ApiError && e.status === 423) {
          set({ status: "locked", statusMessage: "" }); // unlocking calls retryNow()
          return;
        }
      }
    }
    connect();
  }, delay);
}

export function retryNow() {
  attempt = 0;
  clearTimeout(retryTimer);
  scheduleReconnect();
}

// ---------------------------------------------------------------------------------------------
// timelines

const timelineRetries = new Map<string, ReturnType<typeof setTimeout>>();
const timelineAttempts = new Map<string, number>();

export async function loadTimeline(agentId: string, limit = FULL_LIMIT, full = true) {
  clearTimeout(timelineRetries.get(agentId));
  timelineRetries.delete(agentId);
  const t = get().transport;
  if (!t) return;
  set((st) => {
    const tl = st.timelines[agentId] ?? { entries: [], full: false, loading: false, maybeMore: false, limit: 0 };
    return { timelines: { ...st.timelines, [agentId]: { ...tl, loading: true } } };
  });
  try {
    const res = await bounded(t.request<{ entries: TimelineEntry[] | null }>(
      "GET",
      `/api/agents/${encodeURIComponent(agentId)}/timeline?limit=${limit}`,
    ));
    const entries = res?.entries ?? [];
    clearTimeout(timelineRetries.get(agentId));
    timelineRetries.delete(agentId);
    timelineAttempts.delete(agentId);
    set((st) => {
      const prev = st.timelines[agentId];
      // Union with what we already have (events that arrived meanwhile, or a larger fetch that
      // finished first): never shrink a timeline.
      const bySeq = new Map<number, TimelineEntry>();
      for (const e of prev?.entries ?? []) bySeq.set(e.seq, e);
      for (const e of entries) bySeq.set(e.seq, e);
      const merged = [...bySeq.values()].sort((a, b) => a.seq - b.seq);
      return {
        timelines: {
          ...st.timelines,
          [agentId]: {
            entries: merged,
            error: false,
            full: full || !!prev?.full,
            loading: false,
            maybeMore: full ? entries.length >= limit : (prev?.maybeMore ?? false),
            limit: Math.max(full ? limit : 0, prev?.limit ?? 0),
          },
        },
      };
    });
  } catch {
    if (t !== get().transport) return;
    const attempt = timelineAttempts.get(agentId) ?? 0;
    timelineAttempts.set(agentId, attempt + 1);
    clearTimeout(timelineRetries.get(agentId));
    timelineRetries.set(agentId, setTimeout(() => {
      timelineRetries.delete(agentId);
      if (t === get().transport) void loadTimeline(agentId, limit, full);
    }, Math.min(15_000, 1000 * 2 ** Math.min(attempt, 4))));
    set((st) => {
      const tl = st.timelines[agentId];
      if (!tl) return {};
      return { timelines: { ...st.timelines, [agentId]: { ...tl, loading: false, error: true } } };
    });
  }
}

const catchupCursor = new Map<string, number>();
const catchingUp = new Set<string>();

async function catchUp(agentId: string) {
  const t = get().transport;
  const tl = get().timelines[agentId];
  if (!t || !tl?.entries.length || catchingUp.has(agentId)) return;
  catchingUp.add(agentId);
  let after = catchupCursor.get(agentId) ?? tl.entries[tl.entries.length - 1].seq;
  catchupCursor.set(agentId, after);
  try {
    while (t === get().transport) {
      const res = await bounded(t.request<{ entries: TimelineEntry[] | null }>(
        "GET", `/api/agents/${encodeURIComponent(agentId)}/timeline?after=${after}&limit=1000`,
      ));
      const entries = res?.entries ?? [];
      appendEntries(agentId, entries);
      const next = entries.at(-1)?.seq ?? after;
      if (entries.length < 1000 || next <= after) { catchupCursor.delete(agentId); break; }
      after = next;
      catchupCursor.set(agentId, after);
    }
  } catch {
    if (get().conn === "open") streamLost(); // retry from a fresh snapshot, visibly
  } finally { catchingUp.delete(agentId); }
}

const fullInFlight = new Set<string>();

export function ensureFullTimeline(agentId: string) {
  const tl = get().timelines[agentId];
  if (!tl?.full && !fullInFlight.has(agentId)) {
    fullInFlight.add(agentId);
    void loadTimeline(agentId, FULL_LIMIT, true).finally(() => fullInFlight.delete(agentId));
  }
}

export function loadEarlier(agentId: string) {
  const tl = get().timelines[agentId];
  void loadTimeline(agentId, Math.max(FULL_LIMIT, tl?.limit ?? 0) * 2, true);
}

// ---------------------------------------------------------------------------------------------
// selectors

export function agentsOfProject(agents: Record<string, Agent>, projectId: string): Agent[] {
  return Object.values(agents).filter((a) => a.project_id === projectId);
}

/** lead first, then its workers, each sorted by name */
export function projectTree(agents: Record<string, Agent>, p: Project): { lead: Agent | null; workers: Agent[] } {
  const all = agentsOfProject(agents, p.id);
  const lead = (p.lead_id && agents[p.lead_id]) || all.find((a) => a.level === "lead") || null;
  const workers = all.filter((a) => a.id !== lead?.id).sort((a, b) => a.name.localeCompare(b.name));
  return { lead, workers };
}

export function pendingApprovals(approvals: Record<string, Approval>): Approval[] {
  return Object.values(approvals)
    .filter((a) => a.state === "pending" && !a.deferred)
    .sort((a, b) => Number(!!b.card?.urgent) - Number(!!a.card?.urgent)
      || (a.card?.priority || Number.MAX_SAFE_INTEGER) - (b.card?.priority || Number.MAX_SAFE_INTEGER)
      || b.created_at - a.created_at);
}

export function agentName(agents: Record<string, Agent>, idOrName: string | undefined): string {
  if (!idOrName) return "";
  if (idOrName === "owner") return "you";
  return agents[idOrName]?.name ?? idOrName;
}

// "Show all" switched on: load the shelved agents the default snapshot leaves out.
useUI.subscribe((s, prev) => {
  if (!s.showAllAgents || prev.showAllAgents) return;
  const t = get().transport;
  if (t) void t.request<Snapshot>("GET", "/api/snapshot").then(applySnapshot).catch(() => {});
});
