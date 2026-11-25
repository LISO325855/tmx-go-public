// Team (#/team): team status (DESIGN.md "Team status"). The groups you run show everyone who
// shares their status with you; a group whose admin you share with shows your own row (or the
// whole team, when its admin opened it). Above the rows, the opt-ins still waiting for an answer.
import { useEffect, useState } from "react";
import { PlugZap, RefreshCw, Users } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { EmptyState, PageBody, PageHeader, PageTitle } from "@/components/page";
import { ShareOffer } from "@/components/team/share-offer";
import { TeamView } from "@/components/team/team-view";
import { useNow } from "@/hooks/use-now";
import { loadTeam, teamApi, useTeam } from "@/lib/team";
import { unasked } from "@/lib/team-format";
import { cn } from "@/lib/utils";

export function TeamPage() {
  const page = useTeam((s) => s.page);
  const loaded = useTeam((s) => s.loaded);
  const error = useTeam((s) => s.error);
  const now = useNow(30_000);
  const [picked, setPicked] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => void loadTeam(), []);

  const refresh = async () => {
    setBusy(true);
    await loadTeam();
    setBusy(false);
  };
  const open = async (chatId: string, v: boolean) => {
    try {
      await teamApi.open(chatId, v);
    } catch (e) {
      toast.error("Could not change who sees the team", { description: (e as Error).message });
    }
  };

  const group = page?.groups.find((g) => g.chat_id === picked) ?? page?.groups[0];
  const offers = unasked(page);
  const self = page?.self;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageHeader
        actions={
          <Button variant="outline" size="sm" disabled={!page || busy} onClick={() => void refresh()}>
            <RefreshCw className={cn(busy && "animate-spin")} />
            Refresh
          </Button>
        }
      >
        <Users className="size-4 shrink-0 text-muted-foreground" />
        <PageTitle>Team</PageTitle>
      </PageHeader>
      <PageBody width="max-w-[84rem]" className="space-y-6">
        {!page ? (
          loaded && error ? (
            <EmptyState
              icon={PlugZap}
              title={error.status === 503 && error.message.startsWith("peer-to-peer is off") ? "Peer-to-peer is off" : error.status === 404 ? "This tmx has no Team page yet" : "Could not load the team"}
              actions={
                <Button variant="outline" size="sm" onClick={() => void loadTeam()}>
                  <RefreshCw />
                  Try again
                </Button>
              }
            >
              {error.status === 503 ? "Team status travels in your groups' chats; turn peer-to-peer on to see it." : error.status === 404 ? "Update tmx to see your team's status." : error.message}
            </EmptyState>
          ) : (
            <div className="space-y-3" aria-busy="true" aria-label="Loading the team">
              <Skeleton className="h-7 w-64 max-w-full" />
              <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
                {[0, 1, 2, 3].map((i) => (
                  <Skeleton key={i} className="h-28" />
                ))}
              </div>
              <Skeleton className="h-40" />
            </div>
          )
        ) : (
          <>
            {offers.map((o) => (
              <ShareOffer key={o.chat_id} offer={o} showGroup />
            ))}
            {group ? (
              <TeamView page={page} group={group} now={now} onOpen={(id, v) => void open(id, v)} onPick={setPicked} />
            ) : (
              <EmptyState icon={Users} title="No team to show yet">
                When you run a group, everyone who shares their status with you shows up here. In a group someone else runs, share your status
                to see your own row.
              </EmptyState>
            )}
            {self && (
              <p className="text-[0.9375rem] leading-6 text-muted-foreground" data-testid="self-contract">
                This computer passes{" "}
                <span className="tabular font-medium text-foreground">
                  {self.passed} of {page.checks}
                </span>{" "}
                checks of contract v{page.contract}; {page.checks - self.checked} of them need more than a status report and are not checked here.
              </p>
            )}
          </>
        )}
      </PageBody>
    </div>
  );
}
