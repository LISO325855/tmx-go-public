// Per-chat WhatsApp access for agents (the daemon's internal/whatsapp/access.go): which chats a
// profile's agents may read, and which they may ask to write to. Pure helpers for the Settings
// page and a chat's menu; the daemon decides.

import type { WaChatAccess, WaSettings } from "./wa-types";

export type AccessKind = "read" | "send";

/** The profiles the owner lets use WhatsApp at all (the head when nothing is set). */
export function accessProfiles(s: WaSettings): string[] {
  return s.agent_profiles?.length ? s.agent_profiles : ["head"];
}

/** The head's profile: it reads every chat except the private ones; the others read only what is opened. */
export const HEAD_PROFILE = "head";

/** The profiles other than the head's: their chats are opened one by one. */
export function otherProfiles(s: WaSettings): string[] {
  return accessProfiles(s).filter((p) => p !== HEAD_PROFILE);
}

export function isPrivate(s: WaSettings, jid: string): boolean {
  return s.private_chats?.includes(jid) ?? false;
}

/** The settings with one chat marked private for the head, or not. */
export function withPrivate(s: WaSettings, jid: string, on: boolean): WaSettings {
  const cur = s.private_chats ?? [];
  return { ...s, private_chats: on ? [...new Set([...cur, jid])] : cur.filter((j) => j !== jid) };
}

/** How a profile is named to the owner: "head" is "The head", others "lead agents". */
export function profileLabel(slug: string): string {
  return slug === "head" ? "The head" : `${slug.charAt(0).toUpperCase()}${slug.slice(1)} agents`;
}

export function accessList(s: WaSettings, key: string, kind: AccessKind): string[] {
  return s.chat_access?.[key]?.[kind] ?? [];
}

/** The settings with one list of one profile replaced; a profile left with no chats drops out. */
export function withAccessList(s: WaSettings, key: string, kind: AccessKind, jids: string[]): WaSettings {
  const table: Record<string, WaChatAccess> = { ...(s.chat_access ?? {}) };
  const cur: WaChatAccess = { read: [...(table[key]?.read ?? [])], send: [...(table[key]?.send ?? [])] };
  cur[kind] = [...new Set(jids)];
  if (cur.read.length + cur.send.length === 0) delete table[key];
  else table[key] = cur;
  return { ...s, chat_access: table };
}

/** The settings with one chat added to, or taken out of, one list of one profile. */
export function withAccessChat(s: WaSettings, key: string, kind: AccessKind, jid: string, on: boolean): WaSettings {
  const cur = accessList(s, key, kind);
  return withAccessList(s, key, kind, on ? [...cur, jid] : cur.filter((j) => j !== jid));
}

/**
 * Why an agent of the profile reads a chat although it is not on the profile's read list: the
 * owner opened it another way. "" when it does not.
 */
export function readsAnyway(s: WaSettings, key: string, jid: string): string {
  if (key === HEAD_PROFILE) return ""; // the head: every chat but the private ones (isPrivate)
  if (s.allowed_chats?.includes(jid)) return "agents may send here without asking";
  return "";
}
