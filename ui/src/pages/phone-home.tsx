// Home on a phone in the classic profile: the Python mobile app's Home, in its order. The logo
// and the name, a headline, the "Your turn" card (the first thing that needs you), the disk of
// each machine, up to three conversations to pick up, up to three projects moving forward, then
// plain rows to everything else (they replace the menu). No message box, no activity feed.
import { useEffect, useMemo } from "react";
import { ChevronRight } from "lucide-react";
import { AgentAvatar } from "@/components/agent-avatar";
import { approvalHeadline } from "@/components/approval";
import { BrandMark } from "@/components/brand-mark";
import { HeadPausedBanner } from "@/components/resources/machine-strip";
import { StateDot } from "@/components/state";
import { useNow } from "@/hooks/use-now";
import { classicAgentVisible } from "@/lib/classic-agents";
import { dailyLine, greeting } from "@/lib/daily";
import { loadFleetHosts, useFleetHosts } from "@/lib/fleet-hosts";
import { relativeTime } from "@/lib/format";
import { PHONE_HOME_ROWS, askingAgents, homeAgents, phoneAgentRows, phoneProjectTiles, projectTileLine, stateTone } from "@/lib/phone";
import { agentLabel } from "@/lib/profiles";
import { bytes, useResources } from "@/lib/resources";
import { href, paths } from "@/lib/router";
import { pendingApprovals, useStore } from "@/lib/store";
import { summarizeTool } from "@/lib/tools";
import { headWord } from "@/lib/ui-profile";
import { cn } from "@/lib/utils";

const TONE = {
  you: "text-[color-mix(in_oklch,var(--state-waiting)_75%,var(--foreground))]",
  moving: "text-state-working",
  fault: "text-state-error",
  quiet: "text-muted-foreground",
} as const;

const amber = "text-[color-mix(in_oklch,var(--state-waiting)_60%,var(--foreground))]";

function SectionTitle({ title, to, more }: { title: string; to: string; more: string }) {
  return (
    <div className="mt-6 mb-1 flex items-center justify-between gap-2">
      <h2 className="text-[13px] font-semibold">{title}</h2>
      <a href={href(to)} className="flex min-h-11 items-center text-[12px] text-muted-foreground">
        {more}
      </a>
    </div>
  );
}

/** The first thing that needs you, as Python's "attention" card; it opens Your turn on that card. */
function YourTurnCard() {
  const approvals = useStore((s) => s.approvals);
  const agents = useStore((s) => s.agents);
  const list = pendingApprovals(approvals);
  const first = list[0];
  if (!first) return null;
  const agent = agents[first.agent_id];
  const tool = summarizeTool(first.tool, first.input, agent?.cwd);
  const text = first.kind === "question" || first.kind === "companion" || first.kind === "whatsapp" ? first.question : first.kind === "brake" ? first.description : tool.detail;
  return (
    <a
      href={href(paths.inbox() + "?id=" + first.id)}
      aria-label={`Your turn: ${list.length} waiting`}
      data-phone-your-turn
      className="block rounded-[18px] border border-state-waiting/45 bg-state-waiting/[0.1] px-[18px] pt-[19px] pb-4 outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      <span className={cn("flex items-center gap-[7px] text-[12px] font-semibold tracking-[0.08em] uppercase", amber)}>
        <span className="size-[7px] rounded-full bg-state-waiting" aria-hidden />
        Your turn
        <span className="tabular ml-auto">{list.length === 1 ? "1 waiting" : `1 of ${list.length}`}</span>
      </span>
      <span className="mt-4 line-clamp-3 block text-[22px] leading-[1.2] font-semibold tracking-[-0.03em] [overflow-wrap:anywhere]">{text || approvalHeadline(first)}</span>
      <span className="mt-[18px] flex items-center gap-2 text-[12px]">
        {agent && <AgentAvatar agent={agent} className="size-7 rounded-lg" />}
        <span className="min-w-0 truncate">
          {agent ? (agent.level === "head" ? headWord(true) : agent.name) : "An agent"} · {relativeTime(first.created_at)}
        </span>
        <b className="ml-auto shrink-0 rounded-full border border-state-waiting/60 px-3 py-2 text-[12px] font-semibold">Open</b>
      </span>
    </a>
  );
}

/** Disk per machine: this one, and the other hosts of the fleet that report it. */
function DiskRows() {
  const machine = useStore((s) => s.machine);
  const sample = useResources((s) => s.sample);
  const hosts = useFleetHosts((s) => s.hosts);
  useEffect(() => {
    void loadFleetHosts();
    const id = setInterval(() => void loadFleetHosts(), 15_000);
    return () => clearInterval(id);
  }, []);
  const m = sample?.machine;
  const rows: { name: string; text: string; warn: boolean }[] = [];
  if (m && m.disk_total > 0) rows.push({ name: machine || "This machine", text: `${bytes(m.disk_free)} free of ${bytes(m.disk_total)}`, warn: !!m.saturated });
  for (const h of hosts) {
    if (!h.reachable) rows.push({ name: h.name, text: "not reachable", warn: true });
    else if (h.disk_free_gb !== undefined) rows.push({ name: h.name, text: `${Math.round(h.disk_free_gb)} GB free`, warn: false });
  }
  if (rows.length === 0) return null;
  return (
    <section aria-label="Disk per machine" className="mt-4 rounded-[14px] border bg-card px-[14px]">
      {rows.map((r) => (
        <p key={r.name} className="flex min-h-11 items-center gap-2 border-b text-[13px] last:border-b-0">
          <span className="min-w-0 flex-1 truncate font-medium">{r.name}</span>
          <span className={cn("tabular shrink-0 text-[12px] text-muted-foreground", r.warn && amber)}>{r.text}</span>
        </p>
      ))}
    </section>
  );
}

export function PhoneHome() {
  const agents = useStore((s) => s.agents);
  const projects = useStore((s) => s.projects);
  const profiles = useStore((s) => s.profiles);
  const approvals = useStore((s) => s.approvals);
  const machine = useStore((s) => s.machine);
  const headId = useStore((s) => s.headId);
  const pending = pendingApprovals(approvals).length;
  useNow(30_000);
  const all = useMemo(() => Object.values(agents), [agents]);
  const running = all.filter((a) => a.state === "working" || a.state === "waiting" || a.state === "starting").length;
  const pick = useMemo(
    () => homeAgents(phoneAgentRows(all.filter((a) => classicAgentVisible(a, false)), projects, "", { machine, asks: askingAgents(Object.values(approvals)) }), headId),
    [all, projects, machine, approvals, headId],
  );
  const tiles = useMemo(() => phoneProjectTiles(projects, all).filter((t) => t.project.status === "active").slice(0, 3), [projects, all]);
  return (
    <div className="scroll-thin relative min-h-0 min-w-0 flex-1 overflow-y-auto" data-phone-home>
      <div className="px-[22px] pt-7 pb-6">
        <p className="flex items-center gap-2 text-[15px] font-semibold">
          <BrandMark />
          Tiamatrix
        </p>
        <h1 className="mt-[23px] text-[31px] leading-[1.12] font-semibold tracking-[-0.035em]">{greeting(new Date().getHours())}</h1>
        <p className="mt-2.5 mb-5 text-[13px] text-muted-foreground">{dailyLine(running, pending)}</p>

        <YourTurnCard />
        <div className="mt-4 empty:hidden">
          <HeadPausedBanner />
        </div>
        <DiskRows />

        {pick.length > 0 && (
          <>
            <SectionTitle title="Pick up a conversation" to={paths.agents()} more="All agents" />
            <ul aria-label="Pick up a conversation">
              {pick.map(({ agent, sub, state }) => {
                const label = agent.level === "head" ? headWord(true) : agentLabel(agent, profiles);
                return (
                  <li key={agent.id} className="border-b last:border-b-0">
                    <a href={href(paths.agent(agent.id))} className="flex min-h-[66px] items-center gap-[11px] py-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring">
                      <AgentAvatar agent={agent} label={label} className="size-[38px] rounded-[10px] text-[15px]" />
                      <span className="min-w-0 flex-1">
                        <span className="block truncate text-[14px] font-semibold">{label}</span>
                        {sub && <span className="mt-1 block truncate text-[12px] text-muted-foreground">{sub}</span>}
                      </span>
                      <span className={cn("flex shrink-0 items-center gap-1.5 text-[12px]", TONE[stateTone(agent)])}>
                        <StateDot state={agent.state} />
                        {state}
                      </span>
                    </a>
                  </li>
                );
              })}
            </ul>
          </>
        )}

        {tiles.length > 0 && (
          <>
            <SectionTitle title="Moving forward" to={paths.projects()} more="All projects" />
            <ul aria-label="Moving forward">
              {tiles.map((t) => (
                <li key={t.project.id} className="border-b last:border-b-0">
                  <a href={href(paths.project(t.project.id))} className="flex min-h-[66px] items-center gap-[11px] py-3 outline-none focus-visible:ring-2 focus-visible:ring-ring">
                    <span aria-hidden className="flex size-[38px] shrink-0 items-center justify-center rounded-[10px] bg-secondary text-[18px] font-semibold text-brand">
                      {(t.project.name.match(/[\p{L}\p{N}]/u)?.[0] ?? "P").toUpperCase()}
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-[14px] font-semibold">{t.project.name}</span>
                      <span className="mt-1 block truncate text-[12px] text-muted-foreground">{projectTileLine(t)}</span>
                    </span>
                    <ChevronRight className="size-[17px] shrink-0 text-muted-foreground" aria-hidden />
                  </a>
                </li>
              ))}
            </ul>
          </>
        )}

        <nav aria-label="More" className="mt-6">
          {PHONE_HOME_ROWS.map((r) => (
            <a
              key={r.path}
              href={href(r.path)}
              className="flex min-h-12 items-center gap-[9px] border-t text-[14px] outline-none focus-visible:ring-2 focus-visible:ring-ring"
              data-phone-home-row
            >
              <span className="min-w-0 flex-1 truncate">
                {r.label}
                <span className="ml-2 text-[12px] text-muted-foreground">{r.hint}</span>
              </span>
              <ChevronRight className="size-[17px] shrink-0 text-muted-foreground" aria-hidden />
            </a>
          ))}
        </nav>
      </div>
    </div>
  );
}
