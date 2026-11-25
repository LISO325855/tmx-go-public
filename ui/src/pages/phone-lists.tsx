import { ProjectShells } from "@/components/classic/project-shells";
// The two lists behind the phone's bottom bar (components/phone/bottom-bar.tsx), as in the Python
// dashboard's mobile app: Agents is one flat, searchable list with the state in words; Projects
// is one tile per project. On a phone in the classic profile they open on their headline, with
// nothing fixed above; anywhere else they have the usual title row (#/agents, #/projects).
// The agent rule and the words are the classic sidebar's (lib/classic-agents.ts).
import { useMemo, useState } from "react";
import { Bot, ChevronRight, FolderPlus, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { AgentAvatar } from "@/components/agent-avatar";
import { ProjectStatusBadge } from "@/components/badges";
import { EmptyState, PageBody, PageHeader, PageTitle, PhoneHeadline } from "@/components/page";
import { StateDot } from "@/components/state";
import { useClassic } from "@/hooks/use-classic";
import { usePhoneClassic } from "@/hooks/use-phone-classic";
import { classicAgentVisible, saveShowStopped, stoppedAgentCount, stoppedLine } from "@/lib/classic-agents";
import { plural } from "@/lib/format";
import { askingAgents, phoneAgentRows, phoneProjectTiles, projectTileLine, stateTone } from "@/lib/phone";
import { agentLabel } from "@/lib/profiles";
import { href, paths } from "@/lib/router";
import { useStore } from "@/lib/store";
import { headWord } from "@/lib/ui-profile";
import { ui, useUI } from "@/lib/ui";
import { cn } from "@/lib/utils";

const TONE = {
  you: "text-[color-mix(in_oklch,var(--state-waiting)_75%,var(--foreground))]",
  moving: "text-state-working",
  fault: "text-state-error",
  quiet: "text-muted-foreground",
} as const;

export function AgentsPage() {
  const agents = useStore((s) => s.agents);
  const projects = useStore((s) => s.projects);
  const profiles = useStore((s) => s.profiles);
  const approvals = useStore((s) => s.approvals);
  const machine = useStore((s) => s.machine);
  const showStopped = useUI((s) => s.showStoppedAgents);
  const phone = usePhoneClassic();
  const [query, setQuery] = useState("");
  const all = useMemo(() => Object.values(agents), [agents]);
  // Python's list: every agent except the stopped ones (one line under the list shows them).
  const rows = useMemo(
    () => phoneAgentRows(all.filter((a) => classicAgentVisible(a, showStopped)), projects, query, { machine, asks: askingAgents(Object.values(approvals)) }),
    [all, projects, query, machine, approvals, showStopped],
  );
  const stopped = stoppedLine(stoppedAgentCount(all), showStopped);
  const searching = query.trim() !== "";
  const count = plural(rows.length, "agent");
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageHeader>
        <PageTitle>Agents</PageTitle>
        <span className="tabular truncate text-xs text-muted-foreground">{count}</span>
      </PageHeader>
      <PageBody width="max-w-[45rem]" className="py-3 md:py-6">
        {phone && <PhoneHeadline title="Agents">{count}</PhoneHeadline>}
        <input
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search agents, projects, machines"
          aria-label="Search agents, projects, machines"
          autoComplete="off"
          spellCheck={false}
          className="block min-h-11 w-full rounded-xl border bg-card px-3.5 text-[16px] outline-none placeholder:text-[14px] placeholder:text-muted-foreground focus-visible:ring-2 focus-visible:ring-ring"
        />
        {rows.length === 0 ? (
          <EmptyState icon={Bot} title={searching ? "No agent matches" : "No agents here"} className="mt-4 border-none bg-transparent">
            {searching ? "Try a name, a project or a state such as “working”." : `The ${headWord(true)} starts agents as projects need them.`}
          </EmptyState>
        ) : (
          <ul className="mt-1.5" aria-label="Agents">
            {rows.map(({ agent, sub, state }) => {
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
                      {!agent.shelved_at && <StateDot state={agent.state} />}
                      {state}
                    </span>
                    <ChevronRight className="size-[17px] shrink-0 text-muted-foreground" aria-hidden />
                  </a>
                </li>
              );
            })}
          </ul>
        )}
        {stopped && (
          <button
            type="button"
            aria-pressed={showStopped}
            data-stopped-agents
            onClick={() => {
              saveShowStopped(!showStopped);
              ui.set({ showStoppedAgents: !showStopped });
            }}
            className="mt-2 flex min-h-11 w-full items-center justify-center text-[12px] text-muted-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            {stopped.text} · {stopped.action}
          </button>
        )}
        {/* Python's "+ Agent" (the project is a field of that form), as in the classic sidebar */}
        <Button variant="outline" className="mt-3 h-11 w-full" onClick={() => ui.openNewAgent(true)}>
          <Plus />
          Agent
        </Button>
      </PageBody>
    </div>
  );
}

export function ProjectsPage() {
  const projects = useStore((s) => s.projects);
  const agents = useStore((s) => s.agents);
  const phone = usePhoneClassic();
  const classic = useClassic();
  const tiles = useMemo(() => phoneProjectTiles(projects, Object.values(agents)), [projects, agents]);
  const count = plural(tiles.length, "project");
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageHeader>
        <PageTitle>Projects</PageTitle>
        <span className="tabular truncate text-xs text-muted-foreground">{count}</span>
      </PageHeader>
      <PageBody width="max-w-[45rem]" className="py-3 md:py-6">
        {phone && <PhoneHeadline title="Projects">{count}</PhoneHeadline>}
        <div className="mb-4"><ProjectShells /></div>
        {tiles.length === 0 ? (
          <EmptyState icon={FolderPlus} title="No projects yet" className="border-none bg-transparent">
            The {headWord(true)} sets up projects and leads for you. Tell it what to build, or create a project yourself.
          </EmptyState>
        ) : (
          <ul className="space-y-3.5" aria-label="Projects">
            {tiles.map((t) => (
              <li key={t.project.id}>
                <a
                  href={href(paths.project(t.project.id))}
                  className={cn(
                    "block rounded-[18px] border bg-card p-5 outline-none transition-colors duration-150 hover:border-border-strong focus-visible:ring-2 focus-visible:ring-ring",
                    t.project.status !== "active" && "bg-transparent",
                  )}
                >
                  <span className="flex items-center gap-3">
                    <span aria-hidden className="flex size-[50px] shrink-0 items-center justify-center rounded-[13px] bg-secondary text-[26px] font-semibold text-brand">
                      {(t.project.name.match(/[\p{L}\p{N}]/u)?.[0] ?? "P").toUpperCase()}
                    </span>
                    {t.project.status !== "active" && <ProjectStatusBadge status={t.project.status} className="ml-auto" />}
                  </span>
                  <span className="mt-4 block text-[23px] leading-[1.2] font-semibold tracking-[-0.03em] [overflow-wrap:anywhere]">{t.project.name}</span>
                  <span className="mt-2 line-clamp-3 block text-[12px] leading-normal whitespace-pre-line text-muted-foreground">
                    {t.project.brief || "Open the project to see its agents."}
                  </span>
                  <span className="mt-[15px] flex items-center gap-1.5 text-[12px] text-muted-foreground">
                    {t.needsYou > 0 && <StateDot state="waiting" />}
                    {t.needsYou === 0 && t.working > 0 && <StateDot state="working" />}
                    {projectTileLine(t)}
                  </span>
                </a>
              </li>
            ))}
          </ul>
        )}
        {/* classic: a project is made from "+ Agent" (Agents), as on the computer */}
        {!classic && (
          <Button variant="outline" className="mt-4 h-11 w-full" onClick={() => ui.openNewProject(true)}>
            <Plus />
            New project
          </Button>
        )}
      </PageBody>
    </div>
  );
}
