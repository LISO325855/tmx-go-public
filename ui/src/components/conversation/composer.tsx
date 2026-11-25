import { focusComposer } from "@/lib/composer-focus";
import { reloadGate } from "@/lib/reload-guard";
import { clearSubmittedDraft, ReloadSafeDrafts, ReloadSafeSends } from "@/lib/composer-draft";
import { forwardRef, useEffect, useImperativeHandle, useLayoutEffect, useRef, useState } from "react";
import { ArrowUp, Square } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { AttachButton, AttachChips, DropOverlay, useAttachments } from "@/components/attach";
import { DictateButton } from "@/components/dictate-button";
import { DictationInline } from "@/components/dictation-inline";
import { useClassic } from "@/hooks/use-classic";
import { useStore } from "@/lib/store";
import { api } from "@/lib/api";
import { MAX_FILE_BYTES, MAX_FILES } from "@/lib/attachments";
import { hasLiveScreen } from "@/lib/terminal-keys";
import { paths } from "@/lib/router";
import type { Agent } from "@/lib/types";
import { cn } from "@/lib/utils";

const drafts = new ReloadSafeDrafts("tmx.agent-composer-drafts");
const retries = new ReloadSafeSends("tmx.agent-pending-sends");

export interface ComposerHandle {
  focus(): void;
}

export const Composer = forwardRef<
  ComposerHandle,
  {
    agent: Agent;
    placeholder?: string;
    className?: string;
    autoFocus?: boolean;
    onSent?: () => void;
    minRows?: number;
    /** to the head: the board topic the message goes to */
    thread?: string;
  }
>(function Composer({ agent, placeholder, className, autoFocus, onSent, minRows = 1, thread }, ref) {
  const messages = useStore((s) => s.messages);
  const unavailable = !agent.pid || ["stopped", "exited", "error", "queued", "starting"].includes(agent.state);
  const queuedCount = messages.filter((m) => m.from === "owner" && m.to === agent.id && (!thread || m.thread_id === thread) &&
    (m.receipt === "menu_wait" || (unavailable && (m.state === "queued" || m.receipt === "dispatching")))).length;
  const key = thread ? `${agent.id}:${thread}` : agent.id;
  const [text, setText] = useState(() => drafts.get(key) ?? "");
  const [sending, setSending] = useState(false);
  const ta = useRef<HTMLTextAreaElement>(null);
  const classic = useClassic();
  const working = agent.state === "working" || agent.state === "waiting";
  const terminal = agent.mode === "terminal" && agent.harness === "shell"; // a harness's screen gets messages typed in
  const att = useAttachments("agent:" + key, (f) => api.upload(agent.id, f), {
    maxFiles: MAX_FILES,
    maxBytes: MAX_FILE_BYTES,
    disabled: terminal,
  });

  useImperativeHandle(ref, () => ({ focus: () => focusComposer(ta.current) }));

  useEffect(() => {
    setText(drafts.get(key) ?? "");
  }, [key]);

  useEffect(() => {
    if (autoFocus) focusComposer(ta.current);
  }, [autoFocus, agent.id]);

  useLayoutEffect(() => {
    const el = ta.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = Math.min(el.scrollHeight, 240) + "px";
  }, [text]);

  const canSend = (!!text.trim() || att.readyIds.length > 0) && !att.uploading && !sending && !terminal;

  async function send(voiceText?: string, voiceId?: string, signal?: AbortSignal) {
    const voice = voiceText !== undefined;
    const body = (voiceText ?? text).trim();
    if (!voice && !canSend) return;
    if (voice && terminal) throw new Error("Use the terminal to send this message.");
    const releaseReload = reloadGate.hold();
    setSending(true);
    const attachments = voice ? [] : att.readyIds;
    const envelope = JSON.stringify([agent.id, body, attachments, thread]);
    let retry = voice ? { envelope, id: voiceId || crypto.randomUUID() } : retries.get(key);
    if (retry?.envelope !== envelope) {
      retry = { envelope, id: crypto.randomUUID() };
      retries.set(key, retry);
    }
    try {
      await api.send(agent.id, body, attachments, thread, retry.id, signal);
      if (!voice) retries.delete(key);
      if (!voice && clearSubmittedDraft(drafts, key, text)) setText("");
      if (!voice) att.clear();
      if (!voice) onSent?.();
    } catch (e) {
      if (voice) throw e;
      toast.error((e as Error).message || "The message wasn't sent.");
    } finally {
      setSending(false);
      releaseReload();
      if (!voice) focusComposer(ta.current);
    }
  }

  // Classic "Paste only" (Python's button; parity B4): the text goes into the agent's screen
  // without Enter, so it can be looked at or finished there. Only where there is a screen to
  // paste into, and never with attachments (those travel with a sent message).
  const canPaste = classic && !thread && hasLiveScreen(agent) && agent.harness !== "shell";
  async function pasteOnly() {
    const body = text;
    if (!body.trim() || sending) return;
    const releaseReload = reloadGate.hold();
    setSending(true);
    try {
      await api.typeText(agent.id, body, false);
      if (clearSubmittedDraft(drafts, key, text)) setText("");
    } catch (e) {
      toast.error((e as Error).message || "The text wasn't pasted.");
    } finally {
      setSending(false);
      releaseReload();
      focusComposer(ta.current);
    }
  }

  async function interrupt() {
    try {
      await api.agentAction(agent.id, "interrupt");
    } catch (e) {
      toast.error((e as Error).message);
    }
  }

  return (
    <>
      <DictationInline boxId={"agent:" + key} />
      {queuedCount > 0 && <div role="status" className="mb-1 text-xs text-muted-foreground">{queuedCount} queued</div>}
    <div
      className={cn(
        "relative min-w-0 rounded-xl border border-border-strong bg-card transition-[border-color,box-shadow] duration-150 focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/25",
        att.dragging && "border-ring ring-2 ring-ring/25",
        className,
      )}
      {...att.dropProps}
    >
      <DropOverlay show={att.dragging} />
      <AttachChips files={att.files} onRemove={att.remove} />
      <textarea
        ref={ta}
        value={text}
        rows={minRows}
        onChange={(e) => {
          setText(e.target.value);
          drafts.set(key, e.target.value);
        }}
        onPaste={(e) => {
          if (terminal || !e.clipboardData.files.length) return;
          e.preventDefault();
          att.add(e.clipboardData.files);
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
            e.preventDefault();
            void send();
          } else if (e.key === "Escape") {
            ta.current?.blur();
          }
        }}
        placeholder={placeholder ?? (terminal ? `Type into ${agent.name}'s terminal from the Terminal tab` : `Message ${agent.name}…`)}
        disabled={terminal}
        aria-label={`Message ${agent.name}`}
        className="scroll-thin block max-h-60 w-full resize-none bg-transparent px-3.5 pt-3 pb-1 text-sm leading-relaxed outline-none placeholder:text-subtle-foreground disabled:cursor-not-allowed"
      />
      <div data-composer-actions className="flex min-w-0 flex-wrap items-center gap-2 px-2 pb-2">
        <AttachButton
          disabled={terminal}
          onFiles={(f) => {
            att.add(f);
            focusComposer(ta.current);
          }}
        />
        <DictateButton
              onSend={send}
          target={ta}
          value={text}
          draft={{ id: "agent:" + key, label: agent.name, path: paths.agent(agent.id), drafts, draftKey: key }}
          disabled={terminal}
          onChange={(v) => {
            setText(v);
            drafts.set(key, v);
          }}
        />
        <span className="hidden min-w-0 truncate text-2xs text-subtle-foreground sm:block">
          {classic
              ? "" // classic: no key hint under the box, as in Python
              : "Enter to send · Shift+Enter for a new line"}
        </span>
        <div className="ml-auto flex min-w-0 flex-wrap items-center justify-end gap-1.5">
          {working && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button variant="outline" size="sm" onClick={() => void interrupt()} className="gap-1.5">
                  <Square className="size-3 fill-current" />
                  Interrupt
                </Button>
              </TooltipTrigger>
              <TooltipContent>Stop the current turn; the agent keeps running</TooltipContent>
            </Tooltip>
          )}
          {canPaste && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button variant="outline" size="sm" onClick={() => void pasteOnly()} disabled={!text.trim() || sending || att.readyIds.length > 0 || att.uploading} data-paste-only>
                  Paste only
                </Button>
              </TooltipTrigger>
              <TooltipContent>Put the text into {agent.name}'s screen without sending it</TooltipContent>
            </Tooltip>
          )}
          <Button size="icon-sm" onClick={() => void send()} disabled={!canSend} aria-label={att.uploading ? "Uploading…" : "Send"} className="rounded-lg">
            <ArrowUp className="size-4" />
          </Button>
        </div>
      </div>
    </div>
    </>
  );
});
