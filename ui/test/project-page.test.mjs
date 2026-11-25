// A project's shared page on the project screen (src/lib/project-page.ts; parity H2), under the
// classic UI profile: status, progress from the checklist, links, final outputs, each member.
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { needsProgress, sectionEmpty, shownContributions, statusLines, webLink } from "../src/lib/project-page.ts";

const src = (p) => readFileSync(new URL(`../src/${p}`, import.meta.url), "utf8");

test("progress is the ticked share of the checklist, never a guess", () => {
  assert.deepEqual(needsProgress([{ text: "a", done: true }, { text: "b", done: false }, { text: "c", done: true }]), { done: 2, total: 3, percent: 67 });
  assert.deepEqual(needsProgress([{ text: "a", done: true }]), { done: 1, total: 1, percent: 100 });
  assert.deepEqual(needsProgress([{ text: "a", done: false }]), { done: 0, total: 1, percent: 0 });
  for (const none of [[], null, undefined]) assert.deepEqual(needsProgress(none), { done: 0, total: 0, percent: 0 });
});

test("the status is its lines, without empty ones", () => {
  assert.deepEqual(statusLines("Webhooks done.\r\n\n  Plan picker in review.  "), ["Webhooks done.", "Plan picker in review."]);
  assert.deepEqual(statusLines(""), []);
  assert.deepEqual(statusLines(undefined), []);
});

test("only a plain web address can be clicked; everything else an agent wrote is text", () => {
  assert.equal(webLink("https://example.com/a?b=1"), "https://example.com/a?b=1");
  assert.equal(webLink("  http://127.0.0.1:8787/preview/x/ "), "http://127.0.0.1:8787/preview/x/");
  for (const no of [
    "javascript:alert(1)",
    "JavaScript:alert(1)",
    "data:text/html,<script>1</script>",
    "file:///C:/Users/x/secret.txt",
    "docs/billing.md",
    "C:\\out\\report.pdf",
    "//example.com/x",
    "https://",
    "http:// not a url",
    "",
    null,
    undefined,
  ]) {
    assert.equal(webLink(no), null, String(no));
  }
});

test("a member's line shows when it says something or offers an output; newest first", () => {
  const parts = [
    { agent_id: "a", agent_name: "api", status: "", final_output: [], updated_at: 5 },
    { agent_id: "b", agent_name: "docs", status: "Guide written", final_output: [], updated_at: 2 },
    { agent_id: "c", agent_name: "ui", status: " ", final_output: [{ path: "out.pdf", what: "Report" }], updated_at: 9 },
    { agent_id: "d", agent_name: "ops", status: "Deployed", final_output: null, updated_at: 2 },
  ];
  assert.deepEqual(shownContributions(parts).map((p) => p.agent_id), ["c", "b", "d"]);
  assert.deepEqual(shownContributions(null), []);
});

test("nothing published is said so, once", () => {
  const empty = { status: "", needs: [], links: null, final_output: [] };
  assert.equal(sectionEmpty(empty, { final_output: [], contributions: [] }), true);
  assert.equal(sectionEmpty(null, null), true);
  assert.equal(sectionEmpty({ ...empty, status: "Going" }, null), false);
  assert.equal(sectionEmpty({ ...empty, needs: [{ text: "x", done: false }] }, null), false);
  assert.equal(sectionEmpty(empty, { final_output: [{ path: "a", what: "b", kind: "path" }], contributions: [] }), false);
  assert.equal(sectionEmpty(empty, { final_output: [], contributions: [{ agent_id: "a", agent_name: "a", status: "On it", final_output: [], updated_at: 1 }] }), false);
});

test("the project screen shows it under classic only, from the daemon's own endpoints, read-only", () => {
  const page = src("pages/project.tsx");
  assert.match(page, /\{classic && <ProjectSharedPage project=\{project\} \/>\}/);
  const api = src("lib/api.ts");
  assert.match(api, /projectSection: \(id: string\) => t\(\)\.request<ProjectSection>\("GET", `\/api\/projects\/\$\{enc\(id\)\}\/section`\)/);
  assert.match(api, /projectArtifacts: \(id: string\) => t\(\)\.request<ProjectArtifacts>\("GET", `\/api\/projects\/\$\{enc\(id\)\}\/artifacts`\)/);
  const view = src("components/classic/project-status.tsx");
  assert.doesNotMatch(view, /"POST"|api\.(?!setProjectRepo)\w+\(/, "it only reads (through lib/project-pages.ts); the one thing the owner writes here is the repository");
  const cache = src("lib/project-pages.ts");
  assert.match(cache, /Promise\.all\(\[api\.projectSection\(id\), api\.projectArtifacts\(id\)\]\)/);
  assert.match(view, /const web = webLink\(url\);/, "every link and output goes through webLink");
  assert.match(view, /rel="noopener noreferrer"/);
  assert.doesNotMatch(view, /dangerouslySetInnerHTML/);
  // the endpoints exist in the daemon
  const routes = readFileSync(new URL("../../internal/api/routes.go", import.meta.url), "utf8");
  assert.match(routes, /"GET \/api\/projects\/\{id\}\/section"/);
  assert.match(routes, /"GET \/api\/projects\/\{id\}\/artifacts"/);
});
