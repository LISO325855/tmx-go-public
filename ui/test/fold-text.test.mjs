// Long thread text is folded to its first lines with "more" (lib/fold-text.ts); and on a phone
// under classic the attach button opens Photos first, with "File…" second (components/attach.tsx).
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { FOLD_CHARS, FOLD_LINES, foldText } from "../src/lib/fold-text.ts";

const read = (p) => readFileSync(new URL(`../src/${p}`, import.meta.url), "utf8");

test("a short text is left exactly as it is", () => {
  for (const s of ["", "Webhook handler done.", "one\ntwo\nthree\nfour", "x".repeat(FOLD_CHARS), "x".repeat(FOLD_CHARS + 60)]) {
    assert.deepEqual(foldText(s), { head: s, folded: false });
  }
});

test("a long text shows its beginning, cut at a word", () => {
  const long = ("The parser handles the first format. ".repeat(40)).trim();
  const f = foldText(long);
  assert.equal(f.folded, true);
  assert.ok(f.head.endsWith("…"));
  assert.ok(f.head.length <= FOLD_CHARS + 1, `head is ${f.head.length} long`);
  assert.ok(long.startsWith(f.head.slice(0, -1)), "the head is the text's own beginning");
  assert.match(f.head, /(format\.|first|the|handles|parser|The)…$/, "cut at a word, not inside one");
});

test("many lines show the first ones", () => {
  const lines = Array.from({ length: 12 }, (_, i) => `line ${i + 1}`).join("\n");
  const f = foldText(lines);
  assert.equal(f.folded, true);
  assert.equal(f.head, "line 1\nline 2\nline 3\nline 4…");
  assert.equal(f.head.split("\n").length, FOLD_LINES);
  // only blank lines after the limit: nothing more to show
  assert.equal(foldText("a\nb\nc\nd\n\n\n").folded, false);
});

test("a long word without spaces is still cut", () => {
  const f = foldText("x".repeat(2000));
  assert.equal(f.folded, true);
  assert.equal(f.head.length, FOLD_CHARS + 1);
});

test("thread events are folded where their whole text is shown", () => {
  assert.match(read("pages/matrix.tsx"), /<FoldedText text=\{e\.text\} \/>/);
  assert.match(read("components/board/log.tsx"), /<FoldedText text=\{e\.text\} \/>/);
  const view = read("components/folded-text.tsx");
  assert.match(view, /if \(!f\.folded\) return <>\{text\}<\/>;/, "a short event renders the same words, no button");
  assert.match(view, /aria-expanded=\{open\}/);
});

test("classic on a phone: Photos first, File… second; elsewhere the one button as before", () => {
  const src = read("components/attach.tsx");
  assert.match(src, /const phone = usePhoneClassic\(\);/);
  const phone = src.slice(src.indexOf("if (phone) {"), src.indexOf("return (\n    <>\n      <input\n        ref={picker}"));
  assert.ok(phone.length > 0, "the phone branch comes before the usual button");
  assert.ok(phone.indexOf('pick(photos, "image/*")') >= 0 && phone.indexOf("data-attach-photo") < phone.indexOf("data-attach-file"), "the photo button is first");
  assert.match(phone, /pick\(picker, "\*\/\*"\)/);
  assert.doesNotMatch(phone, /capture=/, "no capture attribute: it would force the camera and hide the library");
  const rest = src.slice(src.indexOf("return (\n    <>\n      <input\n        ref={picker}"));
  assert.doesNotMatch(rest.slice(0, 400), /accept=/, "the usual button still takes any file");
  assert.match(rest, /aria-label="Attach files"/);
});
