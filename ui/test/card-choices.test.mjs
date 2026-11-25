// A card's choices the Python way (src/lib/card-choices.ts; parity E2 / F12): the recommended
// choice first and marked, "Not applicable / Dismiss", cards by project with chips.
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import {
  cardChoices,
  cardProjectId,
  filterCardGroups,
  groupCardsByProject,
  NOT_APPLICABLE_ANSWER,
  NOT_APPLICABLE_LABEL,
  projectChips,
  recommendationNote,
  recommendedIndex,
} from "../src/lib/card-choices.ts";

const src = (p) => readFileSync(new URL(`../src/${p}`, import.meta.url), "utf8");

test("the recommendation names an option exactly, however it is cased or punctuated", () => {
  const opts = ["Keep the old plan", "Move to the new plan", "Something else (say which)"];
  assert.equal(recommendedIndex(opts, "Move to the new plan"), 1);
  assert.equal(recommendedIndex(opts, "  move to the NEW plan. "), 1);
  assert.equal(recommendedIndex(opts, '"Keep the old plan"'), 0);
});

test("a recommendation with its reason still finds its option", () => {
  const opts = ["Yes", "No", "Later"];
  assert.equal(recommendedIndex(opts, "Yes, because it is reversible"), 0);
  assert.equal(recommendedIndex(opts, "No: the cost is not known yet."), 1);
  // the option says more than the recommendation
  assert.equal(recommendedIndex(["Option A (the cheap one)", "Option B (the fast one)"], "Option B"), 1);
});

test("a whole word, not the start of another one; the longest option wins", () => {
  assert.equal(recommendedIndex(["No", "Notify the client"], "Notify the client first"), 1);
  assert.equal(recommendedIndex(["No", "Yes"], "Nothing: wait for the bank"), -1, '"No" is not "Nothing"');
  assert.equal(recommendedIndex(["Plan", "Plan B"], "Plan B, it keeps the data"), 1);
});

test("no button is marked rather than the wrong one", () => {
  const opts = ["Yes", "No"];
  for (const rec of [undefined, null, "", "   ", "Ask the accountant", "It depends on the invoice"]) assert.equal(recommendedIndex(opts, rec), -1);
  assert.equal(recommendedIndex([], "Yes"), -1);
  assert.equal(recommendedIndex(undefined, "Yes"), -1);
  // two different options fit equally well
  assert.equal(recommendedIndex(["Plan A now", "Plan A later"], "Plan A"), -1);
  // the same option listed twice is not a choice either
  assert.equal(recommendedIndex(["Yes", "Yes"], "Yes"), -1);
});

test("the recommended choice comes first and is marked; the others keep the agent's order", () => {
  const opts = ["A", "B", "C", "D"];
  assert.deepEqual(cardChoices(opts, "C"), [
    { label: "C", index: 2, recommended: true },
    { label: "A", index: 0, recommended: false },
    { label: "B", index: 1, recommended: false },
    { label: "D", index: 3, recommended: false },
  ]);
  // nothing recommended: the agent's order, nothing marked
  assert.deepEqual(cardChoices(opts, "").map((c) => c.label), opts);
  assert.ok(cardChoices(opts, "nothing fits").every((c) => !c.recommended));
  assert.deepEqual(cardChoices(undefined, "A"), []);
  // the label sent back is the agent's own text, untouched
  assert.equal(cardChoices(["  Yes, do it  ", "No"], "yes, do it")[0].label, "  Yes, do it  ");
});

test("the note under the buttons: nothing when the mark says it all, the reason otherwise", () => {
  const opts = ["Yes", "No"];
  assert.equal(recommendationNote(opts, "Yes"), "");
  assert.equal(recommendationNote(opts, "yes."), "");
  assert.equal(recommendationNote(opts, "Yes, because it is reversible"), "Yes, because it is reversible");
  assert.equal(recommendationNote(opts, "Ask the accountant first"), "Ask the accountant first");
  assert.equal(recommendationNote(opts, ""), "");
  assert.equal(recommendationNote(undefined, undefined), "");
});

test("Not applicable / Dismiss tells the agent not to wait", () => {
  assert.equal(NOT_APPLICABLE_LABEL, "Not applicable / Dismiss");
  assert.match(NOT_APPLICABLE_ANSWER, /^Not applicable/);
  assert.match(NOT_APPLICABLE_ANSWER, /Do not wait/);
});

test("cards are grouped by project: the card's own, else its agent's, else none", () => {
  const agents = { a1: { project_id: "p1" }, a2: { project_id: "p2" }, head: {} };
  const projects = { p1: { name: "billing-portal" }, p2: { name: "docs-site" } };
  assert.equal(cardProjectId({ agent_id: "a1" }, agents), "p1");
  assert.equal(cardProjectId({ agent_id: "a1", card: { project_id: "p2" } }, agents), "p2", "the card's own project wins");
  assert.equal(cardProjectId({ agent_id: "head" }, agents), "");
  assert.equal(cardProjectId({ agent_id: "gone" }, agents), "");

  const list = [
    { id: 1, agent_id: "a1" },
    { id: 2, agent_id: "head" },
    { id: 3, agent_id: "a2" },
    { id: 4, agent_id: "head", card: { project_id: "p1" } },
    { id: 5, agent_id: "a9", card: { project_id: "p9" } },
  ];
  const groups = groupCardsByProject(list, agents, projects);
  assert.deepEqual(groups.map((g) => [g.key, g.name, g.items.map((a) => a.id)]), [
    ["p1", "billing-portal", [1, 4]],
    ["", "No project", [2]],
    ["p2", "docs-site", [3]],
    ["p9", "p9", [5]],
  ]);
  assert.deepEqual(groupCardsByProject([], agents, projects), []);
});

test("Python's chips: All projects, then project · n; none when there is one project", () => {
  const groups = [
    { key: "p1", name: "billing-portal", items: [1, 2] },
    { key: "", name: "No project", items: [3] },
  ];
  assert.deepEqual(projectChips(groups), [
    { key: null, label: "All projects", count: 3 },
    { key: "p1", label: "billing-portal · 2", count: 2 },
    { key: "", label: "No project · 1", count: 1 },
  ]);
  assert.deepEqual(projectChips(groups.slice(0, 1)), []);
  assert.deepEqual(projectChips([]), []);
});

test("a chip shows its project; a chip whose cards are gone shows everything again", () => {
  const groups = [
    { key: "p1", name: "a", items: [1] },
    { key: "", name: "No project", items: [2] },
  ];
  assert.deepEqual(filterCardGroups(groups, null), groups);
  assert.deepEqual(filterCardGroups(groups, "p1"), [groups[0]]);
  assert.deepEqual(filterCardGroups(groups, ""), [groups[1]], '"" is the no-project chip, not "all"');
  assert.deepEqual(filterCardGroups(groups, "answered-and-gone"), groups);
});

// The cards themselves, held in the source (the browser shows the real thing in Linux CI).
test("classic cards use that order, mark the button and carry the phone's hooks", () => {
  const owner = src("components/owner-card-actions.tsx");
  assert.match(owner, /cardChoices\(a\.options, classic \? a\.card\?\.recommendation : undefined\)/, "reordered only under classic");
  assert.match(owner, /data-card-choices/);
  assert.match(owner, /data-recommended=\{c\.recommended \|\| undefined\}/);
  assert.match(owner, /send\("answer", c\.label\)/, "the agent's own text is sent");
  assert.match(owner, /send\("answer", NOT_APPLICABLE_ANSWER\)/);
  assert.match(owner, /\{classic && !text && !closed && !a\.deferred && \(/, "Not applicable only under classic, and never over typed words");
  const legacy = src("components/approval.tsx");
  assert.match(legacy, /data-card-choices/);
  assert.match(legacy, /recommendationNote\(a\.options, a\.card\.recommendation\)/);
  assert.match(legacy, /decide\("answer", \{ answer: NOT_APPLICABLE_ANSWER \}\)/);
});

test("classic Your turn groups by project with chips; the default page still groups by agent", () => {
  const inbox = src("pages/inbox.tsx");
  assert.match(inbox, /groupCardsByProject\(list, agents, projects\)/);
  assert.match(inbox, /projectChips\(byProject\)/);
  assert.match(inbox, /\{classic \? "Your turn" : "Needs you"\}/);
  assert.match(inbox, /const flat = useMemo\(\(\) => \(classic \? shownProjects\.flatMap\(\(g\) => g\.items\) : groups\.flatMap\(\(g\) => g\.items\)\)/, "the keys move through what is shown");
  assert.match(inbox, /!classic && list\.length > 1 && \(/, "no key hints under classic");
});
