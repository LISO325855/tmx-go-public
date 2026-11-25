// Mock of the fleet rules routes (GET /api/rules, /api/rules/doc, POST /api/rules/refresh,
// PUT /api/rules/config): a small source with a commit in force and one agent on older rules.
import { ApiError } from "../transport";
import type { RulesDoc, RulesView } from "../fleet-rules-format";
import { NOT_MINE } from "./resources";

const HOUR = 3_600_000;
const COMMIT = "4f1c2a9b7d3e8a6c5b4d3e2f1a0b9c8d7e6f5a4b";
const OLDER = "9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b";

function change(commit: string, hoursAgo: number, subject: string) {
  return { commit, author: "head", time: new Date(Date.now() - hoursAgo * HOUR).toISOString(), subject };
}

const DOCS: RulesDoc[] = [
  {
    name: "rules/core", path: "rules/core.md", title: "Core rules", summary: "What every agent keeps to.", size: 420,
    last_change: change(COMMIT, 2, "reports name the files changed"),
    body: "# Core rules\n\n- Report what you verified, not what you assume.\n- Reports name the files changed.\n",
  },
  {
    name: "rules/head", path: "rules/head.md", title: "Head", size: 310, last_change: change(OLDER, 30, "first rules"),
    body: "# Head\n\nKeep the owner informed with short updates.\n",
  },
  {
    name: "apps/tickets", path: "apps/tickets.md", title: "Ticket app", summary: "Issues and tasks of the fleet.", size: 260,
    meta: { name: "Ticket app", summary: "Issues and tasks of the fleet.", state: "works", ask: "the head" },
    last_change: change(OLDER, 30, "first rules"),
    body: "Open a ticket with the tickets tool; one ticket per piece of work.\n",
  },
  {
    name: "pages/operations", path: "pages/operations.md", title: "Operations", summary: "How the fleet runs day to day.", size: 540,
    last_change: change(OLDER, 30, "first rules"),
    body: "# Operations\n\nWho does what, and where to look first.\n",
  },
];

export class MockFleetRules {
  private view: RulesView = {
    enabled: true, source: "https://github.com/example/fleet-rules.git", branch: "main", signers: [],
    loaded: true, head: change(COMMIT, 2, "reports name the files changed"),
    accepted_at: new Date(Date.now() - 2 * HOUR).toISOString(), fetched_at: new Date(Date.now() - 4 * 60_000).toISOString(),
    docs: DOCS.map(({ body: _body, ...d }) => d),
    agents: [
      { id: "a-head", name: "head", state: "idle", commit: COMMIT, behind: false },
      { id: "a-worker", name: "api-worker", state: "working", commit: OLDER, behind: true },
    ],
  };

  route(method: string, parts: string[], q: URLSearchParams, body: unknown): unknown {
    if (parts[0] !== "rules") return NOT_MINE;
    const action = parts[1];
    if (!action && method === "GET") return structuredClone(this.view);
    if (action === "doc" && method === "GET") {
      const name = (q.get("name") ?? "").replace(/\.md$/, "");
      const d = DOCS.find((x) => x.name === name);
      if (!d) throw new ApiError(404, "there is no such document in the fleet rules");
      return structuredClone(d);
    }
    if (action === "apps" && method === "GET") return structuredClone(DOCS.filter((d) => d.name.startsWith("apps/")));
    if (action === "refresh" && method === "POST") {
      this.view.fetched_at = new Date().toISOString();
      return structuredClone(this.view);
    }
    if (action === "config" && method === "PUT") {
      const b = (body ?? {}) as { pin?: string };
      if (b.pin !== undefined) this.view.pin = b.pin || undefined;
      return structuredClone(this.view);
    }
    throw new ApiError(404, "Not found.");
  }
}
