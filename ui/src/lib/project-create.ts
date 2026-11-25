import type { CreateProjectBody, Harness, HarnessInfo } from "./types";

export function projectLeadHarnesses(harnesses: HarnessInfo[]): HarnessInfo[] {
  return harnesses.filter((h) => h.modes.includes("chat"));
}

export function createProjectPayload(input: {
  name: string;
  harness: Harness;
  folder?: string;
  brief?: string;
}): CreateProjectBody {
  return {
    name: input.name,
    harness: input.harness,
    ...(input.folder ? { folder: input.folder } : {}),
    ...(input.brief ? { brief: input.brief } : {}),
  };
}

/**
 * The projects the New agent form offers when it is opened from anywhere ("+ Agent"; the project
 * is a field of the form): every project that is not archived, active ones first, by name.
 */
export function newAgentProjects<T extends { id: string; name: string; status: string }>(projects: readonly T[]): T[] {
  const rank = (p: T) => (p.status === "active" ? 0 : 1);
  return projects.filter((p) => p.status !== "archived").sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name));
}

/** The project that form starts on: the preferred one if it is offered, else the first offered, else none (""). */
export function pickNewAgentProject<T extends { id: string; name: string; status: string }>(projects: readonly T[], preferredId?: string | null): string {
  const offered = newAgentProjects(projects);
  if (preferredId && offered.some((p) => p.id === preferredId)) return preferredId;
  return offered[0]?.id ?? "";
}
