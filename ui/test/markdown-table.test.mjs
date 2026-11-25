// Markdown tables in a conversation (src/index.css .prose-tmx; the owner, 17 Nov 18:54, computer and
// phone): the first column was squeezed until its words broke in the middle ("Workspac / e",
// "Recording / s") while other columns had room. A cell breaks between words only, so a column
// is never narrower than its longest word; a table that does not fit scrolls sideways.
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const css = readFileSync(new URL("../src/index.css", import.meta.url), "utf8").replace(/\r\n/g, "\n");
/** The declarations of the rule with exactly this selector list. */
function rule(selector) {
  const at = css.indexOf(selector + " {");
  assert.ok(at >= 0, `no rule ${selector}`);
  return css.slice(at, css.indexOf("\n}", at));
}

test("a table cell never breaks inside a word", () => {
  const cell = rule(".prose-tmx th,\n.prose-tmx td");
  assert.match(cell, /overflow-wrap: normal;/);
  assert.match(cell, /word-break: normal;/);
  assert.match(cell, /hyphens: manual;/, "and no automatic hyphens either");
  assert.doesNotMatch(cell, /overflow-wrap: anywhere|word-break: break-all|white-space: nowrap/, "it still wraps between words");
});

test("the cells override the prose's break-anywhere; prose outside tables keeps it", () => {
  const prose = rule(".prose-tmx");
  assert.match(prose, /overflow-wrap: anywhere;/, "a long path in a paragraph still cannot push a message wide");
  assert.ok(css.indexOf(".prose-tmx th,\n.prose-tmx td {") > css.indexOf(".prose-tmx {"), "the cell rule comes later and is more specific");
});

test("a table that does not fit scrolls sideways inside itself", () => {
  const table = rule(".prose-tmx table");
  assert.match(table, /display: block;/);
  assert.match(table, /overflow-x: auto;/);
  assert.doesNotMatch(table, /table-layout: fixed/, "columns size to their content");
});

test("the mock conversation has a wide table to look at", () => {
  const mock = readFileSync(new URL("../src/lib/mock/data.ts", import.meta.url), "utf8");
  assert.match(mock, /\| Python tab \| In Go \| Note \|/);
  assert.match(mock, /\| Workspace \|/);
  assert.match(mock, /\| Recordings \|/);
});
