// A paired phone (or any browser on the HTTPS address) that cannot reach the computer
// (docs/PHONE-PAIRING.md). It names the usual causes in the order to check them and keeps
// trying by itself; the page's shell comes from the service worker (public/push-sw.js), so
// this also shows when tmx is opened cold with Tailscale off.
import { useEffect, useState } from "react";
import { Monitor, RefreshCw, ShieldCheck, Wifi, WifiOff } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cantReachCauses } from "@/lib/phone-connect";

const ICONS = { internet: WifiOff, tailscale: ShieldCheck, wifi: Wifi, computer: Monitor } as const;

export function CantReach({ onRetry }: { onRetry: () => void }) {
  const [online, setOnline] = useState(() => navigator.onLine);
  useEffect(() => {
    const on = () => {
      setOnline(true);
      onRetry(); // the connection is back: try at once
    };
    const off = () => setOnline(false);
    const seen = () => {
      if (document.visibilityState === "visible") onRetry();
    };
    window.addEventListener("online", on);
    window.addEventListener("offline", off);
    document.addEventListener("visibilitychange", seen);
    return () => {
      window.removeEventListener("online", on);
      window.removeEventListener("offline", off);
      document.removeEventListener("visibilitychange", seen);
    };
  }, [onRetry]);
  const host = window.location.hostname;
  return (
    <div className="w-full max-w-md rounded-xl border p-6" role="alert">
      <h1 className="text-xl font-semibold">Can't reach your computer</h1>
      <p className="mt-1 text-sm text-muted-foreground">tmx keeps trying by itself. Check these, in order:</p>
      <ol className="mt-4 space-y-3">
        {cantReachCauses(online, host).map((c) => {
          const Icon = ICONS[c.kind];
          return (
            <li key={c.kind} className="flex gap-3">
              <Icon className="mt-0.5 size-5 shrink-0 text-muted-foreground" />
              <div className="min-w-0">
                <div className="text-sm font-medium">{c.title}</div>
                <div className="text-sm text-muted-foreground">{c.text}</div>
              </div>
            </li>
          );
        })}
      </ol>
      <Button className="mt-6 w-full" size="lg" onClick={onRetry}>
        <RefreshCw />
        Try again
      </Button>
    </div>
  );
}
