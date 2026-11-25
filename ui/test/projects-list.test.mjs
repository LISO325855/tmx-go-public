// The projects list in the classic sidebar (src/lib/classic-agents.ts; the owner, 17 Nov 19:03:
// "the projects list with ~40 projects is messy… all of this is about organizing information,
// filtering it down to the human brain's capability"). Collapsed by default, one line per
// project; a project opens by itself only while one of its agents is busy; one toggle,
// "collapse all" / "expand active"; projects with no agent hide behind "show all (N)".
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { emptyProjectsLine, isBusyState, projectListed, projectOpen, projectsModeAction, savedProjectsMode, saveProjectsMode } from "../src/lib/classic-agents.ts";

const side = readFileSync(new URL("../src/components/sidebar.tsx", import.meta.url), "utf8");
const base = { mode: "active", busy: false, current: false, searching: false };

test("busy means working, starting, or waiting for the owner", () => {
  for (const s of ["working", "starting", "waiting"]) assert.equal(isBusyState(s), true, s);
  for (const s of ["idle", "queued", "paused", "error", "stopped", "exited"]) assert.equal(isBusyState(s), false, s);
});

test("collapsed by default; only a project with a busy agent opens by itself", () => {
  assert.equal(projectOpen(base), false, "a quiet project is one line");
  assert.equal(projectOpen({ ...base, busy: true }), true);
});

test("the project being looked at is open, so the place is not lost", () => {
  assert.equal(projectOpen({ ...base, current: true }), true);
});

test("collapse all closes every project, the busy ones and the current one too", () => {
  assert.equal(projectOpen({ ...base, mode: "collapsed", busy: true }), false);
  assert.equal(projectOpen({ ...base, mode: "collapsed", current: true }), false);
});

test("a project's own chevron wins, in both modes, until the mode is switched", () => {
  assert.equal(projectOpen({ ...base, override: true }), true, "opened by hand though quiet");
  assert.equal(projectOpen({ ...base, busy: true, override: false }), false, "closed by hand though busy");
  assert.equal(projectOpen({ ...base, mode: "collapsed", override: true }), true);
  assert.match(side, /setOpened\(\{\}\); \/\/ the toggle speaks for every project again/);
});

test("a search opens what it found, whatever the mode or the chevrons", () => {
  assert.equal(projectOpen({ ...base, mode: "collapsed", searching: true, override: false }), true);
});

test("empty projects are put away behind show all (N); the current one and a search still show them", () => {
  const p = { members: 0, current: false, showEmpty: false, searching: false };
  assert.equal(projectListed(p), false);
  assert.equal(projectListed({ ...p, members: 2 }), true);
  assert.equal(projectListed({ ...p, showEmpty: true }), true);
  assert.equal(projectListed({ ...p, current: true }), true);
  assert.equal(projectListed({ ...p, searching: true }), true);
  assert.equal(emptyProjectsLine(0, false), null);
  assert.equal(emptyProjectsLine(27, false), "show all (27)");
  assert.equal(emptyProjectsLine(27, true), "hide empty (27)");
});

test("the toggle says what a click does, and the mode is remembered on the device", () => {
  assert.equal(projectsModeAction("active"), "collapse all");
  assert.equal(projectsModeAction("collapsed"), "expand active");
  const store = new Map();
  globalThis.localStorage = { getItem: (k) => (store.has(k) ? store.get(k) : null), setItem: (k, v) => void store.set(k, String(v)), removeItem: (k) => void store.delete(k) };
  try {
    assert.equal(savedProjectsMode(), "active", "expand active is the default");
    saveProjectsMode("collapsed");
    assert.equal(savedProjectsMode(), "collapsed");
    saveProjectsMode("active");
    assert.equal(savedProjectsMode(), "active");
    assert.equal(store.size, 0);
  } finally {
    delete globalThis.localStorage;
  }
  assert.equal(savedProjectsMode(), "active", "no storage: the default");
});

test("only the classic sidebar lists projects this way; the default one is as it was", () => {
  assert.match(side, /\{!classic &&\s*sorted\.map\(\(p\) => \(\s*<ProjectItem key=\{p\.id\} project=\{p\} agents=\{agents\} path=\{path\} collapsed=\{collapsed\[p\.id\] \?\? p\.status === "archived"\} onToggle=\{\(\) => toggle\(p\.id\)\}/);
  assert.match(side, /\{classic &&\s*listing\.rows\s*\.filter\(\(r\) => r\.listed\)/);
  assert.match(side, /collapsed=\{!r\.open\}/);
  assert.match(side, /data-projects-mode=\{projectsMode\}/);
  assert.match(side, /\{classic && emptyProjectsLine\(listing\.empty, showEmpty\) && !query\.trim\(\) && \(/);
});
