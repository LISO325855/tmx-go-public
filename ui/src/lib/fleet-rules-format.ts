// Fleet rules (DESIGN.md "Fleet rules"): the shapes of GET /api/rules and the words the
// Settings card says about them. No imports from the store: node tests load this file alone.

export interface RulesChange {
  commit: string;
  /** as written in the commit: anyone who can write to the source can set it */
  author: string;
  time: string;
  subject: string;
}

export interface RulesDoc {
  /** the path without ".md": what rules_read takes */
  name: string;
  path: string;
  title: string;
  summary?: string;
  /** the front matter (an app's card: name, state, owner, ask ...) */
  meta?: Record<string, string>;
  size: number;
  last_change: RulesChange;
  body?: string;
}

export interface RulesAgent {
  id: string;
  name: string;
  state: string;
  /** the commit its instructions were built from; none = built before rules were loaded */
  commit?: string;
  behind: boolean;
}

export interface RulesView {
  enabled: boolean;
  source?: string;
  branch?: string;
  /** the commit this node is held at, if the owner holds it */
  pin?: string;
  signers: { id: string; fingerprint: string }[];
  loaded: boolean;
  head?: RulesChange;
  /** the pinned signer of the commit in force; none = not checked */
  signature?: string;
  accepted_at?: string;
  fetched_at?: string;
  problem?: string;
  rejected?: { commit: string; author?: string; reason: string; at: string };
  docs: RulesDoc[];
  agents: RulesAgent[];
}

export function shortCommit(sha: string | undefined): string {
  return (sha ?? "").slice(0, 12);
}

/** The card's first line: are rules in force, and is anything wrong. */
export function rulesHeadline(v: RulesView): { tone: "ok" | "warn" | "off"; text: string } {
  if (!v.enabled) return { tone: "off", text: "This machine reads no fleet rules: agents get their profile's instructions only." };
  if (!v.loaded) {
    return { tone: "warn", text: v.problem ? `No fleet rules are in force yet: ${v.problem}` : "No fleet rules are in force yet: the source was not looked at." };
  }
  if (v.rejected) {
    return { tone: "warn", text: `A newer commit (${shortCommit(v.rejected.commit)}) was not taken: ${v.rejected.reason}. The rules in force stay at ${shortCommit(v.head?.commit)}.` };
  }
  if (v.problem) return { tone: "warn", text: `The rules in force stay at ${shortCommit(v.head?.commit)}: ${v.problem}` };
  return { tone: "ok", text: `Fleet rules are in force at commit ${shortCommit(v.head?.commit)}.` };
}

/** What is known about who made the commit in force. */
export function signatureLine(v: RulesView): string {
  if (v.signature) return `Signature checked: made with the pinned key "${v.signature}".`;
  if (v.signers.length > 0) return "Not valid for the keys pinned here: this commit was taken before they were pinned.";
  return "Signature not checked: no signing keys are pinned on this machine, so any commit of the source is taken.";
}

const GROUPS: [prefix: string, title: string][] = [
  ["rules/", "Standing rules"],
  ["apps/", "Connected apps"],
  ["pages/", "Pages"],
];

/** The documents by folder, in a fixed order; what is elsewhere comes last. */
export function docGroups(docs: RulesDoc[]): { title: string; docs: RulesDoc[] }[] {
  const out = GROUPS.map(([prefix, title]) => ({ title, docs: docs.filter((d) => d.name.startsWith(prefix)) }));
  const other = docs.filter((d) => !GROUPS.some(([prefix]) => d.name.startsWith(prefix)));
  if (other.length) out.push({ title: "Other", docs: other });
  return out.filter((g) => g.docs.length > 0);
}

/** The running agents whose instructions were built from other rules than those in force. */
export function behindAgents(v: RulesView): string[] {
  return v.agents.filter((a) => a.behind).map((a) => a.name);
}

/** "3 agents run on older rules: a, b, c" (at most five names). */
export function behindLine(v: RulesView): string | null {
  const names = behindAgents(v);
  if (!v.loaded || names.length === 0) return null;
  const shown = names.slice(0, 5).join(", ");
  const more = names.length > 5 ? ` and ${names.length - 5} more` : "";
  return `${names.length} running ${names.length === 1 ? "agent was" : "agents were"} started on older rules: ${shown}${more}. They get the rules in force at their next start, or now with rules_read.`;
}
