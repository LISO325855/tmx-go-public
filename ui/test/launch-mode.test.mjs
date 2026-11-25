// The mode the New agent form starts on (src/lib/harness.ts defaultLaunchMode). Under the classic
// UI profile Claude Code starts in its own screen, as every Python tmx agent ran; the daemon
// applies the same default to launches that name no mode (internal/fleet/classic_test.go).
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { defaultLaunchMode, FALLBACK_HARNESSES } from "../src/lib/harness.ts";

const h = (name) => FALLBACK_HARNESSES.find((x) => x.name === name);

test("the default dashboard starts every harness on its first mode", () => {
  assert.equal(defaultLaunchMode(h("claude"), false), "chat");
  assert.equal(defaultLaunchMode(h("codex"), false), "chat");
  for (const name of ["opencode", "pi", "shell", "gemini", "agy"]) assert.equal(defaultLaunchMode(h(name), false), "terminal");
});

test("classic: Claude Code starts in its own screen, Codex stays in chat", () => {
  assert.equal(defaultLaunchMode(h("claude"), true), "terminal");
  assert.equal(defaultLaunchMode(h("codex"), true), "chat");
  for (const name of ["opencode", "pi", "shell"]) assert.equal(defaultLaunchMode(h(name), true), "terminal");
});

test("a Claude without a terminal mode, or an unknown harness, never gets a mode it cannot run", () => {
  assert.equal(defaultLaunchMode({ name: "claude", modes: ["chat"] }, true), "chat");
  assert.equal(defaultLaunchMode(undefined, true), "chat");
  assert.equal(defaultLaunchMode({ name: "claude", modes: [] }, true), "chat");
});

test("the New agent form takes its starting mode from it", () => {
  const form = readFileSync(new URL("../src/components/new-agent-dialog.tsx", import.meta.url), "utf8");
  assert.match(form, /setMode\(defaultLaunchMode\(FALLBACK_HARNESSES\[0\], classic\)\)/);
  assert.match(form, /classic \? defaultLaunchMode\(h, true\) : h\.modes\.includes\(mode\) \? mode : h\.modes\[0\]/);
  assert.equal(FALLBACK_HARNESSES[0].name, "claude");
});
