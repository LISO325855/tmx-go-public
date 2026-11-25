// The profile editor's Claude Code MCP servers (components/claude-extensions.tsx): what is
// picked, what Claude Code no longer has, and reading a server's config as the owner typed it.
import type { ClaudeExtensions, ClaudeServer } from "./types";

/** Names no profile can add: tmx's own server, the Python fleet's, and Claude in Chrome's (claudeext.Reserved). */
export const RESERVED_SERVERS = ["tmxg", "tmx", "claude-in-chrome"];

/** A name `claude mcp add-json` takes and tmx may add (claudeext.AddServer). */
export function validServerName(name: string): boolean {
  return /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(name) && !RESERVED_SERVERS.includes(name);
}

/** The profile's servers with id switched on or off; the order of picking is kept. */
export function toggleServer(servers: string[], id: string): string[] {
  return servers.includes(id) ? servers.filter((x) => x !== id) : [...servers, id];
}

/** Servers the profile lists that Claude Code does not have (any more): agents start without them. */
export function missingServers(servers: string[], ext: ClaudeExtensions | null): string[] {
  if (!ext?.available) return [];
  const known = ext.servers ?? [];
  return servers.filter((id) => !known.some((s) => s.id === id));
}

/** The small word after a server's name on its chip. */
export function serverSource(s: ClaudeServer): string {
  return s.source === "plugin" ? `plugin${s.enabled ? "" : " (off)"}` : "user";
}

/** The config typed into "Add an MCP server": a JSON object, or why not. */
export function parseServerConfig(text: string): { config: Record<string, unknown> } | { error: string } {
  let v: unknown;
  try {
    v = JSON.parse(text);
  } catch {
    return { error: "The config is not valid JSON" };
  }
  if (!v || typeof v !== "object" || Array.isArray(v)) return { error: "The config must be a JSON object" };
  return { config: v as Record<string, unknown> };
}
