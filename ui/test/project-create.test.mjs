import test from "node:test";
import assert from "node:assert/strict";
import { createProjectPayload, projectLeadHarnesses } from "../src/lib/project-create.ts";

test("project creation sends the selected harness and keeps optional fields optional", () => {
  assert.deepEqual(createProjectPayload({ name: "billing", harness: "codex" }), {
    name: "billing",
    harness: "codex",
  });
});

test("project creation preserves a custom folder and brief with the selected harness", () => {
  assert.deepEqual(createProjectPayload({
    name: "billing",
    harness: "codex",
    folder: "D:/work/billing",
    brief: "Build the billing portal",
  }), {
    name: "billing",
    harness: "codex",
    folder: "D:/work/billing",
    brief: "Build the billing portal",
  });
});

test("project lead choices include chat harnesses and exclude terminal-only harnesses", () => {
  const harnesses = [
    { name: "claude", modes: ["chat", "terminal"], installed: true },
    { name: "codex", modes: ["chat", "terminal"], installed: false },
    { name: "shell", modes: ["terminal"], installed: true },
  ];
  assert.deepEqual(projectLeadHarnesses(harnesses).map((h) => h.name), ["claude", "codex"]);
});

// "+ Agent" from anywhere (Python's header button; parity A5): the project is a field of the form.
test("the New agent form offers every project that is not archived, active ones first", async () => {
  const { newAgentProjects, pickNewAgentProject } = await import("../src/lib/project-create.ts");
  const projects = [
    { id: "p3", name: "zeta", status: "active" },
    { id: "p1", name: "alpha", status: "paused" },
    { id: "p4", name: "old", status: "archived" },
    { id: "p2", name: "beta", status: "active" },
  ];
  assert.deepEqual(newAgentProjects(projects).map((p) => p.id), ["p2", "p3", "p1"]);
  assert.deepEqual(projects.map((p) => p.id), ["p3", "p1", "p4", "p2"], "the caller's list is not reordered");
  // it starts on the project of the page you are on, when that can take an agent
  assert.equal(pickNewAgentProject(projects, "p1"), "p1");
  assert.equal(pickNewAgentProject(projects, "p4"), "p2", "never an archived project");
  assert.equal(pickNewAgentProject(projects, "gone"), "p2");
  assert.equal(pickNewAgentProject(projects, undefined), "p2");
  assert.equal(pickNewAgentProject([], "p1"), "");
  assert.equal(pickNewAgentProject([{ id: "p4", name: "old", status: "archived" }]), "");
});

test("the New agent dialog can be opened from anywhere and then asks for the project", async () => {
  const { readFileSync } = await import("node:fs");
  const read = (p) => readFileSync(new URL(`../src/${p}`, import.meta.url), "utf8");
  const uiState = read("lib/ui.ts");
  assert.match(uiState, /openNewAgent: \(v = true, projectId\?: string\) => useUI\.setState\(\{ newAgent: v \? \{ projectId \} : false, palette: false, mobileNav: false \}\)/);
  const form = read("components/new-agent-dialog.tsx");
  assert.match(form, /export function NewAgentHost\(\)/);
  assert.match(form, /\{!fixed && \(\s*<div className="grid gap-1\.5">\s*<Label htmlFor="na-project">Project<\/Label>/, "the Project field only without a fixed project");
  assert.match(form, /const canCreate = nameOK && !!project && /, "no agent without a project");
  assert.match(form, /project_id: project\.id,/);
  assert.match(read("App.tsx"), /<NewAgentHost \/>/);
  // within reach: the sidebar's "+" and the Ctrl K search, under classic; the default dashboard keeps New project
  const side = read("components/sidebar.tsx");
  assert.match(side, /aria-label=\{classic \? "New agent" : "New project"\}/);
  assert.equal(side.split("classic ? ui.openNewAgent(true) : ui.openNewProject(true)").length - 1, 2, "the + and the row under the list");
  assert.match(read("components/command-palette.tsx"), /\{classic && \(\s*<CommandItem value="new agent create start launch add" onSelect=\{\(\) => ui\.openNewAgent\(true\)\}>/);
  // a project page still opens it for that project
  assert.match(read("pages/project.tsx"), /<NewAgentDialog project=\{project\}/);
});
