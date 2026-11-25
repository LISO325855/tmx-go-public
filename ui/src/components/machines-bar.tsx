// The Machines bar: the owner's own machines as one fleet (GET /api/fleet/hosts: this machine,
// the fleet's other machines - Go or Python - and other local daemons). One chip per machine:
// whether it answers, agents running of all its agents, how many wait for the owner, and its CPU
// when it sends a reading. A chip opens that machine: its page here for another machine
// (pages/machine.tsx), Home for this one, its own window for another local daemon. Shown only
// when there is another machine besides this one. Everything read from another machine is its
// own data: rendered as plain text only, never as HTML.
import { useEffect } from "react";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useFleetHosts, watchFleetHosts, type HostSummary } from "@/lib/fleet-hosts";
import { machineAgents, machineCpu, machineDetail, machineRows, machineTone, showsMachinesBar, type MachineTone } from "@/lib/fleet-hosts-format";
import { href, paths, usePath } from "@/lib/router";
import { cn } from "@/lib/utils";

const DOT: Record<MachineTone, string> = {
  online: "bg-state-working",
  offline: "bg-state-error",
  unpaired: "border-[1.5px] border-dashed border-muted-foreground/70",
};

const TONE_LABEL: Record<MachineTone, string> = { online: "online", offline: "not reachable", unpaired: "not paired" };

export function MachinesBar() {
  const hosts = useFleetHosts((s) => s.hosts);
  const self = useFleetHosts((s) => s.self);
  useEffect(() => watchFleetHosts(), []);
  if (!showsMachinesBar(self, hosts)) return null;
  return (
    <nav aria-label="Machines" className="flex shrink-0 items-center gap-1.5 overflow-x-auto border-b px-5 py-1.5 md:px-6">
      <span className="mr-1 shrink-0 text-2xs font-medium text-subtle-foreground">Machines</span>
      {machineRows(self, hosts).map((h) => (
        <MachineChip key={`${h.kind}:${h.name}`} h={h} />
      ))}
      <a href={href(paths.team())} className="ml-auto shrink-0 rounded-md px-2 py-1 text-xs text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring">
        Team
      </a>
      <a href={href(paths.watchdog())} className="shrink-0 rounded-md px-2 py-1 text-xs text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring">
        Watchdog
      </a>
    </nav>
  );
}

/** Where a chip leads: another local daemon has its own window (a loopback address this daemon
 * was configured with, never one a machine sent). */
function chipTarget(h: HostSummary): { href: string; external: boolean } {
  if (h.kind === "host") return { href: `http://127.0.0.1:${h.port}/`, external: true };
  return { href: href(h.kind === "self" ? paths.overview() : paths.machine(h.name)), external: false };
}

function MachineChip({ h }: { h: HostSummary }) {
  const path = usePath();
  const tone = machineTone(h);
  const cpu = machineCpu(h);
  const detail = machineDetail(h, Date.now());
  const target = chipTarget(h);
  const current = h.kind === "peer" && path === paths.machine(h.name);
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <a
          href={target.href}
          {...(target.external ? { target: "_blank", rel: "noreferrer" } : {})}
          aria-label={`${h.name}, ${TONE_LABEL[tone]}`}
          aria-current={current ? "page" : undefined}
          className={cn(
            "flex shrink-0 items-center gap-1.5 rounded-md border bg-card px-2 py-1 text-xs outline-none transition-colors duration-150 hover:border-border-strong focus-visible:ring-2 focus-visible:ring-ring",
            tone !== "online" && "text-muted-foreground",
            current && "border-border-strong bg-accent",
          )}
        >
          <span className={cn("inline-flex size-2 shrink-0 rounded-full", DOT[tone])} />
          <span className="max-w-[10rem] truncate font-medium">{h.name}</span>
          {tone === "online" ? (
            <>
              <span className="tabular text-2xs text-subtle-foreground">{machineAgents(h)}</span>
              {h.needs_you > 0 && <span className="tabular rounded-sm bg-state-waiting/15 px-1 text-2xs text-foreground">{h.needs_you}</span>}
              {cpu && <span className="tabular text-2xs text-subtle-foreground">{cpu}</span>}
            </>
          ) : (
            <span className="text-2xs">{TONE_LABEL[tone]}</span>
          )}
        </a>
      </TooltipTrigger>
      <TooltipContent side="bottom" className="max-w-xs">
        {detail.map((line, i) => (
          <p key={i} className={cn("break-words", i === 0 && "font-medium")}>
            {line}
          </p>
        ))}
      </TooltipContent>
    </Tooltip>
  );
}
