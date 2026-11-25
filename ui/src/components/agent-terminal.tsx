// The Terminal view of an agent under the classic UI profile (lib/ui-profile.ts; the owner #7,
// 17 Nov: Python's toggle, "a click only changes the view"). An agent in its own screen shows
// it at once, live in xterm, mid-turn too: no bar, no card, no explainer. A Claude or Codex
// agent still in chat mode has no screen; this view says so in one line and offers the change
// as a button of its own (it happens when the turn ends: tmx never cuts a turn short). The
// default dashboard keeps components/harness-screen.tsx.
import { lazy, Suspense } from "react";
import { Loader2, Play, SquareTerminal } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { canOpenScreen, switchMode } from "@/components/harness-screen";
import { api } from "@/lib/api";
import { isRunning, STATE_LABEL } from "@/lib/format";
import { harnessLabel } from "@/lib/harness";
import { agentTerminalPath } from "@/lib/transport";
import type { Agent, Mode } from "@/lib/types";

const TerminalView = lazy(() => import("@/components/terminal-view").then((m) => ({ default: m.TerminalView })));

async function startIn(agent: Agent, mode: Mode) {
  if (agent.mode !== mode && !(await switchMode(agent, mode))) return;
  try {
    await api.agentAction(agent.id, "start");
  } catch (e) {
    toast.error((e as Error).message);
  }
}

/** One quiet line (and at most one button) where a screen would be. */
function Line({ children, action, busy }: { children: React.ReactNode; action?: React.ReactNode; busy?: boolean }) {
  return (
    <div className="flex shrink-0 items-center border-b px-3 py-2">
      <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground" role="status" aria-live="polite">
        {busy ? <Loader2 className="size-4 shrink-0 animate-spin" aria-hidden /> : <SquareTerminal className="size-4 shrink-0" aria-hidden />}
        <span className="min-w-0">{children}</span>
        {action}
      </div>
    </div>
  );
}

export function AgentTerminal({ agent, autoFocus = true }: { agent: Agent; autoFocus?: boolean }) {
  const label = harnessLabel(agent.harness);
  const running = isRunning(agent.state);

  if (agent.mode === "terminal") {
    if (running && agent.pid > 0) {
      return (
        <Suspense fallback={<div className="flex-1" />}>
          <TerminalView key={agent.pid} path={agentTerminalPath(agent.id)} autoFocus={autoFocus} />
        </Suspense>
      );
    }
    if (agent.state === "starting") return <Line busy>Opening {label}'s screen…</Line>;
    return (
      <Line
        action={
          agent.state !== "queued" && (
            <Button size="sm" onClick={() => void startIn(agent, "terminal")}>
              <Play className="fill-current" /> Start
            </Button>
          )
        }
      >
        {agent.name} is {STATE_LABEL[agent.state].toLowerCase()}, so {label}'s screen is closed.{agent.error ? ` ${agent.error}` : ""}
      </Line>
    );
  }

  // Chat mode (Claude Code, Codex): there is no screen until the owner asks for one.
  if (agent.pending_mode === "terminal") {
    if (agent.state === "idle") return <Line busy>Opening {label}'s screen…</Line>;
    return (
      <Line
        busy
        action={
          <Button size="sm" variant="ghost" onClick={() => void switchMode(agent, "chat")}>
            Cancel
          </Button>
        }
      >
        {label}'s screen opens when this turn ends.
      </Line>
    );
  }
  if (canOpenScreen(agent)) {
    return (
      <Line
        action={
          <Button size="sm" variant="outline" onClick={() => void switchMode(agent, "terminal", true)}>
            <SquareTerminal /> Open {label}'s screen
          </Button>
        }
      >
        {agent.name} is in chat mode.
      </Line>
    );
  }
  if (agent.state === "queued" || agent.state === "paused") {
    return (
      <Line>
        {agent.name} runs in chat mode, which has no screen to show, and is {STATE_LABEL[agent.state].toLowerCase()}.
      </Line>
    );
  }
  return (
    <Line
      action={
        <Button size="sm" variant="outline" onClick={() => void startIn(agent, "terminal")}>
          <Play className="fill-current" /> Start in {label}'s screen
        </Button>
      }
    >
      {agent.name} runs in chat mode, which has no screen to show, and is {STATE_LABEL[agent.state].toLowerCase()}.
    </Line>
  );
}
