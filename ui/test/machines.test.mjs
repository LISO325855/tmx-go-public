// A machine's page (src/lib/machines-format.ts): the roster of one of the owner's other machines
// and the relay requests that work its agents.
import test from "node:test";
import assert from "node:assert/strict";
import {
  byProject,
  dotState,
  launchProblem,
  launchRequest,
  machineSessions,
  resting,
  sessionRequest,
  sessionsLine,
} from "../src/lib/machines-format.ts";

const roster = [
  { name: "zeta", host: "hostc", state: "exited", project: "tmx" },
  { name: "tmx-lead", host: "Hostc", state: "working", project: "tmx", tier: "lead" },
  { name: "alpha", host: "hostc", state: "dialog" },
  { name: "head", host: "hosta", state: "idle" },
  { name: "bravo", host: "hostc", state: "idle", project: "blackcode" },
];

test("dotState: both daemons' words; an unknown word is idle, never working or waiting", () => {
  assert.equal(dotState("dialog"), "waiting"); // Go's word for waiting on the owner
  assert.equal(dotState("exited"), "stopped"); // Go's word for stopped
  assert.equal(dotState("stopped"), "stopped"); // Python's
  assert.equal(dotState("working"), "working");
  assert.equal(dotState("paused"), "paused");
  assert.equal(dotState("something-new"), "idle");
  assert.equal(dotState(""), "idle");
  assert.equal(resting("exited"), true);
  assert.equal(resting("error"), true);
  assert.equal(resting("idle"), false);
  assert.equal(resting("something-new"), false);
});

test("machineSessions: one machine's agents, running first, host names without case", () => {
  assert.deepEqual(machineSessions(roster, "hostc").map((s) => s.name), ["alpha", "bravo", "tmx-lead", "zeta"]);
  assert.deepEqual(machineSessions(roster, "HOSTA").map((s) => s.name), ["head"]);
  assert.deepEqual(machineSessions(roster, "hostb"), []);
  assert.deepEqual(machineSessions(null, "hostc"), []);
  assert.equal(roster[0].name, "zeta"); // the roster itself is not reordered
});

test("byProject and sessionsLine: grouped by project name, no project last", () => {
  const agents = machineSessions(roster, "hostc");
  assert.deepEqual(byProject(agents).map((g) => [g.project, g.agents.map((a) => a.name)]), [
    ["blackcode", ["bravo"]],
    ["tmx", ["tmx-lead", "zeta"]],
    ["", ["alpha"]],
  ]);
  assert.equal(sessionsLine(agents), "4 agents · 3 running · 1 need you");
  assert.equal(sessionsLine([{ name: "a", host: "x", state: "exited" }]), "1 agent · 0 running");
});

test("sessionRequest: only the session routes the relay admits, only for a name that fits a path", () => {
  assert.deepEqual(sessionRequest("hostc", "tmx-lead", "stop"), { host: "hostc", method: "POST", path: "/api/sessions/tmx-lead/stop" });
  assert.deepEqual(sessionRequest("hostc", "tmx-lead", "start"), { host: "hostc", method: "POST", path: "/api/sessions/tmx-lead/start" });
  assert.deepEqual(sessionRequest("hostc", "tmx-lead", "restart"), { host: "hostc", method: "POST", path: "/api/sessions/tmx-lead/restart" });
  assert.deepEqual(sessionRequest("hostc", "tmx-lead", "screen"), { host: "hostc", method: "GET", path: "/api/sessions/tmx-lead/screen?history=200" });
  assert.deepEqual(sessionRequest("hostc", "tmx-lead", "keys", "enter"), {
    host: "hostc",
    method: "POST",
    path: "/api/sessions/tmx-lead/keys",
    body: { keys: "enter" },
  });
  assert.equal(sessionRequest("hostc", "tmx-lead", "keys"), null); // no key, no request
  // A name from another machine never becomes another path.
  for (const name of ["../config", "a/b", "x?y=1", "", "a b", "%2e%2e", "a".repeat(81)]) {
    assert.equal(sessionRequest("hostc", name, "stop"), null, name);
  }
  assert.equal(sessionRequest("", "tmx-lead", "stop"), null);
});

test("launchRequest: Go's launch body for a Go machine, Python's for a Python machine", () => {
  const form = { name: " docs-writer ", cwd: " D:/work/docs ", role: "writes docs", task: "Write the guide." };
  assert.deepEqual(launchRequest("hosta-go", form, true), {
    host: "hosta-go",
    method: "POST",
    path: "/api/sessions",
    body: { name: "docs-writer", cwd: "D:/work/docs", role: "writes docs", brief: "Write the guide." },
  });
  assert.deepEqual(launchRequest("hostc", form, false), {
    host: "hostc",
    method: "POST",
    path: "/api/sessions",
    body: { name: "docs-writer", cwd: "D:/work/docs", role: "writes docs", adapter: "claude", prompt: "Write the guide." },
  });
  // Empty fields are left out: that machine's own defaults apply.
  assert.deepEqual(launchRequest("hosta-go", { name: "x", cwd: "", role: "", task: "" }, true).body, { name: "x" });
});

test("launchProblem: a name always; the folder on a Python machine", () => {
  const ok = { name: "docs-writer", cwd: "", role: "", task: "" };
  assert.equal(launchProblem(ok, true), null);
  assert.equal(launchProblem(ok, false), "Give the folder on that machine it should work in.");
  assert.equal(launchProblem({ ...ok, cwd: "D:/x" }, false), null);
  assert.equal(launchProblem({ ...ok, name: "  " }, true), "Give the agent a name.");
  assert.equal(launchProblem({ ...ok, name: "Docs Writer" }, true), "A name is lowercase letters, digits and dashes, up to 40.");
});
