// The Board view of the Matrix (classic UI profile; the owner's most repeated wish, 17 Nov:
// "prosthetics for my brain"). One row per thread, in four groups in this order: Needs you,
// Blocked, In progress, Done today. A row says how far the thread is (0-100, set by its agents
// with a one-line why), what blocks it and exactly what the owner has to do, who owns it and
// when it last moved. Pure: pages/matrix.tsx and components/matrix/thread-board.tsx draw it.

export type BoardGroup = "needs_you" | "blocked" | "in_progress" | "done_today";

export const BOARD_GROUPS: { group: BoardGroup; label: string }[] = [
  { group: "needs_you", label: "Needs you" },
  { group: "blocked", label: "Blocked" },
  { group: "in_progress", label: "In progress" },
  { group: "done_today", label: "Done today" },
];

interface ThreadLike {
  id: string;
  title: string;
  status?: string;
  archived?: boolean;
  /** the owner's urgency order: 1 = most urgent; absent or 0 = not placed */
  position?: number;
  updated_at: number;
  /** 0-100, set by the thread's agents; absent = never reported */
  progress?: number | null;
  /** the one line that came with the progress */
  progress_why?: string;
  /** what the thread waits for, when it is blocked */
  blocker?: string;
}

interface CardLike<T extends ThreadLike = ThreadLike> {
  thread: T;
  last_event_at?: number;
}

/** When a thread last moved: its last event, else its own last change. */
export function lastMoved(c: CardLike): number {
  return Math.max(c.last_event_at ?? 0, c.thread.updated_at ?? 0);
}

/** The start of the local day that holds now. */
export function startOfDay(now: number): number {
  const d = new Date(now);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
}

/**
 * Which group a thread's row is in, or null when it is not on the board (archived, or done
 * before today). A thread with an open question for the owner needs him, whatever its status
 * says; so does one whose status is "waiting on you". A paused thread is in progress (its row
 * says paused): it is not blocked by anything outside.
 */
export function boardGroupOf(c: CardLike, asks: number, now: number): BoardGroup | null {
  const t = c.thread;
  if (t.archived) return null;
  const status = t.status || "active";
  if (status === "done") return lastMoved(c) >= startOfDay(now) ? "done_today" : null;
  if (asks > 0 || status === "waiting_on_owner") return "needs_you";
  if (status === "blocked") return "blocked";
  return "in_progress";
}

/** A progress value as the bar shows it: a whole number from 0 to 100, or null when none was reported. */
export function boardProgress(t: Pick<ThreadLike, "progress" | "status">): number | null {
  if ((t.status || "active") === "done") return 100;
  const p = t.progress;
  if (p === null || p === undefined || !Number.isFinite(p) || p < 0) return null;
  return Math.max(0, Math.min(100, Math.round(p)));
}

export interface BoardRow<C> {
  card: C;
  group: BoardGroup;
  /** open questions for the owner on this thread */
  asks: number;
  /** 0-100 or null */
  progress: number | null;
  moved: number;
}

export interface BoardSection<C> {
  group: BoardGroup;
  label: string;
  rows: BoardRow<C>[];
}

/**
 * The board: every group that has rows, in the fixed order. Inside a group the owner's own
 * urgency order comes first (the placed threads, 1 first), then the furthest along (as on the
 * Architect's stopgap board; a thread that reported nothing comes after those that did), then
 * the most recently moved; in "Done today" simply the most recent first.
 */
export function boardSections<C extends CardLike>(cards: readonly C[], asksOf: (threadId: string) => number, now: number): BoardSection<C>[] {
  const rows: BoardRow<C>[] = [];
  for (const card of cards) {
    const asks = asksOf(card.thread.id);
    const group = boardGroupOf(card, asks, now);
    if (group) rows.push({ card, group, asks, progress: boardProgress(card.thread), moved: lastMoved(card) });
  }
  const placed = (r: BoardRow<C>) => (r.card.thread.position && r.card.thread.position > 0 ? r.card.thread.position : Number.MAX_SAFE_INTEGER);
  return BOARD_GROUPS.map(({ group, label }) => ({
    group,
    label,
    rows: rows
      .filter((r) => r.group === group)
      .sort((a, b) => (group === "done_today" ? 0 : placed(a) - placed(b) || (b.progress ?? -1) - (a.progress ?? -1)) || b.moved - a.moved || a.card.thread.title.localeCompare(b.card.thread.title)),
  })).filter((s) => s.rows.length > 0);
}

/** How many threads are done but left the board because that was before today. */
export function doneEarlier(cards: readonly CardLike[], now: number): number {
  return cards.filter((c) => !c.thread.archived && (c.thread.status || "active") === "done" && lastMoved(c) < startOfDay(now)).length;
}

/** The words under a row's bar: "60%" and the why, or what to say when nothing was reported. */
export function progressText(progress: number | null, why: string | undefined, status: string | undefined): string {
  if ((status || "active") === "done") return "Done";
  if (progress === null) return "Not reported";
  const w = (why ?? "").trim();
  return w ? `${progress}% · ${w}` : `${progress}%`;
}

// ---- the interactive Board (the owner, 17 Nov 20:58): one-tap actions, a note, his order ----

/** What the owner can do on a row with one tap (the daemon's acts, internal/fleet/matrix_owner.go). */
export type OwnerAct = "note" | "park" | "not_relevant" | "done" | "thanks";

export const OWNER_ACTS: { act: Exclude<OwnerAct, "note">; label: string; title: string }[] = [
  { act: "done", label: "Done", title: "Tell the agent you completed the requested action." },
  { act: "thanks", label: "Thanks", title: "Says thanks to the agent." },
  { act: "park", label: "Park", title: "Pause this thread and tell the agent to stop for now." },
  { act: "not_relevant", label: "Not relevant", title: "Remove this thread from the Board and tell the agent. Its history is kept." },
];

/** What a toast says after an act, with who was told. */
export function ownerActSaid(act: OwnerAct, mailedTo?: string): string {
  const what = { note: "Note saved", park: "Parked", not_relevant: "Taken off the Board", done: "Marked done by you", thanks: "Thanks sent" }[act];
  return mailedTo ? `${what} · ${mailedTo} was told` : `${what} · on the thread (no agent to tell)`;
}

/**
 * The owner's priority order as one list of thread ids: the threads he placed, in his order,
 * then the rest as the Board shows them (group by group). Saving this list makes the order on
 * the screen the order in the daemon, so the first move never reshuffles the other rows.
 */
export function boardOrder<C extends CardLike>(cards: readonly C[], sections: readonly BoardSection<C>[]): string[] {
  const pos = (c: C) => c.thread.position ?? 0;
  const placed = cards.filter((c) => pos(c) > 0).sort((a, b) => pos(a) - pos(b)).map((c) => c.thread.id);
  const seen = new Set(placed);
  const out = [...placed];
  const add = (id: string) => {
    if (!seen.has(id)) {
      seen.add(id);
      out.push(id);
    }
  };
  for (const s of sections) for (const r of s.rows) add(r.card.thread.id);
  for (const c of cards) add(c.thread.id);
  return out;
}

/**
 * Moving a row inside its group: `to` is "up", "down", or the id of the row it is dropped on
 * (it takes that row's place). Returns the whole new order, or null when nothing moves (the
 * first row up, the last down, a drop on itself or on another group's row).
 */
export function movedOrder(order: readonly string[], groupIds: readonly string[], id: string, to: "up" | "down" | string): string[] | null {
  const at = groupIds.indexOf(id);
  if (at < 0) return null;
  const target = to === "up" ? at - 1 : to === "down" ? at + 1 : groupIds.indexOf(to);
  if (target < 0 || target >= groupIds.length || target === at) return null;
  // the group's rows keep their slots in the whole order; only who sits in which slot changes
  const group = [...groupIds];
  group.splice(at, 1);
  group.splice(target, 0, id);
  const slots = groupIds.map((g) => order.indexOf(g));
  if (slots.some((s) => s < 0)) return null;
  const sorted = [...slots].sort((a, b) => a - b);
  const out = [...order];
  group.forEach((g, i) => {
    out[sorted[i]] = g;
  });
  return out;
}

/** The two acts that change a thread's state can be undone from their toast for a few seconds. */
export const UNDO_MS = 5000;
export const canUndo = (act: OwnerAct): act is "park" | "not_relevant" => act === "park" || act === "not_relevant";
/** What the agent is told when the owner takes an act back (it already got the first mail). */
export function undoNote(act: "park" | "not_relevant"): string {
  return act === "park" ? 'Undo: ignore my "Park it". Carry on.' : 'Undo: ignore my "Not relevant". Carry on.';
}
