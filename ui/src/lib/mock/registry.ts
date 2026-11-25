// The machine registry in the mock daemon (internal/fleet/registry.go; lib/machine-registry.ts):
// what the billing project's agents have running, one of them left behind by a stopped agent.
import { ApiError } from "../transport";
import type { MachineProcess, MachineRegistry } from "../machine-registry";
import type { MockWorld } from "./data";

const min = 60_000;

export class MockRegistry {
  private entries: MachineProcess[] | null = null;

  constructor(private w: MockWorld) {}

  private all(): MachineProcess[] {
    if (this.entries) return this.entries;
    const now = Date.now();
    const p = "pbl7n2c4ka";
    const project = this.w.projects.get(p)?.name ?? "billing-portal";
    const name = (id: string) => this.w.agents.get(id)?.name ?? id;
    this.entries = [
      { id: "shell:sh5r7t9v3w", kind: "shell", what: "Dev server", command: "zsh", agent_id: "sh5r7t9v3w", agent: name("sh5r7t9v3w"), project_id: p, project, since: now - 70 * min, state: "running", pid: 4812, actions: [] },
      { id: "tool:t7k2m9x4", kind: "tool", what: "Stripe webhook listener", command: "stripe", agent_id: "ld8q2w4e6r", agent: name("ld8q2w4e6r"), project_id: p, project, since: now - 41 * min, state: "running", pid: 5120, restarts: 1, actions: ["stop", "restart"] },
      { id: "pid:6344:1794900000", kind: "process", what: "node (seen under codex-api)", command: "node", agent_id: "cx8m2n4b6v", agent: name("cx8m2n4b6v"), project_id: p, project, since: now - 26 * min, state: "running", pid: 6344, actions: ["stop"] },
      { id: "pid:7012:1794890000", kind: "process", what: "vite dev server", command: "node", agent: "ui-worker-2", project_id: p, project, since: now - 3 * 60 * min, state: "orphan", pid: 7012, orphan: true, why: "Its agent (ui-worker-2) is gone; the process still runs.", actions: ["stop"] },
      { id: "tool:t1a2b3c4", kind: "tool", what: "Test watcher", command: "pnpm", agent_id: "ld8q2w4e6r", agent: name("ld8q2w4e6r"), project_id: p, project, since: now - 5 * 60 * min, state: "exited", actions: ["restart"] },
    ];
    return this.entries;
  }

  route(method: string, action: string | undefined, q: URLSearchParams, body: unknown): MachineRegistry | MachineProcess | { ok: boolean } {
    const all = this.all();
    if (!action && method === "GET") {
      const project = q.get("project");
      const entries = project ? all.filter((e) => e.project_id === project) : all;
      return { entries, orphans: entries.filter((e) => e.orphan).length };
    }
    const id = (body as { id?: string } | null)?.id ?? "";
    const e = all.find((x) => x.id === id);
    if (action === "stop" && method === "POST") {
      if (!e) throw new ApiError(404, "It is gone already.");
      if (!e.actions.includes("stop")) throw new ApiError(403, "This one cannot be stopped from here.");
      if (e.kind === "tool") Object.assign(e, { state: "stopped", pid: undefined, orphan: false, why: "", actions: ["restart"] });
      else all.splice(all.indexOf(e), 1);
      return { ok: true };
    }
    if (action === "restart" && method === "POST") {
      if (!e || e.kind !== "tool") throw new ApiError(404, "No such tool.");
      Object.assign(e, { state: "running", since: Date.now(), restarts: (e.restarts ?? 0) + 1, actions: ["stop", "restart"] });
      return e;
    }
    throw new ApiError(404, "Not found.");
  }
}
