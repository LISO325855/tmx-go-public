// "Shells & scripts" inside a project (classic UI profile): the machine registry's entries in the
// owner's words and reading order (lib/machine-registry.ts), and the screen that shows them.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { canAct, isAlive, isOrphan, kindWord, listedProcesses, registryMissing, registrySummary, stateWord } from "../src/lib/machine-registry.ts";

const e = (id, x = {}) => ({ id, kind: "process", what: id, command: "node", since: 1000, state: "running", actions: [], ...x });

test("kinds and states are said in plain words", () => {
  assert.equal(kindWord("shell"), "Shell");
  assert.equal(kindWord("tool"), "Tool");
  assert.equal(kindWord("system_tool"), "tmx tool");
  assert.equal(kindWord("process"), "Script");
  assert.equal(kindWord("something-new"), "something-new");
  assert.equal(stateWord({ state: "running" }), "Running");
  assert.equal(stateWord({ state: "detached" }), "Running on its own");
  assert.equal(stateWord({ state: "orphan" }), "Left running");
  assert.equal(stateWord({ state: "running", orphan: true }), "Left running", "the flag wins over the state");
  assert.equal(stateWord({ state: "exited" }), "Ended");
  assert.equal(stateWord({ state: "retrying" }), "Restarting");
});

test("what was left running comes first, then what runs, then what ended; the newest first inside each", () => {
  const list = [
    e("ended", { state: "exited", since: 9000 }),
    e("old", { since: 1000 }),
    e("new", { since: 5000 }),
    e("orphan-old", { state: "orphan", orphan: true, since: 10 }),
    e("stopped", { state: "stopped", since: 9500 }),
    e("orphan-new", { orphan: true, since: 20 }),
    e("detached", { state: "detached", since: 3000 }),
  ];
  const before = list.map((x) => x.id);
  assert.deepEqual(listedProcesses(list).map((x) => x.id), ["orphan-new", "orphan-old", "new", "detached", "old", "stopped", "ended"]);
  assert.deepEqual(list.map((x) => x.id), before, "the given list is not reordered");
  assert.deepEqual(listedProcesses(null), []);
  assert.equal(isOrphan(list[3]), true);
  assert.equal(isAlive(list[0]), false);
  assert.equal(isAlive(list[6]), true);
});

test("only the buttons the daemon offers are shown", () => {
  assert.equal(canAct(e("a", { actions: ["stop"] }), "stop"), true);
  assert.equal(canAct(e("a", { actions: ["stop"] }), "restart"), false);
  assert.equal(canAct(e("a"), "stop"), false);
  assert.equal(canAct({}, "stop"), false, "an entry without the field offers nothing");
});

test("the count on top", () => {
  assert.equal(registrySummary([]), "Nothing is running for this project.");
  assert.equal(registrySummary(undefined), "Nothing is running for this project.");
  assert.equal(registrySummary([e("a"), e("b", { state: "detached" })]), "2 running");
  assert.equal(registrySummary([e("a"), e("b", { orphan: true }), e("c", { state: "exited" })]), "1 running · 1 left running · 1 ended");
  assert.equal(registrySummary([e("c", { state: "stopped" })]), "1 ended");
});

test("a daemon without the registry is told from another failure", () => {
  assert.equal(registryMissing({ status: 404 }), true);
  assert.equal(registryMissing({ status: 500 }), false);
  assert.equal(registryMissing(new Error("offline")), false);
  assert.equal(registryMissing(null), false);
});

test("the screen: visible while loading or unavailable, Stop asked twice, ids in the body", () => {
  const read = (p) => readFileSync(new URL(`../src/${p}`, import.meta.url), "utf8");
  const page = read("pages/project.tsx");
  assert.match(page, /<ProjectShells projectId=\{project\.id\} \/>/);
  const view = read("components/classic/project-shells.tsx");
  assert.match(view, /if \(missing \|\| error \|\| entries === null\) return/);
  assert.match(view, /Loading shells and scripts/);
  assert.match(view, /unavailable on this machine/);
  assert.match(view, /if \(registryMissing\(err\)\) setMissing\(true\);/);
  assert.match(view, /onClick=\{\(\) => setAsking\(e\.id\)\}/, "Stop asks first");
  assert.match(view, /Yes, stop/);
  assert.match(view, /canAct\(e, "restart"\) &&/);
  assert.match(view, /canAct\(e, "stop"\) &&/);
  assert.doesNotMatch(view, /window\.confirm|confirm\(/, "no browser dialog");
  const api = read("lib/api.ts");
  assert.match(api, /registryStop: \(id: string\) => t\(\)\.request<\{ ok: boolean \}>\("POST", "\/api\/registry\/stop", \{ id \}\)/);
  assert.match(api, /registryRestart: \(id: string\) => t\(\)\.request<MachineProcess>\("POST", "\/api\/registry\/restart", \{ id \}\)/);
  assert.match(api, /"GET", `\/api\/registry\?project=\$\{enc\(project\)\}`/);
});
