// The conversation under the classic UI profile, easy to skim (the owner, 17 Nov 19:26: his eye
// could not tell the head's replies from tool lines and agent mail: "for my brain it's extra
// work… some kind of effect or color… Shadow boxes, whatever, something elegant"). He skims his
// own messages and the replies; the rest is there, out of the way, each kind in its own look:
// (1) replies: plain text, the reading line; (2) tool calls: ONE soft card per run, closed,
// "N steps"; (3) agent mail, in and out: a coloured left rule and a sender chip; (4) his own
// messages: clearly his. Computer and phone share the component; the colours are theme tokens.
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { buildItems, classicConversation, conversationItems, isOutgoing, isToOwner, outgoingMail, toolRunSummary, runningTool, toolElapsed } from "../src/components/conversation/build.ts";

const src = (p) => readFileSync(new URL(`../src/${p}`, import.meta.url), "utf8");
let seq = 0;
const e = (kind, text, extra = {}) => ({ seq: ++seq, agent_id: "head", at: seq * 1000, kind, text, ...extra });
let tid = 0;
/** a tool call and its result */
const tool = (name, input, result = "ok", opts = {}) => {
  const id = "t" + ++tid;
  const use = e("tool_use", "", { tool: name, tool_use_id: id, input });
  return opts.running ? [use] : [use, e("tool_result", result, { tool: name, tool_use_id: id, is_error: !!opts.error })];
};
const classic = (entries) => classicConversation(buildItems(entries));

test("a run of consecutive tool calls is one item: one card, N steps", () => {
  const items = classic([
    e("user", "Fix the bug", { meta: { from: "owner" } }),
    ...tool("Read", { file_path: "a.ts" }),
    ...tool("Edit", { file_path: "a.ts", old_string: "x", new_string: "y" }),
    ...tool("Bash", { command: "pnpm test" }),
    e("assistant", "Fixed: the test passes."),
  ]);
  assert.deepEqual(items.map((i) => i.type), ["user", "tools", "assistant"]);
  assert.equal(items[1].tools.length, 3);
  assert.deepEqual(toolRunSummary(items[1].tools), { steps: 3, failed: 0, running: false, label: "3 steps" });
});

test("a Thinking line between tool calls does not split the run", () => {
  const items = classic([...tool("Read", { file_path: "a" }), e("thinking", "hm"), ...tool("Read", { file_path: "b" }), e("thinking", "ok"), ...tool("Bash", { command: "ls" })]);
  assert.deepEqual(items.map((i) => i.type), ["tools"]);
  assert.equal(items[0].tools.length, 3);
});

test("a reply between tool calls does split it: the reading line is never swallowed", () => {
  const items = classic([...tool("Read", { file_path: "a" }), e("assistant", "Found it."), ...tool("Edit", { file_path: "a", old_string: "x", new_string: "y" })]);
  assert.deepEqual(items.map((i) => i.type), ["tools", "assistant", "tools"]);
});

test("the closed card says when a step runs or failed", () => {
  const run = classic([...tool("Bash", { command: "a" }, "boom", { error: true }), ...tool("Bash", { command: "b" }), ...tool("Bash", { command: "sleep 9" }, "", { running: true })])[0].tools;
  assert.deepEqual(toolRunSummary(run), { steps: 3, failed: 1, running: true, label: "3 steps" });
  assert.equal(toolRunSummary(run.slice(0, 1)).label, "1 step");
});

test("the agent's own mail is recognised by its tool: send, report, reply", () => {
  const pair = (name, input) => ({ key: "k", use: e("tool_use", "", { tool: name, input }) });
  assert.deepEqual(outgoingMail(pair("mcp__tmxg__send", { to: "docs-lead", body: "Propose an approach." })), { to: "docs-lead", text: "Propose an approach.", how: "send" });
  assert.deepEqual(outgoingMail(pair("mcp__tmxg__report", { text: "Done: 3 files." })), { to: "", text: "Done: 3 files.", how: "report" });
  assert.deepEqual(outgoingMail(pair("mcp__tmx__reply", { id: "ab12", body: "Yes." })), { to: "", text: "Yes.", how: "reply" });
  assert.deepEqual(outgoingMail(pair("mcp__tmxg__send", JSON.stringify({ to: "owner", body: "Staffed." }))), { to: "owner", text: "Staffed.", how: "send" }, "input as a JSON string");
  for (const [name, input] of [["Bash", { command: "send" }], ["mcp__tmxg__fleet", {}], ["mcp__tmxg__send", { to: "x" }], ["mcp__tmxg__send", { to: "x", body: "  " }], ["send", { to: "x", body: "y" }], ["mcp__tmxg__sendfile", { to: "x", body: "y" }]]) {
    assert.equal(outgoingMail(pair(name, input)), null, name);
  }
  assert.equal(isToOwner({ to: "Owner", text: "x", how: "send" }), true);
  assert.equal(isToOwner({ to: "docs-lead", text: "x", how: "send" }), false);
});

test("what the agent sends to the owner is a reply in the reading line, not a step in a card", () => {
  const items = classic([
    ...tool("Read", { file_path: "plan.md" }),
    ...tool("mcp__tmxg__send", { to: "owner", body: "billing-portal is staffed: three workers." }, "Delivered."),
    ...tool("Bash", { command: "ls" }),
  ]);
  assert.deepEqual(items.map((i) => i.type), ["tools", "assistant", "tools"]);
  assert.equal(items[1].e.text, "billing-portal is staffed: three workers.");
  assert.equal(items[0].tools.length, 1);
  assert.equal(items[2].tools.length, 1);
});

for (const classic of [false, true]) {
  const profile = classic ? "classic" : "default";
  test(`${profile}: owner and unresolved replies stay open when the original is outside the loaded entries`, () => {
    const reply = () => tool("mcp__tmxg__reply", { message_id: "older", body: "Can you approve this?" });
    const toOwner = reply();
    toOwner[1].meta = { to: "Owner" };
    const fromOwner = reply();
    fromOwner[0].meta = { reply_to_from: "owner" };
    const toAgent = reply();
    toAgent[0].meta = { recipient: "docs-lead" };
    const items = conversationItems(buildItems([...toOwner, ...fromOwner, ...reply(), ...toAgent]), classic);
    assert.deepEqual(items.map((i) => i.type), ["assistant", "assistant", "agent-message", "traffic"]);
    assert.equal(items[2].e.text, "Can you approve this?");
    assert.equal(items[2].e.meta.to, "");
    assert.equal(items[3].entries[0].meta.to, "docs-lead");
  });

  test(`${profile}: a failed request for owner approval stays open and is marked as unsent`, () => {
    const failed = tool("mcp__tmxg__send", { to: "owner", body: "Please approve the release." }, "refused", { error: true });
    const items = conversationItems(buildItems([
      e("message_in", "Passed testing", { meta: { from: "worker" } }),
      ...failed,
      e("message_in", "Ready to package", { meta: { from: "worker" } }),
    ]), classic);
    assert.deepEqual(items.map((i) => i.type), ["traffic", "agent-message", "traffic"]);
    assert.equal(items[1].e.text, "Please approve the release.");
    assert.deepEqual(items[1].e.meta, { to: "owner", how: "send", failed: true });
    assert.match(src("components/conversation/conversation.tsx"), /case "agent-message":\s*return <AgentMessageRow e=\{item.e\} defaultOpen \/>/);
  });
}

test("mail to other agents joins the agent mail, in the order it happened, in and out", () => {
  const items = classic([
    e("user", "Get search onto the docs site", { meta: { from: "owner" } }),
    ...tool("mcp__tmxg__send", { to: "docs-lead", body: "The owner wants search. Propose an approach." }, "Delivered."),
    e("message_in", "Proposal: a static index.", { meta: { from: "docs-lead", message_id: "m1" } }),
    ...tool("mcp__tmxg__reply", { id: "m1", body: "Go ahead." }, "Delivered."),
    e("assistant", "The docs lead is building a static search index."),
  ]);
  assert.deepEqual(items.map((i) => i.type), ["user", "traffic", "assistant"]);
  const mail = items[1].entries;
  assert.deepEqual(mail.map((m) => [isOutgoing(m), m.text]), [
    [true, "The owner wants search. Propose an approach."],
    [false, "Proposal: a static index."],
    [true, "Go ahead."],
  ]);
  assert.equal(mail[0].meta.to, "docs-lead");
  assert.equal(mail[2].meta.how, "reply");
});

test("the owner's answer that arrived as mail is one of his messages", () => {
  const items = classic([e("message_in", "Yes, go with option 1.", { meta: { from: "owner" } }), e("assistant", "Going with option 1.")]);
  assert.deepEqual(items.map((i) => i.type), ["user", "assistant"]);
  assert.equal(items[0].e.text, "Yes, go with option 1.");
});

test("nothing is lost: every message and every tool call is still there", () => {
  const entries = [
    e("user", "go", { meta: { from: "owner" } }),
    ...tool("Read", { file_path: "a" }),
    ...tool("mcp__tmxg__send", { to: "lead", body: "start" }),
    e("message_in", "started", { meta: { from: "lead" } }),
    ...tool("mcp__tmxg__send", { to: "owner", body: "It is under way." }),
    e("assistant", "Under way."),
  ];
  const items = classic(entries);
  const calls = items.flatMap((i) => (i.type === "tools" ? i.tools.length : i.type === "traffic" ? i.entries.filter(isOutgoing).length : i.type === "assistant" && i.key.startsWith("o") ? 1 : 0)).reduce((a, b) => a + b, 0);
  assert.equal(calls, 3, "three tool calls: one step, one mail out, one message to the owner");
  assert.equal(items.filter((i) => i.type === "traffic").flatMap((i) => i.entries).filter((m) => !isOutgoing(m)).length, 1);
  assert.equal(items.filter((i) => i.type === "assistant").length, 2);
});

test("the classic conversation does not change the items it was given", () => {
  const all = buildItems([...tool("Read", { file_path: "a" }), e("thinking", "x"), ...tool("Read", { file_path: "b" })]);
  const lengths = all.filter((i) => i.type === "tools").map((i) => i.tools.length);
  classicConversation(all);
  assert.deepEqual(all.filter((i) => i.type === "tools").map((i) => i.tools.length), lengths);
});

test("four looks, each told apart at a glance, in theme colours only", () => {
  const items = src("components/conversation/items.tsx");
  // (1) replies: plain text, no box
  assert.match(items, /<div data-chat="reply"><Markdown text=\{e\.text\} \/>/);
  // (2) one soft card per run, closed
  const run = items.slice(items.indexOf("export function ToolRun"), items.indexOf("export function MailChip"));
  assert.match(run, /const \[open, setOpen\] = useState\(false\);/, "closed by default");
  assert.match(run, /data-chat="steps"/);
  assert.match(run, /ml-3 rounded-lg border border-border\/70 bg-muted\/35 shadow-/, "indented, tinted, hairline and a light shadow");
  assert.match(run, /text-xs text-muted-foreground/, "smaller, muted");
  assert.match(run, /\{run\.label\}/);
  assert.match(run, /\{open && \(/);
  // (3) agent mail: a coloured left rule and a sender chip, not the steps' card
  const mail = items.slice(items.indexOf("export function AgentMessageRow"), items.indexOf("export function ResultDivider"));
  assert.match(mail, /border-l-2 border-brand\/45/);
  assert.match(mail, /data-chat="mail"/);
  assert.match(mail, /rounded-full bg-brand-soft px-1\.5 py-px text-2xs font-medium text-brand" data-mail-chip=\{out \? "to" : "from"\}/);
  assert.doesNotMatch(mail, /bg-muted\/35 shadow-/, "not the steps' look");
  // (4) his own messages: to the right, tinted, with a hairline and a shadow
  const mine = items.slice(items.indexOf("export const UserBubble"), items.indexOf("export const AssistantText"));
  assert.match(mine, /items-end/);
  assert.match(mine, /bg-brand\/15/);
  assert.match(mine, /classic && "border border-brand\/25 shadow-/);
  // theme tokens only in the new looks: no fixed colours that would break dark or light
  for (const part of [run, mail, mine]) assert.doesNotMatch(part, /#[0-9a-fA-F]{3,8}\b|text-(white|black)\b|bg-(white|black)\b/);
});

test("only under classic: the default dashboard keeps its rows", () => {
  const conv = src("components/conversation/conversation.tsx");
  assert.match(conv, /return classic \? <ToolRun tools=\{item\.tools\} cwd=\{cwd\} \/> : <ToolGroup tools=\{item\.tools\} cwd=\{cwd\} \/>;/);
  assert.match(conv, /<ItemView item=\{it\} cwd=\{agent\.cwd\} classic=\{classic\} \/>/);
});

// The owner, 17 Nov 19:50, on his phone: "a message from matrix-keeper-g, worker, 18 seconds ago"
// in his chat with the Architect. matrix-keeper-g is the Architect's OWN worker on the same
// machine. Two things had to hold, and the second did not.
test("mail from an agent's own worker on the same machine is agent mail like any other", () => {
  // what the daemon stamps on delivered mail (internal/fleet/mail.go): the sender's agent id, and its name
  const go = readFileSync(new URL("../../internal/fleet/mail.go", import.meta.url), "utf8");
  assert.match(go, /Kind: model\.EntryMessageIn, Text: m\.Body,\s*Meta: map\[string\]any\{"from": m\.From, "from_name": f\.nameOfLocked\(m\.From\), "message_id": m\.ID\}/);
  const items = classic([
    e("user", "Keep the Matrix tidy", { meta: { from: "owner" } }),
    e("message_in", "Three threads archived.", { meta: { from: "wk7t9v3x5z", from_name: "matrix-keeper-g", message_id: "m1" } }),
    e("assistant", "The Matrix is tidy: three threads archived."),
  ]);
  assert.deepEqual(items.map((i) => i.type), ["user", "traffic", "assistant"]);
  assert.equal(items[1].entries[0].meta.from_name, "matrix-keeper-g");
  const row = src("components/conversation/items.tsx");
  assert.match(row, /typeof e\.meta\?\.from_name === "string" \? e\.meta\.from_name : ""/, "the chip can name a sender that is no longer in the list");
});

test("the Matrix's turn view of the Architect follows the same rules (it showed the old mail box)", () => {
  const head = src("components/matrix/head.tsx");
  assert.match(head, /conversationItems\(buildItems\(entries \?\? \[\]\), classic, showAgentMessages,/);
  assert.match(head, /<ItemView item=\{it\} cwd=\{head\.cwd\} classic=\{classic\} \/>/);
  // and nothing else draws conversation items on its own
  for (const f of ["pages/agent.tsx", "pages/overview.tsx", "pages/phone-lists.tsx", "pages/inbox.tsx"]) {
    assert.doesNotMatch(src(f), /buildItems\(|<MailChip/, f);
  }
});


test("running step survives a later parallel tool finishing and elapsed advances", () => {
  const slow = { key: "slow", use: e("tool_use", "", { at: 1000, tool: "Bash", input: { description: "Watch service health", command: "sleep 300" } }) };
  const done = { key: "done", use: e("tool_use", ""), result: e("tool_result", "ok") };
  assert.equal(runningTool([slow, done]), slow);
  assert.equal(runningTool([done]), undefined);
  assert.equal(toolElapsed(slow.use.at, 169000), "2m 48s");
  assert.equal(toolElapsed(slow.use.at, 170000), "2m 49s");
  assert.equal(toolElapsed(slow.use.at, 500), "0s");
  assert.equal(toolElapsed(slow.use.at, 60000), "59s");
  assert.equal(toolElapsed(slow.use.at, 61000), "1m 0s");
});

test("running cards show description and timer, with interval cleanup", () => {
  const items = src("components/conversation/items.tsx");
  assert.match(items, /const last = runningTool\(tools\)/);
  assert.match(items, /typeof description === "string" && description.trim\(\) \? description/);
  assert.match(items, /<RunningElapsed at=\{last.use.at\}/);
  assert.match(items, /setInterval\(\(\) => setNow\(Date.now\(\)\), 1000\)/);
  assert.match(items, /clearInterval\(timer\)/);
});
