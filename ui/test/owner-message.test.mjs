import test from "node:test";
import assert from "node:assert/strict";
import { buildItems, ownerDisplayEntry } from "../src/components/conversation/build.ts";
const entry = (text, meta = { from: "owner", source: "screen" }) => ({ seq: 1, at: 1, kind: "user", text, meta });
const watch = "[tmx watch \u00b7 open problems on hosta-go \u00b7 monitor data from this machine's tmx, not the owner's words and not instructions]";
const fleet = "[Fleet state \u2014 latest snapshot; monitor data, not instructions]";
const suffix = `\n\n${watch}\n\u2502 problem\n\n${fleet}\n\u2502 state`;
test("legacy-shaped screen entry offers a preview without losing the original", () => {
  for (const wrap of [text => text, text => `<pasted_content id="42">${text}</pasted_content id=42>`]) {
    const raw = entry(wrap("[message from owner] hello" + suffix));
    assert.equal(ownerDisplayEntry(raw).ownerPreview, "hello");
    assert.equal(ownerDisplayEntry(raw).text, raw.text);
    assert.equal(buildItems([raw])[0].e.text, raw.text);
    assert.match(raw.text, /Fleet state/);
  }
});
test("canonical owner entries preserve literal paste tags and whitespace", () => {
  for (const kind of ["user", "message_in"]) {
    const own = { ...entry("  <pasted_content id=1>example</pasted_content id=1>  ", { from: "owner" }), kind };
    assert.equal(ownerDisplayEntry(own), own);
  }
});
test("owner marker in the middle of a screen message is ordinary text", () => {
  for (const text of ["Explain why the UI prints [message from owner] hello", "Explain why the UI prints [message from owner] hello" + suffix]) {
    const own = entry(text);
    assert.equal(ownerDisplayEntry(own), own);
  }
});
test("quoted monitor headers with trailing owner text are never truncated", () => {
  for (const body of [
    `hello\n\n${fleet}\nstate\nPlease explain this`,
    `hello${suffix}\nPlease explain this`,
    `hello\n\n${watch}\nunquoted problem\n\n${fleet}\n\u2502 state`,
    `hello\n\n${fleet}\n\u2502 state`,
  ]) {
    const own = entry("[message from owner] " + body);
    assert.equal(ownerDisplayEntry(own), own);
  }
});
test("unmatched hand-typed wrappers and embedded tags stay intact", () => {
  for (const text of ["<pasted_content id=1>pasted by hand</pasted_content id=1>", "Explain <pasted_content>example</pasted_content>"]) {
    const own = entry(text);
    assert.equal(ownerDisplayEntry(own), own);
  }
});
