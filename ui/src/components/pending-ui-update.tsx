import { useSyncExternalStore } from "react";
import { reloadGate } from "@/lib/reload-guard";

export function PendingUIUpdate() {
  const pending = useSyncExternalStore(reloadGate.subscribe, reloadGate.pending, () => false);
  if (!pending) return null;
  return <div role="status" className="shrink-0 border-b bg-muted px-4 py-2 text-sm">Update ready. Reloads when drafts, sends, and dialogs are finished.</div>;
}
