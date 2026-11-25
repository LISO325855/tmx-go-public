// What the projects' agents published, kept for the cards and the project screen (classic UI
// profile; lib/project-page.ts): one read per project, shared by every card and page that shows
// it, refreshed when the project changes and now and then.
import { create } from "zustand";
import { api } from "./api";
import type { ProjectArtifacts, ProjectSection } from "./project-page";
import { onBusEvent, useStore } from "./store";

export interface ProjectPageData {
  section: ProjectSection | null;
  artifacts: ProjectArtifacts | null;
  /** why it could not be read, if it could not */
  failed: string;
  loadedAt: number;
}

export const useProjectPages = create<{ pages: Record<string, ProjectPageData> }>(() => ({ pages: {} }));

const loading = new Set<string>();

/** Reads a project's shared page (again). Never throws: a failure is kept with the page. */
export async function loadProjectPage(id: string): Promise<void> {
  if (!id || loading.has(id)) return;
  loading.add(id);
  try {
    const [section, artifacts] = await Promise.all([api.projectSection(id), api.projectArtifacts(id)]);
    useProjectPages.setState((s) => ({ pages: { ...s.pages, [id]: { section, artifacts, failed: "", loadedAt: Date.now() } } }));
  } catch (e) {
    const prev = useProjectPages.getState().pages[id];
    useProjectPages.setState((s) => ({
      pages: { ...s.pages, [id]: { section: prev?.section ?? null, artifacts: prev?.artifacts ?? null, failed: (e as Error).message || "The project's page could not be read.", loadedAt: Date.now() } },
    }));
  } finally {
    loading.delete(id);
  }
}

const STALE_MS = 60_000;
let following = false;

/** Keeps the pages that are being shown current: on the project's own events and once a minute. */
export function followProjectPages() {
  if (following) return;
  following = true;
  onBusEvent((ev) => {
    const shown = useProjectPages.getState().pages;
    if (ev.type === "snapshot") Object.keys(shown).forEach((id) => void loadProjectPage(id));
    else if (ev.project_id && shown[ev.project_id]) void loadProjectPage(ev.project_id);
  });
  setInterval(() => {
    if (typeof document !== "undefined" && document.hidden) return;
    const { pages } = useProjectPages.getState();
    const known = useStore.getState().projects;
    for (const [id, p] of Object.entries(pages)) if (known[id] && Date.now() - p.loadedAt > STALE_MS) void loadProjectPage(id);
  }, STALE_MS / 2);
}
