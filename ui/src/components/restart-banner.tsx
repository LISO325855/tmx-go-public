import { useEffect, useState } from "react";
import { onBusEvent, useStore } from "@/lib/store";

export function RestartBanner() {
  const transport = useStore(s => s.transport);
  const conn = useStore(s => s.conn);
  const [at, setAt] = useState(0);
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    let live = true;
    void transport?.request<{ at: number }>("GET", "/api/restart-notice").then(n => { if (live) setAt(n.at); }).catch(() => {});
    const off = onBusEvent(ev => {
      if (ev.type === "daemon.restart-notice") setAt((ev.data as { at: number })?.at || 0);
    });
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => { live = false; off(); clearInterval(timer); };
  }, [transport, conn]);
  // A missed cancellation must not leave an obsolete warning on screen indefinitely.
  if (!at || now > at + 5 * 60_000) return null;
  const seconds = Math.max(0, Math.ceil((at - now) / 1000));
  return <div role="status" aria-live="polite" className="shrink-0 border-b bg-amber-100 px-4 py-2 text-sm text-amber-950" data-restart-banner>
    {seconds > 0 ? `Restart in ${seconds} s, finish your sentence.` : "Restart is waiting for recordings to finish."}
  </div>;
}
