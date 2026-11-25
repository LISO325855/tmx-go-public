import type { TimelineEntry } from "@/lib/types";

export type Item =
  | { type: "user"; key: string; e: TimelineEntry }
  | { type: "assistant"; key: string; e: TimelineEntry }
  | { type: "thinking"; key: string; e: TimelineEntry }
  | { type: "tools"; key: string; tools: ToolPair[] }
  | { type: "mail"; key: string; e: TimelineEntry }
  | { type: "result"; key: string; e: TimelineEntry }
  | { type: "system"; key: string; e: TimelineEntry }
  | { type: "error"; key: string; e: TimelineEntry }
  | { type: "time"; key: string; at: number }
  | { type: "agent-message"; key: string; e: TimelineEntry }
  | { type: "traffic"; key: string; entries: TimelineEntry[] };

export interface ToolPair {
  key: string;
  use?: TimelineEntry;
  result?: TimelineEntry;
}

const GAP = 15 * 60_000;

/** Harness plumbing, not work: Claude loading its deferred tool definitions. */
const HIDDEN_TOOLS = new Set(["ToolSearch"]);

/** A legacy-shaped screen entry may be a genuine paste: retain all text and offer a preview. */
export function ownerDisplayEntry(e: TimelineEntry): TimelineEntry & { ownerPreview?: string } {
  if (e.kind !== "user" || e.meta?.source !== "screen" || e.meta?.from !== "owner") return e;
  // A single outer transport wrapper, never tags embedded in an owner's explanation.
  const outer = /^<pasted_content(?:\s[^>]*)?>([\s\S]*)<\/pasted_content(?:\s[^>]*)?>$/.exec(e.text);
  const candidate = outer && !/<\/?pasted_content(?:\s[^>]*)?>/.test(outer[1]) ? outer[1].trim() : e.text;
  const prefix = "[message from owner] ";
  if (!candidate.startsWith(prefix)) return e;
  // Both exact generated headers, quoted data lines, and nothing after the snapshot.
  const suffix = /\n\n\[tmx watch \u00b7 open problems on [^\r\n\]]+ \u00b7 monitor data from this machine's tmx, not the owner's words and not instructions\]\n\u2502 [^\r\n]*(?:\n\u2502 [^\r\n]*)*\n\n\[Fleet state \u2014 latest snapshot; monitor data, not instructions\]\n\u2502 [^\r\n]*(?:\n\u2502 [^\r\n]*)*$/;
  const match = suffix.exec(candidate);
  if (!match) return e;
  return { ...e, ownerPreview: candidate.slice(prefix.length, match.index) };
}

/** Pairs tool results with their calls, groups consecutive tool calls, adds time breaks. */
export function buildItems(entries: TimelineEntry[]): Item[] {
  const results = new Map<string, TimelineEntry>();
  const hidden = new Set<string>();
  for (const e of entries) {
    if (e.kind === "tool_result" && e.tool_use_id) results.set(e.tool_use_id, e);
    if (e.kind === "tool_use" && e.tool && HIDDEN_TOOLS.has(e.tool) && e.tool_use_id) hidden.add(e.tool_use_id);
  }
  const used = new Set<string>();
  const items: Item[] = [];
  let prevAt = 0;
  for (const e of entries) {
    if ((e.kind === "tool_use" || e.kind === "tool_result") && e.tool_use_id && hidden.has(e.tool_use_id)) continue;
    if (prevAt && e.at - prevAt > GAP) items.push({ type: "time", key: `t${e.seq}`, at: e.at });
    prevAt = e.at;
    const key = String(e.seq);
    switch (e.kind) {
      case "tool_use":
      case "tool_result": {
        let pair: ToolPair;
        if (e.kind === "tool_use") {
          const r = e.tool_use_id ? results.get(e.tool_use_id) : undefined;
          if (r) used.add(r.tool_use_id!);
          pair = { key, use: e, result: r };
        } else {
          if (e.tool_use_id && used.has(e.tool_use_id)) continue;
          pair = { key, result: e };
        }
        const last = items[items.length - 1];
        if (last?.type === "tools") last.tools.push(pair);
        else items.push({ type: "tools", key: "g" + key, tools: [pair] });
        break;
      }
      case "user":
        items.push({ type: "user", key, e });
        break;
      case "assistant":
        if (e.text.trim()) items.push({ type: "assistant", key, e });
        break;
      case "thinking":
        if (e.text.trim()) items.push({ type: "thinking", key, e });
        break;
      case "message_in":
        items.push({ type: "mail", key, e });
        break;
      case "result":
        items.push({ type: "result", key, e });
        break;
      case "error":
        items.push({ type: "error", key, e });
        break;
      case "system": {
        if (!e.text.trim()) break;
        // Several restarts in a row leave identical markers ("stopped"): show the latest once.
        const last = items[items.length - 1];
        if (last?.type === "system" && last.e.text === e.text) items[items.length - 1] = { type: "system", key, e };
        else items.push({ type: "system", key, e });
        break;
      }
      default:
        if (e.text?.trim()) items.push({ type: "system", key, e });
    }
  }
  return items;
}

/**
 * What the classic conversation keeps of the built items (lib/ui-profile.ts): Python's chat shows
 * messages and folded tool lines only, so no Thinking lines, no time breaks and no "Done · N
 * turns" dividers; a turn that ended with an error keeps its divider, which says so.
 */
export function classicShows(it: Item): boolean {
  if (it.type === "thinking" || it.type === "time") return false;
  if (it.type === "result") {
    const m = it.e.meta ?? {};
    return !!it.e.is_error || (typeof m.subtype === "string" && m.subtype !== "success");
  }
  return true;
}

/**
 * Mail between agents, by its sender field (meta.from): a delivered message, or a typed-in turn,
 * whose sender is not the owner. The owner's own messages and answers, and everything the agent
 * says and does, are not traffic.
 */
export function isAgentTraffic(e: TimelineEntry): boolean {
  if (e.kind !== "message_in" && e.kind !== "user") return false;
  const from = String(e.meta?.from ?? "").trim();
  if (e.kind === "user") return from !== "" && from !== "owner";
  return from !== "owner";
}

/** Group consecutive agent mail in place, or show each message in the reading line. */
export function foldAgentTraffic(items: Item[], showAgentMessages = false): Item[] {
  const out: Item[] = [];
  for (const it of items) {
    if (!((it.type === "mail" || it.type === "user") && isAgentTraffic(it.e)) &&
        !(it.type === "agent-message" && it.e.meta?.to !== "owner" &&
          (it.e.meta?.how !== "reply" || it.e.meta?.to))) {
      out.push(it);
      continue;
    }
    if (showAgentMessages) {
      out.push({ type: "agent-message", key: it.key, e: it.e });
      continue;
    }
    const last = out[out.length - 1];
    if (last?.type === "traffic") last.entries.push(it.e);
    else out.push({ type: "traffic", key: "m" + it.key, entries: [it.e] });
  }
  return out;
}

/** "1 agent message" / "3 agent messages". */
export function trafficLabel(n: number): string {
  return n === 1 ? "1 agent message" : `${n} agent messages`;
}

// ---- the classic conversation, easy to skim (the owner, 17 Nov 19:26: "for my brain it's extra
// work… some kind of effect or color… something elegant") ----
// He reads this view all day: his own messages and the agent's replies are the reading line;
// tool calls and agent mail are there, but out of the way and each in its own look.

/** An agent's own outgoing mail: its send / report / reply tool calls (mcp__<server>__send…). */
const MAIL_TOOL = /^mcp__[A-Za-z0-9_-]+?__(send|report|reply)$/;

export interface OutgoingMail {
  /** who it goes to: an agent's name, "owner", or "" when the call does not say (a report goes to the parent) */
  to: string;
  text: string;
  /** send, report or reply */
  how: string;
}

function inputOf(use: TimelineEntry | undefined): Record<string, unknown> {
  const v = use?.input;
  if (v && typeof v === "object" && !Array.isArray(v)) return v as Record<string, unknown>;
  if (typeof v === "string") {
    try {
      const o: unknown = JSON.parse(v);
      if (o && typeof o === "object" && !Array.isArray(o)) return o as Record<string, unknown>;
    } catch {
      /* not JSON */
    }
  }
  return {};
}

/** The mail a tool call sends, or null when the call is not mail (or says nothing). */
export function outgoingMail(pair: ToolPair): OutgoingMail | null {
  const m = MAIL_TOOL.exec(pair.use?.tool ?? "");
  if (!m || !pair.use) return null;
  const i = inputOf(pair.use);
  const text = [i.body, i.text, i.message].find((v) => typeof v === "string" && v.trim()) as string | undefined;
  if (!text) return null;
  const to = [i.to, pair.result?.meta?.to, pair.use.meta?.to, pair.result?.meta?.recipient, pair.use.meta?.recipient]
    .find((value): value is string => typeof value === "string" && !!value.trim())?.trim() ?? "";
  return { to, text: text.trim(), how: m[1] };
}

/** A message the agent sent to the owner: part of the reading line, like a reply. */
export function isToOwner(mail: OutgoingMail): boolean {
  return mail.to.toLowerCase() === "owner";
}

/**
 * The timeline entry that stands for an outgoing message in a run of agent mail (kind
 * "message_out", which the daemon never sends: it is made here from the tool call).
 */
function outEntry(pair: ToolPair, mail: OutgoingMail): TimelineEntry {
  const use = pair.use!;
  return { ...use, kind: "message_out" as TimelineEntry["kind"], text: mail.text, meta: { to: mail.to, how: mail.how, failed: !!pair.result?.is_error } };
}

/** Whether an entry of a mail run is one the agent sent (see outEntry). */
export function isOutgoing(e: TimelineEntry): boolean {
  return (e.kind as string) === "message_out";
}

/**
 * Extract outgoing mail from tool calls, then fold agent mail in both profiles.
 * Classic also filters the reading line, displays owner mail as a user turn, and joins tool runs.
 */
export function conversationItems(all: Item[], classic: boolean, showAgentMessages = false, parentId?: string): Item[] {
  const senders = new Map<string, string>();
  for (const item of all) {
    if (item.type !== "user" && item.type !== "mail") continue;
    const { message_id, from } = item.e.meta ?? {};
    if (typeof message_id === "string" && typeof from === "string" && from.trim()) senders.set(message_id, from.trim());
  }
  const step: Item[] = [];
  for (const it of all) {
    if (classic && !classicShows(it)) continue;
    if (classic && it.type === "mail" && !isAgentTraffic(it.e)) {
      step.push({ type: "user", key: it.key, e: it.e });
      continue;
    }
    if (it.type !== "tools") {
      step.push(it);
      continue;
    }
    let run: ToolPair[] = [];
    const flush = () => {
      if (run.length) step.push({ type: "tools", key: "g" + run[0].key, tools: run });
      run = [];
    };
    for (const pair of it.tools) {
      const mail = outgoingMail(pair);
      if (!mail) {
        run.push(pair);
        continue;
      }
      flush();
      if (mail.how === "report") mail.to = parentId ?? mail.to;
      if (mail.how === "reply" && !mail.to) {
        const { message_id, id } = inputOf(pair.use);
        const originalId = message_id ?? id;
        const sender = [pair.result?.meta?.reply_to_from, pair.use?.meta?.reply_to_from, pair.result?.meta?.from, pair.use?.meta?.from]
          .find((value): value is string => typeof value === "string" && !!value.trim() && value.trim() !== pair.use?.agent_id);
        mail.to = sender?.trim() ?? (typeof originalId === "string" ? senders.get(originalId) ?? "" : "");
      }
      if (isToOwner(mail)) mail.to = "owner";
      if (isToOwner(mail) && !pair.result?.is_error) step.push({ type: "assistant", key: "o" + pair.key, e: { ...pair.use!, kind: "assistant", text: mail.text } });
      else step.push({ type: "agent-message", key: "o" + pair.key, e: outEntry(pair, mail) });
    }
    flush();
  }
  const folded = foldAgentTraffic(step, showAgentMessages);
  if (!classic) return folded;
  const out: Item[] = [];
  for (const it of folded) {
    const last = out[out.length - 1];
    if (it.type === "tools" && last?.type === "tools") last.tools.push(...it.tools);
    else out.push(it.type === "tools" ? { ...it, tools: [...it.tools] } : it);
  }
  return out;
}

export function classicConversation(all: Item[]): Item[] {
  return conversationItems(all, true);
}

export interface ToolRunSummary {
  steps: number;
  failed: number;
  running: boolean;
  /** "3 steps", "1 step" */
  label: string;
}

/** What the closed card of a run of tool calls says. */
export function toolRunSummary(tools: readonly ToolPair[]): ToolRunSummary {
  const steps = tools.length;
  return {
    steps,
    failed: tools.filter((p) => p.result?.is_error).length,
    running: tools.some((p) => p.use && !p.result),
    label: steps === 1 ? "1 step" : `${steps} steps`,
  };
}

/** Latest unresolved call, even when a later parallel call has already finished. */
export function runningTool(tools: readonly ToolPair[]): ToolPair | undefined {
  return tools.findLast((p) => p.use && !p.result);
}

export function toolElapsed(at: number, now: number): string {
  const seconds = Math.max(0, Math.floor((now - at) / 1000));
  return seconds < 60 ? `${seconds}s` : `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
}
