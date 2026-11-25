// The key row under the terminal and "Paste only" (src/lib/terminal-keys.ts; parity B3, B4),
// both under the classic UI profile and both through the daemon's owner-only endpoints.
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { hasLiveScreen, TERMINAL_KEYS, validKeySpec } from "../src/lib/terminal-keys.ts";

const src = (p) => readFileSync(new URL(`../src/${p}`, import.meta.url), "utf8");

test("Python's keys, in its order: Enter, Esc, Ctrl+C, arrows, Tab, 1 2 3, y, n", () => {
  assert.deepEqual(TERMINAL_KEYS.map((k) => k.label), ["Enter", "Esc", "Ctrl+C", "↑", "↓", "←", "→", "Tab", "1", "2", "3", "y", "n"]);
  assert.deepEqual(TERMINAL_KEYS.map((k) => k.keys), ["enter", "esc", "ctrl+c", "up", "down", "left", "right", "tab", "1", "2", "3", "y", "n"]);
  assert.equal(new Set(TERMINAL_KEYS.map((k) => k.keys)).size, TERMINAL_KEYS.length);
  for (const k of TERMINAL_KEYS) assert.ok(k.title, `${k.label} has a name for a screen reader`);
});

test("every key is one the daemon's parser knows", () => {
  for (const k of TERMINAL_KEYS) assert.equal(validKeySpec(k.keys), true, k.keys);
  // the daemon's own names, read from internal/runner/term/screen/answer.go
  const go = readFileSync(new URL("../../internal/runner/term/screen/answer.go", import.meta.url), "utf8");
  const named = [...go.slice(go.indexOf("var namedKeys"), go.indexOf("// Keys encodes")).matchAll(/"([a-z]+)":/g)].map((m) => m[1]);
  assert.ok(named.includes("enter") && named.includes("esc") && named.includes("up") && named.includes("tab"), named.join(","));
  for (const k of TERMINAL_KEYS) {
    const ok = named.includes(k.keys) || /^ctrl\+[a-z]$/.test(k.keys) || [...k.keys].length === 1;
    assert.ok(ok, `the daemon would refuse "${k.keys}"`);
  }
  assert.equal(validKeySpec("down down enter"), true);
  assert.equal(validKeySpec("ctrl+c"), true);
  assert.equal(validKeySpec(""), false);
  assert.equal(validKeySpec("launch"), false);
  assert.equal(validKeySpec("ctrl+1"), false);
});

test("keys and Paste only need a live screen", () => {
  assert.equal(hasLiveScreen({ mode: "terminal", state: "idle", pid: 4242 }), true);
  assert.equal(hasLiveScreen({ mode: "terminal", state: "working", pid: 4242 }), true);
  assert.equal(hasLiveScreen({ mode: "terminal", state: "waiting", pid: 4242 }), true, "a menu on the screen is exactly when keys are needed");
  assert.equal(hasLiveScreen({ mode: "chat", state: "idle", pid: 4242 }), false, "chat mode has no screen");
  assert.equal(hasLiveScreen({ mode: "terminal", state: "stopped", pid: 0 }), false);
  assert.equal(hasLiveScreen({ mode: "terminal", state: "starting", pid: 0 }), false);
  assert.equal(hasLiveScreen({ mode: "terminal", state: "queued", pid: 0 }), false);
  assert.equal(hasLiveScreen({ mode: "terminal", state: "idle" }), false);
});

test("the row sits under the classic terminal and presses keys through the daemon", () => {
  const page = src("pages/agent.tsx");
  assert.match(page, /<AgentTerminal agent=\{agent\} \/>\s*\{\/\*[^*]*\*\/\}\s*<TerminalKeys agent=\{agent\} \/>/, "right under the terminal, in the classic view only");
  assert.equal(page.split("<TerminalKeys").length - 1, 2, "under the terminal in Focus view, and in the terminal pane side by side");
  const row = src("components/classic/terminal-keys.tsx");
  assert.match(row, /if \(!hasLiveScreen\(agent\)\) return null;/);
  assert.match(row, /api\.pressKeys\(agent\.id, keys\)/);
  const api = src("lib/api.ts");
  assert.match(api, /pressKeys: \(id: string, keys: string\) => t\(\)\.request<\{ ok: boolean \}>\("POST", `\/api\/agents\/\$\{enc\(id\)\}\/keys`, \{ keys \}\)/);
  assert.match(api, /typeText: \(id: string, text: string, submit: boolean\) => t\(\)\.request<\{ ok: boolean \}>\("POST", `\/api\/agents\/\$\{enc\(id\)\}\/type`, \{ text, submit \}\)/);
});

test("Paste only puts the text in without Enter, only under classic and only into a screen", () => {
  const box = src("components/conversation/composer.tsx");
  assert.match(box, /const canPaste = classic && !thread && hasLiveScreen\(agent\) && agent\.harness !== "shell";/);
  assert.match(box, /await api\.typeText\(agent\.id, body, false\);/, "submit is false: no Enter");
  assert.match(box, /\{canPaste && \(/);
  assert.match(box, /disabled=\{!text\.trim\(\) \|\| sending \|\| att\.readyIds\.length > 0 \|\| att\.uploading\}/, "never with attachments");
});
