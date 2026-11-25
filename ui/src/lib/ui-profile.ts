// The UI profile (daemon config.json "ui.profile", sent in the snapshot as ui_profile).
//
// "classic" (the owner, 17 Nov 2026): the dashboard is a one-to-one copy of the Python tmx
// dashboard. Everything Python did not have is hidden, the wording is Python's, and Chat /
// Terminal is Python's toggle: a click only changes the view. It is ONE switch: nothing is
// removed from the code, so a daemon without it (Mustneer's line, any other machine) shows the
// default dashboard exactly as before. Components read it with useClassic() (hooks/use-classic.ts);
// this file is the pure part, so node tests can import it.
export const UI_PROFILE_CLASSIC = "classic";

/** Whether a snapshot's ui_profile is the classic one. Unknown or missing = the default dashboard. */
export function isClassic(profile: string | null | undefined): boolean {
  return (profile ?? "").trim().toLowerCase() === UI_PROFILE_CLASSIC;
}

/** Python's word for the supervising agent; the default dashboard says "head". */
export function headWord(classic: boolean, capital = false): string {
  if (classic) return "Architect";
  return capital ? "Head" : "head";
}

/** A level in words: classic says "Architect" where the default dashboard says "Head". */
export function levelWord(level: string, classic: boolean): string {
  if (level === "head") return headWord(classic, true);
  return level === "lead" ? "Lead" : level === "worker" ? "Worker" : level;
}

/**
 * The classic agent header's one line, Python's "project · machine · role": the project (when
 * the agent has one), the machine, and the agent's role, or its level in words when it has none.
 */
export function agentLine(agent: { role?: string | null; level: string; project_id?: string | null }, project: string | undefined, machine: string | undefined): string[] {
  const role = (agent.role ?? "").trim() || levelWord(agent.level, true);
  return [agent.project_id ? project : "", machine, role].map((s) => (s ?? "").trim()).filter(Boolean);
}

/**
 * The profile the mock daemon reports: ?mock=1&ui=classic (search is location.search). Anything
 * else, or nothing, is the default dashboard.
 */
export function mockUIProfile(search: string | null | undefined): string {
  try {
    return isClassic(new URLSearchParams(search ?? "").get("ui")) ? UI_PROFILE_CLASSIC : "";
  } catch {
    return "";
  }
}
