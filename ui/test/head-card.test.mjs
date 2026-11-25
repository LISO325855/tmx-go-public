// The Architect / head card at the top of the sidebar (the owner, 17 Nov 15:58): it changed height
// with its status text ("Idle · just now" against a longer working status with a second line),
// so the whole sidebar jumped. It is a fixed-height card now, in both profiles: the status is
// ONE line, cut with an ellipsis, never wrapping, and what the agent is doing sits on that line.
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const side = readFileSync(new URL("../src/components/sidebar.tsx", import.meta.url), "utf8");
const card = side.slice(side.indexOf("function HeadCard"), side.indexOf("export function SidebarContent"));

test("the card has one fixed height, with or without a head, in both profiles", () => {
  assert.equal(card.split("h-[3.75rem]").length - 1, 2, "the card and its empty state");
  assert.match(card, /"block h-\[3\.75rem\] overflow-hidden rounded-lg border bg-card/);
  assert.doesNotMatch(card, /classic \? "h-|classic && "h-/, "not a classic-only height");
});

test("the status is one line that cannot wrap, and nothing adds a line under it", () => {
  const status = card.slice(card.indexOf("data-head-status") - 200, card.indexOf("data-head-status") + 60);
  assert.match(status, /flex h-4 min-w-0 items-center gap-1\.5 overflow-hidden/);
  assert.match(status, /whitespace-nowrap/);
  assert.doesNotMatch(card, /\{now && <Truncate/, "what it is doing is no longer a line of its own");
  assert.match(card, /<span className="min-w-0 flex-1 truncate text-subtle-foreground">\{now\}<\/span>/, "it takes the room that is left and is cut with an ellipsis");
  // the fixed parts never shrink away; only the free text is cut
  assert.match(card, /<span className="shrink-0">\{STATE_LABEL\[head\.state\]\}<\/span>/);
  assert.match(card, /<span className="tabular shrink-0 text-subtle-foreground">\{relativeTime\(head\.last_activity_at\)\}<\/span>/);
});

test("the name is one line too", () => {
  assert.match(card, /<span className="truncate text-\[0\.8125rem\] font-medium">\{label\}<\/span>/);
});
