// A profile's Claude Code MCP servers (lib/claude-extensions.ts, components/claude-extensions.tsx):
// picking them, what Claude Code no longer has, and the config an owner types. The editor itself is
// rendered against the mock daemon by scripts/render-check.mjs.
import test from "node:test";
import assert from "node:assert/strict";
import { missingServers, parseServerConfig, RESERVED_SERVERS, serverSource, toggleServer, validServerName } from "../src/lib/claude-extensions.ts";

const ext = {
  available: true,
  servers: [
    { id: "arc-desktop", name: "arc-desktop", source: "user", enabled: true, transport: "stdio", detail: "arc-desktop-mcp" },
    { id: "plugin:playwright:playwright", name: "playwright", source: "plugin", plugin: "playwright@official", enabled: false, transport: "stdio", detail: "npx" },
  ],
  plugins: [],
};

test("picking a server adds it at the end, picking it again removes it", () => {
  assert.deepEqual(toggleServer([], "arc-desktop"), ["arc-desktop"]);
  assert.deepEqual(toggleServer(["a", "arc-desktop"], "b"), ["a", "arc-desktop", "b"]);
  assert.deepEqual(toggleServer(["a", "arc-desktop", "b"], "arc-desktop"), ["a", "b"]);
});

test("a listed server Claude Code no longer has is shown as missing, only once Claude Code answered", () => {
  assert.deepEqual(missingServers(["arc-desktop", "gone", "plugin:playwright:playwright"], ext), ["gone"]);
  assert.deepEqual(missingServers(["gone"], null), []);
  assert.deepEqual(missingServers(["gone"], { available: false, error: "Claude Code is not installed", servers: [], plugins: [] }), []);
  assert.deepEqual(missingServers(["gone"], { available: true, servers: null, plugins: null }), ["gone"]);
});

test("a chip says where the server comes from, and when its plugin is off", () => {
  assert.equal(serverSource(ext.servers[0]), "user");
  assert.equal(serverSource(ext.servers[1]), "plugin (off)");
  assert.equal(serverSource({ ...ext.servers[1], enabled: true }), "plugin");
});

test("tmx's own server names and Claude in Chrome cannot be added; other plain names can", () => {
  assert.deepEqual(RESERVED_SERVERS, ["tmxg", "tmx", "claude-in-chrome"]);
  for (const bad of ["tmxg", "tmx", "claude-in-chrome", "", "-x", "a b", "x;y", "plugin:a:b", "a".repeat(65)]) assert.equal(validServerName(bad), false, bad);
  for (const ok of ["arc-desktop", "agent-browser-guard", "docs_2", "A1"]) assert.equal(validServerName(ok), true, ok);
});

test("the typed config must be a JSON object", () => {
  assert.deepEqual(parseServerConfig('{"command": "arc-desktop-mcp", "args": ["--stdio"], "env": {"ARC_MODE": "guarded"}}'), {
    config: { command: "arc-desktop-mcp", args: ["--stdio"], env: { ARC_MODE: "guarded" } },
  });
  assert.deepEqual(parseServerConfig("{nope"), { error: "The config is not valid JSON" });
  for (const notObject of ["[1]", '"x"', "null", "3"]) assert.deepEqual(parseServerConfig(notObject), { error: "The config must be a JSON object" }, notObject);
});
