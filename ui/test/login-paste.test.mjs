import assert from "node:assert/strict";
import test from "node:test";
import { isLoginPasteShortcut, loginPaste } from "../src/lib/login-paste.ts";

test("Claude sign-in writes the whole synthetic code in exactly one paste frame", () => {
  const code = "synthetic-" + "aZ09_#-".repeat(512);
  assert.equal(loginPaste(code), "\x1b[200~" + code + "\x1b[201~");
  assert.equal(loginPaste(code, true), "\x1b[200~" + code + "\x1b[201~\r");
  assert.equal(loginPaste("  token#state\r\n", true), "\x1b[200~token#state\x1b[201~\r");
  assert.equal(loginPaste("part\rsecond"), null);
  assert.equal(loginPaste("part\x1b[201~second"), null);
});

test("Claude sign-in recognizes clipboard paste keys without claiming other terminal keys", () => {
  assert.ok(isLoginPasteShortcut({ key: "v", ctrlKey: true }));
  assert.ok(isLoginPasteShortcut({ key: "v", metaKey: true }));
  assert.ok(isLoginPasteShortcut({ key: "Insert", shiftKey: true }));
  assert.ok(!isLoginPasteShortcut({ key: "Insert" }));
  assert.ok(!isLoginPasteShortcut({ key: "c", ctrlKey: true }));
});
