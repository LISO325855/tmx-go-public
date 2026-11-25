// Your turn on a phone in the classic profile (lib/phone.ts, components/phone/turn-deck.tsx): one
// card per screen, the choices full-width thumb targets with the recommended one first, as in the
// Python mobile app. The order of the choices and the project chips are the computer's
// (lib/card-choices.ts, test/card-choices.test.mjs).
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { deckIndex, swipeStep } from "../src/lib/phone.ts";

const read = (f) => readFileSync(new URL(f, import.meta.url), "utf8");

test("one card per screen: which one", () => {
  const ids = ["a", "b", "c"];
  assert.equal(deckIndex(ids, null, 0), 0, "opens on the first");
  assert.equal(deckIndex(ids, "c", 0), 2, "a link to a card opens that card");
  assert.equal(deckIndex(ids, "b", 2), 1);
  // "b" was answered and left the list: the card that took its place
  assert.equal(deckIndex(["a", "c"], "b", 1), 1);
  // the last card was answered: the new last one
  assert.equal(deckIndex(["a", "b"], "c", 2), 1);
  assert.equal(deckIndex(["a"], "gone", -3), 0);
  assert.equal(deckIndex([], "a", 0), -1);
});

test("a sideways swipe moves one card; a scroll does not", () => {
  assert.equal(swipeStep(-80, 5), 1, "to the left: next");
  assert.equal(swipeStep(80, -5), -1, "to the right: previous");
  assert.equal(swipeStep(-40, 0), 0, "too short");
  assert.equal(swipeStep(-80, 60), 0, "slanted: a scroll");
  assert.equal(swipeStep(10, 300), 0);
  assert.equal(swipeStep(0, 0), 0);
});

test("on a phone in the classic profile Your turn is the deck: one card, Previous · n / N · Next", () => {
  const inbox = read("../src/pages/inbox.tsx");
  assert.ok(inbox.includes("const phone = usePhoneClassic();"), "the classic profile, on a phone");
  assert.ok(/\{phone \? \(\s*<TurnDeck/.test(inbox), "the phone branch");
  // the deck shows the same cards in the same order as the computer's classic list, by project
  assert.ok(inbox.includes("cards={flat}") && inbox.includes("chips={chips}") && inbox.includes("chip={activeChip}") && inbox.includes("onChip={setChip}"));
  assert.ok(inbox.includes("<div data-card-actions>"));
  // otherwise the lists, as before
  assert.ok(inbox.includes('<PageBody width="max-w-[51.25rem]">') && inbox.includes("{groups.map((g) => {") && inbox.includes("{shownProjects.map((g) => ("));
  const deck = read("../src/components/phone/turn-deck.tsx");
  assert.ok(deck.includes("data-turn-deck"));
  assert.ok(deck.includes("renderCard(card, agent)"), "exactly one card is drawn");
  assert.equal((deck.match(/renderCard\(/g) ?? []).length, 1);
  for (const label of ['aria-label="Previous card"', 'aria-label="Next card"', "{index + 1} / {list.length}"]) assert.ok(deck.includes(label), label);
  assert.ok(deck.includes("swipeStep(t.clientX - s.x, t.clientY - s.y)"));
  assert.ok(deck.includes('<PhoneHeadline title="Your turn">'), "it opens on its headline: nothing fixed above");
  assert.ok(deck.includes("data-project-chips") && deck.includes("onChip(c.key);"), "Python's project chips");
  // a link to a card is followed also while Your turn is already open, past a chip
  assert.ok(deck.includes("if ((initialId ?? null) !== linked) {") && deck.includes("setChosen(initialId);"));
  assert.ok(inbox.includes("if (initial) setChip(null);"));
});

test("the choices are full-width thumb targets, 48 px, one under the other", () => {
  const css = read("../src/index.css");
  const block = css.slice(css.indexOf("/* Your turn on that phone"));
  assert.ok(block.startsWith("/* Your turn on that phone"), "the block exists");
  assert.ok(/@media \(max-width: 767px\) \{/.test(block));
  assert.ok(/:where\(button, \[data-slot="button"\]\) \{\s*min-height: 48px;/.test(block), "48 px");
  assert.ok(/:where\(\[data-card-choices\], div\.flex\.flex-wrap:not\(:has\(> div\)\)\) \{\s*flex-direction: column;\s*align-items: stretch;/.test(block), "one under the other");
  assert.ok(/> :where\(button, \[data-slot="button"\]\) \{\s*width: 100%;/.test(block), "full width");
  assert.ok(/\[data-variant="ghost"\] \{\s*width: auto;/.test(block), '"Later" stays a text link');
  assert.ok(!block.includes("var(--text-"), "the type scale is inlined by Tailwind: no such variables at run time");
  assert.ok(!/font-size: (9|10|11)px/.test(block), "nothing under 12 px");
  assert.ok(!block.includes("::after"), "the mark on the recommended choice is the shared component's, not a second one here");
});

test("recommended first and filled: the shared card components do it in the classic profile", () => {
  // lib/card-choices.ts puts the recommended choice first; both card components mark its button
  for (const file of ["../src/components/owner-card-actions.tsx", "../src/components/approval.tsx"]) {
    const src = read(file);
    assert.ok(src.includes("data-card-choices"), file + " marks the row of choices");
  }
  const owner = read("../src/components/owner-card-actions.tsx");
  assert.ok(owner.includes("cardChoices(a.options, classic ? a.card?.recommendation : undefined)"));
  assert.ok(owner.includes('variant={c.recommended ? "default" : "outline"}') && owner.includes("data-recommended={c.recommended || undefined}"));
});
