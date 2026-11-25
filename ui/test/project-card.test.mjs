// The project card and the fuller project page under the classic UI profile (the owner, 17 Nov
// 19:01: "like Python's": description, latest status, progress bar, agents / working,
// coordinator, the row of little agent heads; the page with final outputs, team, files).
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { cardFaces, downloadAgent, fileName, projectCounts, projectFolders, splitProjects } from "../src/lib/project-page.ts";

const src = (p) => readFileSync(new URL(`../src/${p}`, import.meta.url), "utf8");
const m = (id, name, state = "idle", extra = {}) => ({ id, name, state, level: "worker", ...extra });

test("a file is called by its last part, on either kind of path", () => {
  assert.equal(fileName("docs/billing.md"), "billing.md");
  assert.equal(fileName("C:\\Users\\x\\out\\report.pdf"), "report.pdf");
  assert.equal(fileName("D:/out/build/"), "build");
  assert.equal(fileName("setup.exe"), "setup.exe");
  assert.equal(fileName(""), "");
  assert.equal(fileName(undefined), "");
});

test("the card counts the agents that are not shelved, and how many are working", () => {
  const members = [m("a", "a", "working"), m("b", "b", "starting"), m("c", "c", "idle"), m("d", "d", "stopped"), m("e", "e", "idle", { shelved_at: 5 })];
  assert.deepEqual(projectCounts(members), { agents: 4, working: 2, shelved: 1 });
  assert.deepEqual(projectCounts([]), { agents: 0, working: 0, shelved: 0 });
});

test("the row of heads: the coordinator first, then by name, at most 12", () => {
  const members = [m("3", "zed"), m("1", "lead-x"), m("2", "amy")];
  assert.deepEqual(cardFaces(members, "1").shown.map((x) => x.name), ["lead-x", "amy", "zed"]);
  assert.deepEqual(cardFaces(members, null).shown.map((x) => x.name), ["amy", "lead-x", "zed"]);
  const many = Array.from({ length: 15 }, (_, i) => m(String(i), `w${String(i).padStart(2, "0")}`));
  const f = cardFaces(many, "14");
  assert.equal(f.shown.length, 12);
  assert.equal(f.shown[0].name, "w14");
  assert.equal(f.more, 3);
  assert.deepEqual(members.map((x) => x.name), ["zed", "lead-x", "amy"], "the caller's list is not reordered");
});

test("Python's two shelves: active projects, and the ones put aside", () => {
  const ps = [
    { name: "zeta", status: "active" },
    { name: "old", status: "archived" },
    { name: "alpha", status: "active" },
    { name: "later", status: "paused" },
  ];
  const { active, shelf } = splitProjects(ps);
  assert.deepEqual(active.map((p) => p.name), ["alpha", "zeta"]);
  assert.deepEqual(shelf.map((p) => p.name), ["later", "old"]);
});

test("an output is downloaded from the member that named it, else from the coordinator; a link never", () => {
  const known = (id) => ["lead", "w1"].includes(id);
  assert.equal(downloadAgent({ path: "out/report.pdf", agent_id: "w1" }, "lead", known), "w1");
  assert.equal(downloadAgent({ path: "out/report.pdf" }, "lead", known), "lead", "the shared output lives in the project's folder");
  assert.equal(downloadAgent({ path: "out/report.pdf", agent_id: "gone" }, "lead", known), "lead", "its agent was deleted");
  assert.equal(downloadAgent({ path: "out/report.pdf" }, "", known), null);
  assert.equal(downloadAgent({ path: "out/report.pdf" }, "deleted-lead", known), null);
  assert.equal(downloadAgent({ path: "https://example.com/x.zip", agent_id: "w1" }, "lead", known), null);
});

test("the folders of a project, each once, with who works there", () => {
  const folders = projectFolders("C:\\Work\\billing", [
    { name: "lead", cwd: "C:\\Work\\billing" },
    { name: "w1", cwd: "c:\\work\\billing\\" },
    { name: "w2", cwd: "C:\\Work\\billing\\worktrees\\w2" },
    { name: "w3" },
  ]);
  assert.deepEqual(folders, [
    { path: "C:\\Work\\billing", who: ["lead", "w1"] },
    { path: "C:\\Work\\billing\\worktrees\\w2", who: ["w2"] },
  ]);
  assert.deepEqual(projectFolders("", []), []);
});

test("the card carries what Python's does, and the heads are the little faces", () => {
  const card = src("components/classic/project-card.tsx");
  for (const hook of ["data-card-status", "data-card-progress", "data-card-counts", "data-card-faces", "data-card-output"]) assert.ok(card.includes(hook), hook);
  assert.match(card, /\{project\.brief && </, "the description");
  assert.match(card, /coordinator <b className="font-semibold text-foreground">\{lead\.name\}<\/b>/);
  assert.match(card, /<AgentFace key=\{m\.id\} name=\{m\.name\} size=\{34\}/, "34 px heads, as on Python's card (its screenshot)");
  assert.match(card, /<\/b> agent\(s\)/, "Python's words");
  assert.match(card, /<b className="font-semibold">Not reported<\/b>/, "an empty bar says so, as in Python");
  assert.match(card, /href=\{href\(paths\.project\(project\.id\)\)\}/, "the whole card opens the project");
});

test("the Projects page is the classic computer's only; the sidebar links to it", () => {
  const app = src("App.tsx");
  assert.match(app, /if \(r\.name === "projects" && classic && desktop\) return <ClassicProjectsPage \/>;/);
  assert.match(app, /case "projects":\n      return <ProjectsPage \/>;/, "a phone and the default dashboard keep their list");
  const page = src("pages/classic-projects.tsx");
  assert.match(page, /Active projects · \{active\.length\}/);
  assert.match(page, /On the shelf · \{shelf\.length\}/);
  assert.match(src("components/sidebar.tsx"), /\{classic && <NavItem to=\{paths\.projects\(\)\} icon=\{FolderClosed\} label="Projects"/);
});

test("the project page: outputs with who, where and a download; team; files and locations", () => {
  const page = src("components/classic/project-status.tsx");
  for (const title of ["Where it stands", "Final outputs", "Links", "Files and locations"]) assert.ok(page.includes(`"${title}"`) || page.includes(`>${title}<`), title);
  assert.match(page, /title=\{`Team · \$\{members\.length\}`\}/);
  assert.match(page, /downloadAgentFile\(from, o\.path, name\)/);
  assert.match(page, /from=\{downloadAgent\(o, project\.lead_id, known\)\}/);
  assert.match(page, /canDownload\(\) && \(/, "no Download button where the window cannot download");
  assert.match(page, /coordinator\{" "\}/);
  assert.match(page, /\{counts\.agents\} active · \{counts\.shelved\} shelved/);
  assert.match(page, /← All projects/);
  assert.match(page, /Checklist \(\{progress\.done\}\/\{progress\.total\}\)/, "the checklist is closed until asked for");
  assert.match(page, />Open coordinator</);
  assert.doesNotMatch(page, /"POST"|dangerouslySetInnerHTML/);
  // the download goes through the daemon's own endpoint, which keeps the path inside the agent's folder
  const files = src("lib/agent-files.ts");
  assert.match(files, /`\/api\/agents\/\$\{encodeURIComponent\(agentId\)\}\/download\?path=\$\{encodeURIComponent\(path\)\}`/);
  const go = readFileSync(new URL("../../internal/api/agent_files.go", import.meta.url), "utf8");
  assert.match(go, /mux\.HandleFunc\("GET "\+base\+"download", s\.agentDownload\)/);
});
