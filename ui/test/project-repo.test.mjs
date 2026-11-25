// A project's repository on the classic project screen (src/lib/project-page.ts; the owner, 17 Nov
// 19:01: "a GitHub repo per project"). The daemon keeps one address on the project's shared
// page, set by the owner only (internal/fleet/project_repo.go and its Go tests); this is the
// screen's half: showing it, opening it, setting it.
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { repoName, repoWebAddress } from "../src/lib/project-page.ts";

const src = (p) => readFileSync(new URL(`../src/${p}`, import.meta.url), "utf8");

test("the page to open for a repository address", () => {
  assert.equal(repoWebAddress("https://github.com/blackcode-ch/tmx-go-fleet"), "https://github.com/blackcode-ch/tmx-go-fleet");
  assert.equal(repoWebAddress("https://github.com/blackcode-ch/tmx-go-fleet.git"), "https://github.com/blackcode-ch/tmx-go-fleet");
  assert.equal(repoWebAddress("git@github.com:blackcode-ch/tmx-go-fleet.git"), "https://github.com/blackcode-ch/tmx-go-fleet");
  assert.equal(repoWebAddress("ssh://git@gitlab.example.com/group/sub/repo.git"), "https://gitlab.example.com/group/sub/repo");
  assert.equal(repoWebAddress("  https://github.com/owner/repo/  "), "https://github.com/owner/repo");
});

test("anything that is not a repository address opens nothing", () => {
  for (const no of ["", null, undefined, "javascript:alert(1)", "http://github.com/owner/repo", "file:///C:/code", "C:\\code\\repo", "owner/repo", "git@github.com:owner/repo;rm -rf", "ssh://root@host.example/owner/repo"]) {
    assert.equal(repoWebAddress(no), null, String(no));
  }
});

test("a repository is called owner/repo", () => {
  assert.equal(repoName("https://github.com/blackcode-ch/tmx-go-fleet.git"), "blackcode-ch/tmx-go-fleet");
  assert.equal(repoName("git@github.com:blackcode-ch/tmx-go-fleet.git"), "blackcode-ch/tmx-go-fleet");
  assert.equal(repoName("something else"), "something else", "shown as written when it cannot be read");
  assert.equal(repoName(""), "");
});

test("the project screen shows the repository and lets the owner set it; the daemon decides what is valid", () => {
  const page = src("components/classic/project-status.tsx");
  assert.match(page, /<Card title="Repository" name="repo">/);
  assert.match(page, /await api\.setProjectRepo\(project\.id, next\.trim\(\)\);\s*await loadProjectPage\(project\.id\);/, "saved, then read back from the daemon");
  assert.match(page, /const web = repoWebAddress\(repo\);/, "only an address the helper accepts becomes a link");
  assert.match(page, /No token in the address/);
  const api = src("lib/api.ts");
  assert.match(api, /setProjectRepo: \(id: string, repo: string\) => t\(\)\.request<ProjectSection>\("POST", `\/api\/projects\/\$\{enc\(id\)\}\/section`, \{ repo \}\)/);
  // the daemon's side: the owner only, a checked address, and agents are told it
  const go = readFileSync(new URL("../../internal/fleet/project_repo.go", import.meta.url), "utf8");
  assert.match(go, /func NormalizeProjectRepo\(s string\) \(string, error\)/);
  const projects = readFileSync(new URL("../../internal/fleet/projects.go", import.meta.url), "utf8");
  assert.match(projects, /if patch\.Repo != nil && me != nil \{\s*return model\.ProjectSection\{\}, errf\(KindForbidden, "only the owner sets a project's repository"\)/);
  const prompts = readFileSync(new URL("../../internal/fleet/prompts.go", import.meta.url), "utf8");
  assert.match(prompts, /if repo := f\.projectRepoLocked\(a\.ProjectID\); repo != "" \{/);
});
