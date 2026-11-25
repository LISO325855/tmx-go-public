// A project's shared page the Python way (classic UI profile; parity H2): where it stands (the
// status its agents publish, at most three lines), the progress (the checklist with its ticks),
// links, the final outputs to grab, and each member's own line. The daemon already keeps all of
// it (docs/project-sections.md; GET /api/projects/{id}/section and /artifacts); this is the
// screen for it. Pure helpers for components/classic/project-status.tsx.

export interface ProjectNeed {
  text: string;
  done: boolean;
}
export interface ProjectLink {
  title: string;
  url: string;
}
export interface ProjectOutput {
  path: string;
  what: string;
}
/** GET /api/projects/{id}/section (internal/model/board.go ProjectSection) */
export interface ProjectSection {
  project_id: string;
  slug: string;
  name: string;
  status: string;
  needs: ProjectNeed[] | null;
  links: ProjectLink[] | null;
  final_output: ProjectOutput[] | null;
  lead?: string;
  updated_at: number;
  updated_by: string;
  /** the project's repository address, set by the owner (internal/fleet/project_repo.go) */
  repo?: string;
}
export interface ProjectContribution {
  project_id: string;
  agent_id: string;
  agent_name: string;
  status: string;
  final_output: ProjectOutput[] | null;
  updated_at: number;
}
export interface ProjectArtifact extends ProjectOutput {
  /** "link" or "path"; never a claim that it exists */
  kind: string;
  /** empty for the shared project output */
  agent_id?: string;
}
/** GET /api/projects/{id}/artifacts (internal/model/project_artifacts.go) */
export interface ProjectArtifacts {
  project_id: string;
  final_output: ProjectArtifact[] | null;
  contributions: ProjectContribution[] | null;
}

export interface NeedsProgress {
  done: number;
  total: number;
  /** rounded 100 * done / total; 0 with no checklist */
  percent: number;
}

/** Progress is the ticked share of the checklist, never a guess; no checklist = no bar. */
export function needsProgress(needs: readonly ProjectNeed[] | null | undefined): NeedsProgress {
  const list = needs ?? [];
  const done = list.filter((n) => n.done).length;
  return { done, total: list.length, percent: list.length ? Math.round((100 * done) / list.length) : 0 };
}

/** The status as its lines (at most three are stored), without empty ones. */
export function statusLines(status: string | null | undefined): string[] {
  return (status ?? "")
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter(Boolean);
}

/**
 * Only a plain web address becomes something to click. A link or an output is whatever an agent
 * wrote: a path, or text that merely looks like an address (javascript:, data:, file:) is shown
 * as text and never opened.
 */
export function webLink(url: string | null | undefined): string | null {
  const u = (url ?? "").trim();
  if (!/^https?:\/\//i.test(u)) return null;
  try {
    const parsed = new URL(u);
    return parsed.protocol === "http:" || parsed.protocol === "https:" ? parsed.href : null;
  } catch {
    return null;
  }
}

/** A member's line is worth showing when it says something or offers an output. */
export function shownContributions(parts: readonly ProjectContribution[] | null | undefined): ProjectContribution[] {
  return (parts ?? []).filter((p) => p.status.trim() || (p.final_output ?? []).length > 0).sort((a, b) => b.updated_at - a.updated_at || a.agent_name.localeCompare(b.agent_name));
}

/** Nothing published yet: no status, no checklist, no links, no outputs, no member lines. */
export function sectionEmpty(sec: ProjectSection | null | undefined, art: ProjectArtifacts | null | undefined): boolean {
  return !statusLines(sec?.status).length && !(sec?.needs ?? []).length && !(sec?.links ?? []).length && !(art?.final_output ?? []).length && !shownContributions(art?.contributions).length;
}

// ---- the project card and the fuller project page (the owner, 17 Nov 19:01: like Python's) ----

interface MemberLike {
  id: string;
  name: string;
  state: string;
  level: string;
  shelved_at?: number | null;
}

/** A path's last part: what a file is called ("docs/billing.md" -> "billing.md"); a trailing slash is ignored. */
export function fileName(path: string | null | undefined): string {
  const parts = (path ?? "").replace(/[\\/]+$/, "").split(/[\\/]/).filter(Boolean);
  return parts[parts.length - 1] ?? "";
}

/** Python's card counts: the agents that are not shelved, and how many of them are working now. */
export function projectCounts(members: readonly MemberLike[]): { agents: number; working: number; shelved: number } {
  const live = members.filter((m) => !m.shelved_at);
  return { agents: live.length, working: live.filter((m) => m.state === "working" || m.state === "starting").length, shelved: members.length - live.length };
}

/** The card's row of heads: the coordinator first, then by name; at most 12, as in Python. */
export function cardFaces<T extends MemberLike>(members: readonly T[], leadId?: string | null, max = 12): { shown: T[]; more: number } {
  const sorted = [...members].sort((a, b) => Number(b.id === leadId) - Number(a.id === leadId) || a.name.localeCompare(b.name));
  return { shown: sorted.slice(0, max), more: Math.max(0, sorted.length - max) };
}

/** Python's two shelves of the Projects page: active projects, and the ones put aside (paused or archived). */
export function splitProjects<T extends { name: string; status: string }>(projects: readonly T[]): { active: T[]; shelf: T[] } {
  const by = (a: T, b: T) => a.name.localeCompare(b.name);
  return { active: projects.filter((p) => p.status === "active").sort(by), shelf: projects.filter((p) => p.status !== "active").sort(by) };
}

/**
 * Whose folder an output is downloaded from: the member that named it, else the project's
 * coordinator (whose folder is the project's). A web link is opened, never downloaded.
 */
export function downloadAgent(o: { path: string; agent_id?: string }, leadId: string | null | undefined, known: (id: string) => boolean): string | null {
  if (webLink(o.path)) return null;
  if (o.agent_id && known(o.agent_id)) return o.agent_id;
  return leadId && known(leadId) ? leadId : null;
}

/** The folders of a project, each once: the project's own first, then its members' where they differ. */
export function projectFolders(projectFolder: string, members: readonly { name: string; cwd?: string }[]): { path: string; who: string[] }[] {
  const out: { path: string; who: string[] }[] = [];
  const norm = (p: string) => p.replace(/[\\/]+$/, "").toLowerCase();
  if (projectFolder) out.push({ path: projectFolder, who: [] });
  for (const m of members) {
    if (!m.cwd) continue;
    const hit = out.find((f) => norm(f.path) === norm(m.cwd!));
    if (hit) hit.who.push(m.name);
    else out.push({ path: m.cwd, who: [m.name] });
  }
  return out;
}

// ---- the project's repository (the owner, 17 Nov 19:01; internal/fleet/project_repo.go) ----

/**
 * The page to open for a repository address: an https address as it is; git@host:owner/repo and
 * ssh://git@host/owner/repo as https://host/owner/repo (what a forge shows in a browser). null
 * for anything else: it is then shown as text only.
 */
export function repoWebAddress(repo: string | null | undefined): string | null {
  const r = (repo ?? "").trim();
  if (!r) return null;
  const strip = (p: string) => p.replace(/\.git$/i, "").replace(/\/+$/, "");
  const scp = /^git@([A-Za-z0-9.-]+):([A-Za-z0-9._~/-]+)$/.exec(r);
  if (scp) return webLink(`https://${scp[1]}/${strip(scp[2])}`);
  const ssh = /^ssh:\/\/git@([A-Za-z0-9.-]+)\/([A-Za-z0-9._~/-]+)$/.exec(r);
  if (ssh) return webLink(`https://${ssh[1]}/${strip(ssh[2])}`);
  const web = webLink(r);
  return web && web.startsWith("https://") ? strip(web) : null;
}

/** The short name of a repository: "owner/repo". */
export function repoName(repo: string | null | undefined): string {
  const web = repoWebAddress(repo);
  if (!web) return (repo ?? "").trim();
  try {
    return new URL(web).pathname.replace(/^\/+/, "");
  } catch {
    return (repo ?? "").trim();
  }
}
