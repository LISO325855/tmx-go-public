// Both profiles fold consecutive agent mail in place and can expand it inline.
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { buildItems, classicShows, conversationItems, foldAgentTraffic, isAgentTraffic, trafficLabel } from "../src/components/conversation/build.ts";

const src = (p) => readFileSync(new URL(`../src/${p}`, import.meta.url), "utf8");
let seq = 0;
const e = (kind, text, meta) => ({ seq: ++seq, agent_id: "head", at: seq * 1000, kind, text, meta });

test("agent traffic is told by the sender field, not by guessing from the text", () => {
  assert.equal(isAgentTraffic(e("message_in", "report", { from: "ld8q2w4e6r" })), true);
  assert.equal(isAgentTraffic(e("message_in", "report", { from: "billing-lead" })), true);
  assert.equal(isAgentTraffic(e("message_in", "no sender recorded", {})), true, "delivered mail without the owner as sender is not the owner's");
  assert.equal(isAgentTraffic(e("message_in", "yes, do it", { from: "owner" })), false, "the owner's answer");
  assert.equal(isAgentTraffic(e("user", "hello", { from: "owner" })), false, "the owner's message");
  assert.equal(isAgentTraffic(e("user", "typed in the screen", { from: "owner", source: "screen" })), false);
  assert.equal(isAgentTraffic(e("user", "hello")), false, "a user turn with no sender is the owner's");
  assert.equal(isAgentTraffic(e("user", "[from lead] done", { from: "ld8q2w4e6r" })), true, "a turn typed in for another agent");
  for (const kind of ["assistant", "thinking", "tool_use", "tool_result", "result", "system", "error"]) {
    assert.equal(isAgentTraffic(e(kind, "x", { from: "ld8q2w4e6r" })), false, kind);
  }
});

test("a run of agent mail folds into one line; the owner's messages and the agent's replies stay", () => {
  const entries = [
    e("user", "What is the state of billing?", { from: "owner" }),
    e("assistant", "Asking the lead."),
    e("message_in", "Webhooks done.", { from: "lead" }),
    e("message_in", "Tests pass.", { from: "worker" }),
    e("assistant", "Billing: webhooks done, tests pass."),
    e("message_in", "ok thanks", { from: "owner" }),
    e("message_in", "One more thing.", { from: "lead" }),
  ];
  const items = foldAgentTraffic(buildItems(entries).filter(classicShows));
  assert.deepEqual(items.map((i) => i.type), ["user", "assistant", "traffic", "assistant", "mail", "traffic"]);
  assert.deepEqual(items[2].entries.map((x) => x.text), ["Webhooks done.", "Tests pass."]);
  assert.deepEqual(items[5].entries.map((x) => x.text), ["One more thing."]);
  assert.equal(items[4].e.text, "ok thanks", "the owner's own answer is never folded");
  // nothing is lost: every entry is still there, shown or inside a fold
  const kept = items.flatMap((i) => (i.type === "traffic" ? i.entries : [i.e])).map((x) => x.seq);
  assert.deepEqual(kept, entries.map((x) => x.seq));
});

test("a Thinking line or a time break between two agent messages does not split the fold", () => {
  seq = 100;
  const entries = [e("message_in", "a", { from: "lead" }), e("thinking", "hm"), e("message_in", "b", { from: "lead" })];
  entries[2].at = entries[1].at + 3600_000; // a time break in between
  const items = foldAgentTraffic(buildItems(entries).filter(classicShows));
  assert.deepEqual(items.map((i) => i.type), ["traffic"]);
  assert.equal(items[0].entries.length, 2);
});

test("a conversation without agent mail is untouched", () => {
  const items = buildItems([e("user", "hi", { from: "owner" }), e("assistant", "hello")]);
  assert.deepEqual(foldAgentTraffic(items), items);
  assert.deepEqual(foldAgentTraffic([]), []);
});

test("the line says how many", () => {
  assert.equal(trafficLabel(1), "1 agent message");
  assert.equal(trafficLabel(7), "7 agent messages");
});

for (const classic of [false, true]) {
  const profile = classic ? "classic" : "default";
  test(`${profile}: ten messages between two owner turns fold in place and expand to twelve items`, () => {
    const start = e("user", "Start", { from: "owner" });
    const incoming = Array.from({ length: 9 }, (_, n) => e("message_in", `Update ${n}`, { from: "lead" }));
    const outgoing = {
      ...e("tool_use", ""),
      input: { to: "lead", body: "Thanks for the updates" },
      tool: "mcp__tmxg__send",
    };
    const end = e("user", "Continue", { from: "owner" });
    const all = buildItems([start, ...incoming, outgoing, end]);
    const folded = conversationItems(all, classic);
    assert.deepEqual(folded.map((i) => i.type), ["user", "traffic", "user"]);
    assert.equal(trafficLabel(folded[1].entries.length), "10 agent messages");
    assert.deepEqual(folded[1].entries.map((entry) => entry.seq), [...incoming, outgoing].map((entry) => entry.seq));
    const expanded = conversationItems(all, classic, true);
    assert.deepEqual(expanded.map((i) => i.type), ["user", ...Array(10).fill("agent-message"), "user"]);
    assert.deepEqual(expanded.map((i) => i.e.seq), [start, ...incoming, outgoing, end].map((entry) => entry.seq));
    assert.equal(expanded[10].e.text, "Thanks for the updates");
  });

  test(`${profile}: a question sent to the owner and an ask_owner call stay outside the fold`, () => {
    const mail = e("message_in", "Update", { from: "lead" });
    const question = {
      ...e("tool_use", ""),
      input: { to: "owner", body: "Can you approve the design?" },
      tool: "mcp__tmxg__send",
    };
    const card = { ...e("tool_use", ""), input: { question: "Ship it?" }, tool: "mcp__tmxg__ask_owner" };
    const items = conversationItems(buildItems([mail, question, card, e("message_in", "Another update", { from: "lead" })]), classic);
    assert.deepEqual(items.map((i) => i.type), ["traffic", "assistant", "tools", "traffic"]);
    assert.equal(items[1].e.text, question.input.body);
    assert.equal(items[2].tools[0].use, card);
  });
}

test("classic sidebar: Settings has its row, the version is small and muted on its own line", () => {
  const side = src("components/sidebar.tsx");
  assert.match(side, /classic \? "flex-col" : "items-center"\)\} data-sidebar-footer/);
  assert.match(side, /classic \? "min-w-0 self-start truncate px-2 text-\[0\.625rem\] leading-4" : "px-1 text-2xs"/);
  const foot = side.slice(side.indexOf("data-sidebar-footer"));
  assert.ok(foot.indexOf('label="Settings"') < foot.indexOf("data-sidebar-version"), "Settings first, the version under it");
});
