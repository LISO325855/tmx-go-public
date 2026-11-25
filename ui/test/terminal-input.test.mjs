import test from "node:test";
import assert from "node:assert/strict";
import { stripTerminalQueryReplies } from "../src/lib/terminal-input.ts";

test("passive xterm viewer replies are never sent as typing", () => {
  for (const reply of ["\x1b[I", "\x1b[O", "\x1b[1;1R", "\x1b[?12;34R", "\x1b[?62;22c", "\x1b[>0;276;0c", "\x1b[0n", "\x1b[?2004;1$y", "\x1b[8;24;80t", "\x1b]11;rgb:ffff/ffff/ffff\x07", "\x1b]4;2;rgb:ff/ff/ff\x1b\\", "\x1b]52;c;\x1b\\", "\x1bP1$r0m\x1b\\"]) {
    assert.equal(stripTerminalQueryReplies(reply), "", JSON.stringify(reply));
  }
});
test("filter preserves owner typing, navigation, paste and interrupt mixed with replies", () => {
  const human = "draft\r\x1b[A\x1b[200~pasted\x1b[201~\x1b\x03";
  assert.equal(stripTerminalQueryReplies("\x1b[1;1R" + human + "\x1b[?62;22c"), human);
});
