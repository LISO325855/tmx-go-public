import { useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import { Dialog } from "radix-ui";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { retryNow, useStore } from "@/lib/store";
import { daemonRestartAction } from "@/lib/transport";
import { cn } from "@/lib/utils";

export function DaemonUnavailable() {
  const restart = daemonRestartAction();
  const [starting, setStarting] = useState(false);
  const [failed, setFailed] = useState(false);

  async function recover() {
    if (!restart || failed) {
      retryNow();
      return;
    }
    setStarting(true);
    try {
      await restart();
    } catch {
      setFailed(true);
    } finally {
      setStarting(false);
      retryNow();
    }
  }

  return (
    <Dialog.Root open>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-[200] bg-background" />
        <Dialog.Content
          aria-describedby={undefined}
          className="drag-region fixed inset-0 z-[201] flex flex-col items-center justify-center gap-4 p-6 text-center"
          onEscapeKeyDown={(event) => event.preventDefault()}
          onInteractOutside={(event) => event.preventDefault()}
        >
          <Dialog.Title>Tiamatrix lost its connection to its program</Dialog.Title>
          <Button onClick={() => void recover()} disabled={starting}>
            {starting ? "Restarting…" : restart && !failed ? "Restart it" : "Reconnect after restarting Tiamatrix"}
          </Button>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

/** True once the event stream has been down for more than `after` ms. */
export function useDisconnected(after = 3000): boolean {
  const conn = useStore((s) => s.conn);
  const [late, setLate] = useState(false);
  useEffect(() => {
    if (conn === "open") {
      setLate(false);
      return;
    }
    const id = setTimeout(() => setLate(true), after);
    return () => clearTimeout(id);
  }, [conn, after]);
  return conn !== "open" && late;
}

export function ConnectionDot({ className }: { className?: string }) {
  const conn = useStore((s) => s.conn);
  const kind = useStore((s) => s.transport?.kind);
  const late = useDisconnected();
  const label =
    conn === "open"
      ? kind === "mock"
        ? "Connected to the mock daemon"
        : kind === "bridge"
          ? "Connected through the desktop bridge"
          : "Connected to the daemon"
      : late
        ? "Reconnecting to the daemon"
        : "Connecting";
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          role="img"
          aria-label={label}
          className={cn(
            "inline-block size-1.5 shrink-0 rounded-full outline-none focus-visible:ring-2 focus-visible:ring-ring",
            conn === "open" ? "bg-state-ok" : late ? "bg-state-waiting" : "bg-state-idle",
            className,
          )}
        />
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  );
}

export function ReconnectingPill({ className }: { className?: string }) {
  const late = useDisconnected();
  if (!late) return null;
  return (
    <span
      role="status"
      className={cn(
        "inline-flex items-center gap-1 rounded-full border bg-background px-2 py-0.5 text-2xs font-medium text-muted-foreground",
        className,
      )}
    >
      <Loader2 className="size-3 animate-spin" />
      Reconnecting…
    </span>
  );
}
