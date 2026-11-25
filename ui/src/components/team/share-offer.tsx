// The opt-in, once per group: "Share this computer's status with <admin>: …. Never any content."
// One tap shares; "Not now" says no (Settings → General → Team status turns it on later).
import { useState } from "react";
import { Activity } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { shareSentence } from "@/lib/team-format";
import { teamApi, type TeamSharing } from "@/lib/team";
import { cn } from "@/lib/utils";

export function ShareOffer({ offer, className, showGroup }: { offer: TeamSharing; className?: string; showGroup?: boolean }) {
  const [busy, setBusy] = useState(false);
  const answer = async (share: boolean) => {
    setBusy(true);
    try {
      await teamApi.share(offer.chat_id, share);
      if (share) toast.success(`${offer.admin_name} now sees this computer's status`, { description: "Turn it off any time in Settings → General." });
    } catch (e) {
      toast.error("Could not save that", { description: (e as Error).message });
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className={cn("flex flex-wrap items-start gap-3 rounded-xl border bg-card px-4 py-3", className)} role="region" aria-label="Share your status" data-share-offer={offer.chat_id}>
      <Activity className="mt-0.5 size-5 shrink-0 text-muted-foreground" aria-hidden />
      <div className="min-w-0 flex-1 basis-64">
        {showGroup && <div className="text-[0.8125rem] font-medium text-muted-foreground">{offer.name}</div>}
        <p className="text-[0.9375rem] leading-6">{shareSentence(offer.admin_name)}</p>
      </div>
      <div className="flex shrink-0 items-center gap-2">
        <Button size="sm" disabled={busy} onClick={() => void answer(true)}>
          Share
        </Button>
        <Button size="sm" variant="ghost" disabled={busy} onClick={() => void answer(false)}>
          Not now
        </Button>
      </div>
    </div>
  );
}
