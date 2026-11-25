// A card's choices the Python way (the "Your turn" cards; parity rows E2 and F12, the owner's list
// of 17 Nov): the recommended choice comes first and is marked on its button, "Not applicable /
// Dismiss" closes a card that does not apply, and the cards are grouped and filtered by project.
// Pure helpers: the desktop cards (components/owner-card-actions.tsx, components/approval.tsx,
// pages/inbox.tsx) and the phone's full-width buttons use the same order.

export interface CardChoice {
  /** the option exactly as the agent wrote it: this is what is sent back */
  label: string;
  /** its place in the agent's own list */
  index: number;
  recommended: boolean;
}

function norm(s: string | null | undefined): string {
  return (s ?? "")
    .toLowerCase()
    .replace(/[“”"'`*_]/g, "")
    .replace(/\s+/g, " ")
    .replace(/[\s.:;,!]+$/g, "")
    .trim();
}

/** Where a word ends: the option is named as a whole, not as the start of another word. */
function startsWithWhole(text: string, prefix: string): boolean {
  if (!prefix || !text.startsWith(prefix)) return false;
  const next = text.charAt(prefix.length);
  return next === "" || !/[a-z0-9]/.test(next);
}

/**
 * Which option a card's free-text recommendation names: the one it equals, else the longest one
 * it starts with ("Option A, because…"), else the only one that starts with it ("Option B" for
 * "Option B (the fast one)"). If several fit equally, or none does, there is no marked choice
 * (-1): marking the wrong button would be worse than marking none.
 */
export function recommendedIndex(options: readonly string[] | null | undefined, recommendation: string | null | undefined): number {
  const rec = norm(recommendation);
  if (!rec || !options?.length) return -1;
  const opts = options.map(norm);
  const exact = opts.flatMap((o, i) => (o && o === rec ? [i] : []));
  if (exact.length) return exact.length === 1 ? exact[0] : -1;
  // the recommendation starts with an option: the longest (most specific) one
  let best = -1;
  opts.forEach((o, i) => {
    if (o && startsWithWhole(rec, o) && (best < 0 || o.length > opts[best].length)) best = i;
  });
  if (best >= 0) return opts.filter((o) => o === opts[best]).length === 1 ? best : -1;
  // an option starts with the recommendation: only if it is the only one
  const longer = opts.flatMap((o, i) => (o && startsWithWhole(o, rec) ? [i] : []));
  return longer.length === 1 ? longer[0] : -1;
}

/** The choices in the order to show them: the recommended one first, the rest as the agent listed them. */
export function cardChoices(options: readonly string[] | null | undefined, recommendation: string | null | undefined): CardChoice[] {
  const list = (options ?? []).map((label, index) => ({ label, index, recommended: false }));
  const r = recommendedIndex(options, recommendation);
  if (r < 0) return list;
  list[r].recommended = true;
  return [list[r], ...list.slice(0, r), ...list.slice(r + 1)];
}

/**
 * What is left to say about the recommendation once its button is marked: nothing when it is
 * just that option's name, otherwise the whole text (it carries the reason, or names no button).
 */
export function recommendationNote(options: readonly string[] | null | undefined, recommendation: string | null | undefined): string {
  const text = (recommendation ?? "").trim();
  if (!text) return "";
  const r = recommendedIndex(options, recommendation);
  if (r >= 0 && norm(options![r]) === norm(text)) return "";
  return text;
}

/** The answer an agent gets when the owner closes a card with "Not applicable / Dismiss". */
export const NOT_APPLICABLE_ANSWER = "Not applicable: dismissed by the owner. Do not wait for an answer to this.";
export const NOT_APPLICABLE_LABEL = "Not applicable / Dismiss";

// ---- by project -------------------------------------------------------------------------------

interface CardLike {
  agent_id: string;
  card?: { project_id?: string } | null;
}

/** The project a card belongs to: the one it names, else its agent's. "" = none (the Architect's own). */
export function cardProjectId(a: CardLike, agents: Record<string, { project_id?: string } | undefined>): string {
  return a.card?.project_id || agents[a.agent_id]?.project_id || "";
}

export interface CardProjectGroup<T> {
  /** the project id; "" for cards with no project */
  key: string;
  name: string;
  items: T[];
}

/** Cards grouped by project, in the order the projects first appear; cards keep their order. */
export function groupCardsByProject<T extends CardLike>(
  list: readonly T[],
  agents: Record<string, { project_id?: string } | undefined>,
  projects: Record<string, { name: string } | undefined>,
  noProject = "No project",
): CardProjectGroup<T>[] {
  const groups = new Map<string, CardProjectGroup<T>>();
  for (const a of list) {
    const key = cardProjectId(a, agents);
    let g = groups.get(key);
    if (!g) {
      g = { key, name: key ? (projects[key]?.name ?? key) : noProject, items: [] };
      groups.set(key, g);
    }
    g.items.push(a);
  }
  return [...groups.values()];
}

export interface ProjectChip {
  /** null = all projects */
  key: string | null;
  label: string;
  count: number;
}

/** Python's chips above the cards: "All projects", then "project · n" for each. None for a single project. */
export function projectChips<T>(groups: readonly CardProjectGroup<T>[]): ProjectChip[] {
  if (groups.length < 2) return [];
  const total = groups.reduce((n, g) => n + g.items.length, 0);
  return [{ key: null, label: "All projects", count: total }, ...groups.map((g) => ({ key: g.key, label: `${g.name} · ${g.items.length}`, count: g.items.length }))];
}

/** The groups a chip leaves visible; a chip whose project has no cards any more shows all. */
export function filterCardGroups<T>(groups: readonly CardProjectGroup<T>[], chip: string | null): CardProjectGroup<T>[] {
  if (chip === null || !groups.some((g) => g.key === chip)) return [...groups];
  return groups.filter((g) => g.key === chip);
}
