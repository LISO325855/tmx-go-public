import { Mail } from "lucide-react";
import { DropdownMenu, DropdownMenuContent, DropdownMenuLabel, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { useStore } from "@/lib/store";
import { absoluteTime } from "@/lib/format";

const labels: Record<string, string> = {
  queued: "Waiting for a safe input box",
  stalled: "Not delivered, retry (submission was not confirmed)",
  dispatching: "Waiting for submission confirmation",
  accepted: "Accepted by the agent process; pickup not confirmed",
  submitted: "Written to the terminal; pickup not confirmed",
  unconfirmed: "Submission uncertain; not automatically resent",
  read: "Read through the agent's inbox",
  owner: "Available in your mailbox",
};

/** Local delivery effects, updated by the same message events as the mailbox: a small mail
 * badge in the agent's header (nothing without receipts); the last five open on a click. */
export function MailReceipts({ agentId }: { agentId: string }) {
  const messages = useStore((s) => s.messages);
  const agents = useStore((s) => s.agents);
  const recent = messages.filter((m) => m.receipt && (m.to === agentId || m.from === agentId)).slice(-5).reverse();
  if (!recent.length) return null;
  const latest = labels[recent[0].receipt!] ?? recent[0].state;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        className="mb-1.5 inline-flex h-6 shrink-0 items-center gap-1 self-end rounded-md border px-1.5 text-xs text-muted-foreground outline-none hover:bg-accent/60 focus-visible:ring-2 focus-visible:ring-ring"
        aria-label={`Mail delivery: ${latest}`}
        title={`Mail delivery: ${latest}`}
        data-mail-receipts
      >
        <Mail className="size-3.5" aria-hidden />
        <span>{recent.length}</span>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-[min(90vw,26rem)] p-3 text-xs">
        <DropdownMenuLabel className="px-0 pt-0 text-xs font-medium">Mail delivery · {latest}</DropdownMenuLabel>
        <ul className="space-y-2" aria-label="Recent mail delivery receipts">
          {recent.map((m) => (
            <li key={m.id}>
              <div>{m.from === "owner" ? "You" : agents[m.from]?.name ?? m.from} → {m.to === "owner" ? "you" : agents[m.to]?.name ?? m.to}: {labels[m.receipt!] ?? m.state}</div>
              <div className="truncate text-muted-foreground">{m.body.slice(0, 120)}</div>
              <div className="text-muted-foreground">{m.id} · {absoluteTime(m.receipt_at ?? m.created_at)}</div>
            </li>
          ))}
        </ul>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
