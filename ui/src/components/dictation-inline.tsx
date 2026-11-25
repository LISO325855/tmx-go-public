// The dictation state above each message box (the owner #14, 17 Nov:
// "an inline indicator in the message/composer area while recording or transcribing, nothing
// permanent"; Python keeps the microphone state in the message box). One slim row above the box
// being dictated into: elapsed time, Stop and Throw away while recording, or a kept-clip status.
// While it is on screen the app-wide bar (components/dictation-bar.tsx) steps aside.
import { useEffect, useState } from "react";
import { create } from "zustand";
import { Loader2, Square, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cancelDictating, dictationElapsed, stopDictating, useDictation } from "@/lib/dictation-session";
import { useVoice } from "@/lib/voice";
import { acceptDictation, clearDictationNotice, useDictationOutbox } from "@/lib/dictation-outbox";

/** Which dictation boxes show their own state right now (by box id: "agent:<id>"), counted. */
export const useInlineDictation = create<{ boxes: Record<string, number> }>(() => ({ boxes: {} }));

function mount(id: string, by: number) {
  useInlineDictation.setState((s) => {
    const n = Math.max(0, (s.boxes[id] ?? 0) + by);
    const boxes = { ...s.boxes };
    if (n > 0) boxes[id] = n;
    else delete boxes[id];
    return { boxes };
  });
}

export function dictationClock(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

/** boxId: the dictation target this message box registers as (the composer's "agent:<id>"). */
export function DictationInline({ boxId }: { boxId: string }) {
  const kept = useDictationOutbox(s => s.notices[boxId] ?? s.clips.find(c => c.target.id === boxId && c.text === undefined));
  const cleanupError = useDictationOutbox(s => s.error);
  const pending = useVoice((s) => s.pending);
  const phase = useDictation((s) => s.phase);
  const mine = useDictation((s) => s.target?.id === boxId);
  const [elapsed, setElapsed] = useState(0);
  const [actionError, setActionError] = useState("");
  const act = (action: Promise<void>) => { setActionError(""); void action.catch(e => setActionError((e as Error).message || "Could not update this recording.")); };

  // registered while mounted, so the app-wide bar knows this box shows the state itself
  useEffect(() => {
    mount(boxId, 1);
    return () => { mount(boxId, -1); clearDictationNotice(boxId); };
  }, [boxId]);

  useEffect(() => {
    if (!mine || phase !== "recording") return;
    const tick = () => setElapsed(dictationElapsed());
    tick();
    const id = setInterval(tick, 500);
    return () => clearInterval(id);
  }, [mine, phase]);

  if (!mine || phase === "idle") return kept ? (
    <div role="status" data-dictation-inline className="mb-2 min-w-0 text-xs [overflow-wrap:anywhere]">
      {kept.held ? <>
        <p className="font-medium">{kept.heldReason === "send_uncertain" ? "Send uncertain" : kept.heldReason === "send_failed" ? "Message not sent" : "Text unsure: review before sending."}</p>
        <p>{["send_failed", "send_uncertain"].includes(kept.heldReason ?? "") ? kept.error : kept.heldReason === "unexpected_language" ? "The detected language was outside your dictation languages."
          : kept.heldReason === "short" ? "There was too little clear speech to be confident."
          : "The speech model was unsure. Background noise may affect the recording."}</p>
        <p className="my-2 whitespace-pre-wrap rounded border p-2">{kept.text}</p>
      </> : kept.delivered && kept.note ? (
        // Already delivered as dictated; this note is informational only (never reopens it for review).
        <p className="text-muted-foreground">{kept.note}</p>
      ) : <p>{kept.error || "Transcribing..."}</p>}
      {(kept.held || (kept.delivered && kept.note)) && <div className="mt-1 flex flex-wrap gap-2">
        {kept.held && !!kept.text?.trim() && <Button size="xs" onClick={() => act(acceptDictation(kept))}>Use text anyway</Button>}
        <Button size="xs" variant="ghost" onClick={() => clearDictationNotice(boxId)}>{kept.held ? "Discard" : "Dismiss"}</Button>
      </div>}
      {actionError && <p role="alert">{actionError}</p>}
    </div>
  ) : cleanupError ? <div role="alert" data-dictation-inline className="mb-2 min-w-0 text-xs [overflow-wrap:anywhere]">{cleanupError}</div> : null;
  // a transcription that waits to be retried gets no indicator (as for the bar)
  if (phase === "working" && pending) return null;
  const recording = phase === "recording";

  return (
    <div role="status" aria-live="polite" data-dictation-inline className="mb-2 flex min-w-0 flex-wrap items-center gap-2 rounded-lg border border-state-error/30 bg-state-error/10 px-3 py-1 text-xs text-foreground">
      <span className="relative flex size-2 shrink-0" aria-hidden>
        {recording && <span className="absolute inline-flex size-full animate-ping rounded-full bg-state-error opacity-60" />}
        <span className="relative inline-flex size-2 rounded-full bg-state-error" />
      </span>
      <span className="min-w-0 font-medium [overflow-wrap:anywhere]">{recording ? "Dictating" : phase === "starting" ? "Waiting for the microphone…" : "Transcribing…"}</span>
      {recording && <span className="tabular shrink-0 font-mono">{dictationClock(elapsed)}</span>}
      <div className="ml-auto flex shrink-0 items-center gap-1">
        {phase === "working" ? (
          <Loader2 className="size-3.5 animate-spin text-muted-foreground" />
        ) : (
          <>
            <Button variant="ghost" size="xs" className="gap-1" onClick={() => cancelDictating()} aria-label="Throw the dictation away">
              <X />
              <span className="hidden sm:inline">Throw away</span>
            </Button>
            {recording && (
              <Button variant="destructive" size="xs" className="gap-1" onClick={() => void stopDictating()}>
                <Square className="fill-current" />
                Stop
              </Button>
            )}
          </>
        )}
      </div>
    </div>
  );
}
