// The key row under an agent's terminal (classic UI profile; parity B3; lib/terminal-keys.ts):
// Enter, Esc, Ctrl+C, the arrows, Tab, 1 2 3, y, n. A click presses that key on the agent's
// screen, so a prompt there can be answered without the keyboard.
import { useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { hasLiveScreen, TERMINAL_KEYS } from "@/lib/terminal-keys";
import type { Agent } from "@/lib/types";
import { cn } from "@/lib/utils";

export function TerminalKeys({ agent }: { agent: Agent }) {
  const [busy, setBusy] = useState(false);
  if (!hasLiveScreen(agent)) return null;

  async function press(keys: string) {
    if (busy) return;
    setBusy(true);
    try {
      await api.pressKeys(agent.id, keys);
    } catch (e) {
      toast.error((e as Error).message || "The key was not sent.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="scroll-thin flex shrink-0 items-center gap-1 overflow-x-auto px-3 pb-2 md:px-4" role="group" aria-label={`Keys for ${agent.name}'s terminal`} data-terminal-keys>
      {TERMINAL_KEYS.map((k) => (
        <button
          key={k.keys}
          type="button"
          title={k.title}
          aria-label={k.title}
          disabled={busy}
          onClick={() => void press(k.keys)}
          className={cn(
            "inline-flex h-7 min-w-7 shrink-0 items-center justify-center rounded-md border bg-card px-2 font-mono text-xs text-foreground/85 outline-none transition-colors duration-150 hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-60",
            k.gap && "ml-2",
          )}
        >
          {k.label}
        </button>
      ))}
    </div>
  );
}
