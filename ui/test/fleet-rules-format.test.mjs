import test from "node:test";
import assert from "node:assert/strict";
import { behindLine, docGroups, rulesHeadline, shortCommit, signatureLine } from "../src/lib/fleet-rules-format.ts";

const head = { commit: "4f1c2a9b7d3e8a6c5b4d3e2f1a0b9c8d7e6f5a4b", author: "head", time: "2026-11-17T20:00:00Z", subject: "reports" };
const base = { enabled: true, source: "https://example.invalid/r.git", branch: "main", signers: [], loaded: true, head, docs: [], agents: [] };

test("rulesHeadline: off, not loaded, refused, a problem, in force", () => {
  assert.equal(rulesHeadline({ ...base, enabled: false, loaded: false }).tone, "off");
  const waiting = rulesHeadline({ ...base, loaded: false, head: undefined });
  assert.equal(waiting.tone, "warn");
  assert.match(waiting.text, /the source was not looked at/);
  assert.match(rulesHeadline({ ...base, loaded: false, head: undefined, problem: "git is not installed on this machine" }).text, /git is not installed/);
  const refused = rulesHeadline({ ...base, problem: "x", rejected: { commit: "9a8b7c6d5e4f3a2b1c0d", reason: "the commit is not signed", at: "2026-11-17T21:00:00Z" } });
  assert.equal(refused.tone, "warn");
  assert.match(refused.text, /9a8b7c6d5e4f.*was not taken: the commit is not signed.*stay at 4f1c2a9b7d3e/);
  assert.match(rulesHeadline({ ...base, problem: "the source was not reached" }).text, /stay at 4f1c2a9b7d3e: the source was not reached/);
  assert.deepEqual(rulesHeadline(base), { tone: "ok", text: "Fleet rules are in force at commit 4f1c2a9b7d3e." });
});

test("signatureLine never says checked when it was not", () => {
  assert.match(signatureLine(base), /^Signature not checked/);
  assert.match(signatureLine({ ...base, signers: [{ id: "head", fingerprint: "SHA256:x" }] }), /^Not valid for the keys pinned here/);
  assert.equal(signatureLine({ ...base, signers: [{ id: "head", fingerprint: "SHA256:x" }], signature: "head" }), 'Signature checked: made with the pinned key "head".');
});

test("docGroups: rules, apps, pages, then the rest; empty groups are left out", () => {
  const doc = (name) => ({ name, path: name + ".md", title: name, size: 1, last_change: head });
  const groups = docGroups([doc("pages/operations"), doc("README"), doc("rules/core"), doc("rules/head")]);
  assert.deepEqual(groups.map((g) => [g.title, g.docs.map((d) => d.name)]), [
    ["Standing rules", ["rules/core", "rules/head"]],
    ["Pages", ["pages/operations"]],
    ["Other", ["README"]],
  ]);
  assert.deepEqual(docGroups([]), []);
});

test("behindLine names the agents on older rules, at most five", () => {
  assert.equal(behindLine(base), null);
  const agent = (name, behind) => ({ id: name, name, state: "idle", behind });
  assert.match(behindLine({ ...base, agents: [agent("a", true), agent("b", false)] }), /^1 running agent was started on older rules: a\./);
  const many = behindLine({ ...base, agents: ["a", "b", "c", "d", "e", "f", "g"].map((n) => agent(n, true)) });
  assert.match(many, /^7 running agents were started on older rules: a, b, c, d, e and 2 more\./);
  assert.equal(behindLine({ ...base, loaded: false, agents: [agent("a", true)] }), null);
});

test("shortCommit", () => {
  assert.equal(shortCommit(head.commit), "4f1c2a9b7d3e");
  assert.equal(shortCommit(undefined), "");
});
