// The agent list the Python way (src/lib/classic-agents.ts; parity C1, C2, C4; architect, 17 Nov
// 15:05: "agent list: all except stopped").
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import {
  agentMatches,
  agentSecondLine,
  classicAgentVisible,
  filterProjectAgents,
  isStoppedState,
  savedShowStopped,
  saveShowStopped,
  stateWords,
  stoppedAgentCount,
  stoppedLine,
} from "../src/lib/classic-agents.ts";
import { agentVisible } from "../src/lib/active-agents.ts";

const src = (p) => readFileSync(new URL(`../src/${p}`, import.meta.url), "utf8");

test("classic shows every agent except shelved and stopped ones; quiet and failed agents stay", () => {
  for (const state of ["working", "starting", "waiting", "queued", "paused", "idle", "error"]) {
    assert.equal(classicAgentVisible({ state, level: "worker" }, false), true, state);
  }
  for (const state of ["stopped", "exited"]) {
    assert.equal(isStoppedState(state), true);
    assert.equal(classicAgentVisible({ state, level: "worker" }, false), false, state);
    assert.equal(classicAgentVisible({ state, level: "worker" }, true), true, `${state} when shown`);
  }
  // shelved agents never show in the list, whatever the choice
  assert.equal(classicAgentVisible({ state: "idle", level: "worker", shelved_at: 5 }, true), false);
  // the Architect is always there
  assert.equal(classicAgentVisible({ state: "stopped", level: "head" }, false), true);
});

test("the difference to the default dashboard's 2-hour rule is exactly the quiet and the failed agent", () => {
  const now = 10_000_000_000;
  const quiet = { state: "idle", level: "worker", last_activity_at: now - 5 * 3600_000, created_at: 1 };
  const failed = { state: "error", level: "worker", last_activity_at: now, created_at: 1 };
  assert.equal(agentVisible(quiet, false, now), false, "default: a quiet agent vanishes");
  assert.equal(agentVisible(failed, false, now), false, "default: a failed agent vanishes");
  assert.equal(classicAgentVisible(quiet, false), true);
  assert.equal(classicAgentVisible(failed, false), true);
});

test("the stopped line counts stopped agents, not the head and not shelved ones", () => {
  const agents = [
    { state: "stopped", level: "worker" },
    { state: "exited", level: "lead" },
    { state: "stopped", level: "head" },
    { state: "stopped", level: "worker", shelved_at: 9 },
    { state: "idle", level: "worker" },
    { state: "error", level: "worker" },
  ];
  assert.equal(stoppedAgentCount(agents), 2);
  assert.equal(stoppedAgentCount([]), 0);
  assert.deepEqual(stoppedLine(2, false), { text: "2 stopped agents hidden", action: "show" });
  assert.deepEqual(stoppedLine(1, false), { text: "1 stopped agent hidden", action: "show" });
  assert.deepEqual(stoppedLine(3, true), { text: "3 stopped agents shown", action: "hide" });
  assert.equal(stoppedLine(0, false), null);
  assert.equal(stoppedLine(0, true), null);
});

test("the choice to show stopped agents is remembered on the device", () => {
  const store = new Map();
  globalThis.localStorage = {
    getItem: (k) => (store.has(k) ? store.get(k) : null),
    setItem: (k, v) => void store.set(k, String(v)),
    removeItem: (k) => void store.delete(k),
  };
  try {
    assert.equal(savedShowStopped(), false);
    saveShowStopped(true);
    assert.equal(savedShowStopped(), true);
    saveShowStopped(false);
    assert.equal(savedShowStopped(), false);
    assert.equal(store.size, 0);
  } finally {
    delete globalThis.localStorage;
  }
  // no storage at all: hidden, and saving does not throw
  assert.equal(savedShowStopped(), false);
  saveShowStopped(true);
});

test("the state in Python's words", () => {
  assert.equal(stateWords({ state: "waiting" }), "needs you");
  assert.equal(stateWords({ state: "waiting" }, true), "asks you");
  assert.equal(stateWords({ state: "working" }), "working");
  assert.equal(stateWords({ state: "idle" }), "idle");
  assert.equal(stateWords({ state: "error" }), "failed");
  assert.equal(stateWords({ state: "exited" }), "ended");
  assert.equal(stateWords({ state: "idle", shelved_at: 3 }), "shelved");
  assert.equal(stateWords({ state: "something-new" }), "something-new");
});

test("the second line is the role, then what the agent is doing", () => {
  assert.equal(agentSecondLine({ role: "backend", level: "worker" }, "Edit api.ts"), "backend · Edit api.ts");
  assert.equal(agentSecondLine({ role: "", level: "lead" }, ""), "lead");
  assert.equal(agentSecondLine({ level: "worker", profile: "reviewer" }, "Read a message: hi"), "reviewer · Read a message: hi");
  assert.equal(agentSecondLine({ level: "worker", profile: "worker" }, undefined), "worker");
  assert.equal(agentSecondLine({ level: "head" }, null), "architect");
});

test("the search box finds agents by name, role, harness, project and machine", () => {
  const a = { id: "1", name: "api-tests", state: "idle", level: "worker", harness: "codex", role: "backend", project_id: "p" };
  assert.equal(agentMatches(a, ""), true);
  assert.equal(agentMatches(a, "  "), true);
  assert.equal(agentMatches(a, "API"), true);
  assert.equal(agentMatches(a, "backend"), true);
  assert.equal(agentMatches(a, "codex"), true);
  assert.equal(agentMatches(a, "billing", "billing-portal"), true);
  assert.equal(agentMatches(a, "hostc", "billing-portal", "hostc"), true);
  assert.equal(agentMatches(a, "nothing-like-it", "billing-portal", "hostc"), false);
});

test("a project shows when it or one of its agents matches; only the matching agents then", () => {
  const agents = [
    { id: "1", name: "api-tests", state: "idle", level: "worker" },
    { id: "2", name: "docs", state: "idle", level: "worker" },
  ];
  assert.deepEqual(filterProjectAgents(agents, "", "billing-portal"), { show: true, agents });
  assert.deepEqual(filterProjectAgents(agents, "api", "billing-portal"), { show: true, agents: [agents[0]] });
  assert.deepEqual(filterProjectAgents(agents, "billing", "billing-portal"), { show: true, agents }, "the project's name shows all of it");
  assert.deepEqual(filterProjectAgents(agents, "lab", "billing-portal", "lab-mac"), { show: true, agents });
  assert.deepEqual(filterProjectAgents(agents, "zzz", "billing-portal", "lab-mac"), { show: false, agents: [] });
  assert.deepEqual(filterProjectAgents([], "billing", "billing-portal"), { show: true, agents: [] });
});

test("the list rule and the row are the classic ones only under classic", () => {
  const hook = src("hooks/use-visible-agents.ts");
  assert.match(hook, /classic \? classicAgentVisible\(a, showStopped\) : agentVisible\(a, showAll, now\)/);
  const side = src("components/sidebar.tsx");
  assert.match(side, /if \(classic\) return <ClassicAgentRow /, "the row");
  assert.match(side, /\{classic && <ClassicSearchBox /, "the search box above the list");
  assert.match(side, /\{classic && <StoppedAgentsLine \/>\}/);
  assert.match(side, /\{!classic && \(\s*<span className="ml-3">\s*<AgentListFilter \/>/, "Show all is the default dashboard's");
  assert.match(side, /label=\{classic \? "Workspace" : "Home"\}/);
});
