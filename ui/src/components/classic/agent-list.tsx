// The sidebar's agent list the Python way (classic UI profile; parity C1, C2, C4; lib/classic-agents.ts):
// a search box above the list, two-line agent rows (face, name, the state in words; the role and
// what it is doing, a harness tag), and the line "N stopped agents hidden · show" under it.
// components/sidebar.tsx uses these only under classic; its own rows stay for the default dashboard.
import { Search, X } from "lucide-react";
import { AgentFace } from "@/components/classic/agent-face";
import { nowLine } from "@/components/sidebar";
import { StateDot } from "@/components/state";
import { agentSecondLine, saveShowStopped, stateWords, stoppedAgentCount, stoppedLine } from "@/lib/classic-agents";
import { harnessLabel } from "@/lib/harness";
import { href, paths } from "@/lib/router";
import { useStore } from "@/lib/store";
import type { Agent, AgentState } from "@/lib/types";
import { ui, useUI } from "@/lib/ui";
import { cn } from "@/lib/utils";

const WORDS: Partial<Record<AgentState, string>> = {
  working: "text-state-working",
  starting: "text-state-working",
  waiting: "font-medium text-[color-mix(in_oklch,var(--state-waiting)_75%,var(--foreground))]",
  paused: "text-[color-mix(in_oklch,var(--state-waiting)_75%,var(--foreground))]",
  error: "text-state-error",
};

/** "Search agents, projects, machines": filters the list as you type (Ctrl K still opens the search). */
export function ClassicSearchBox({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <div className="relative mt-2" data-agent-search>
      <Search className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-subtle-foreground" aria-hidden />
      <input
        type="search"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Escape" && value) {
            e.stopPropagation();
            onChange("");
          }
        }}
        placeholder="Search agents, projects, machines"
        aria-label="Search agents, projects, machines"
        autoComplete="off"
        spellCheck={false}
        className="h-7 w-full rounded-md border border-sidebar-border bg-background pr-7 pl-7 text-xs outline-none placeholder:text-subtle-foreground focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/25 [&::-webkit-search-cancel-button]:hidden"
      />
      {value && (
        <button
          type="button"
          aria-label="Clear the search"
          onClick={() => onChange("")}
          className="absolute top-1/2 right-1 flex size-5 -translate-y-1/2 items-center justify-center rounded text-subtle-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
        >
          <X className="size-3" />
        </button>
      )}
    </div>
  );
}

/** One agent, two lines: face, name, state in words; then the role and what it is doing, and its harness. */
export function ClassicAgentRow({ agent, depth, active, onNavigate }: { agent: Agent; depth: number; active: boolean; onNavigate?: () => void }) {
  const entries = useStore((s) => s.timelines[agent.id]?.entries);
  // "asks you" when the agent put a question, "needs you" for a permission or anything else
  const asks = useStore((s) => Object.values(s.approvals).some((a) => a.agent_id === agent.id && a.state === "pending" && !a.deferred && a.kind === "question"));
  const machine = useStore((s) => s.machine);
  const words = stateWords(agent, asks);
  const doing = agent.state === "working" || agent.state === "waiting" || agent.state === "error" ? nowLine(entries, agent) : "";
  return (
    <a
      href={href(paths.agent(agent.id))}
      onClick={onNavigate}
      aria-current={active ? "page" : undefined}
      title={[agent.name, words, machine].filter(Boolean).join(" · ")}
      data-agent-row={agent.id}
      className={cn(
        "flex items-start gap-2 rounded-md py-1 pr-2 text-sidebar-foreground/90 outline-none transition-colors duration-150 hover:bg-sidebar-accent/70 focus-visible:ring-2 focus-visible:ring-ring",
        active && "bg-sidebar-accent text-sidebar-accent-foreground",
        depth === 1 ? "pl-2" : "pl-4",
      )}
    >
      <AgentFace name={agent.name} size={22} className="mt-0.5" />
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1.5">
          <span className={cn("min-w-0 truncate text-[0.8125rem]", active && "font-medium")}>{agent.name}</span>
          <span className={cn("ml-auto inline-flex shrink-0 items-center gap-1 text-2xs text-muted-foreground", WORDS[agent.state])} data-agent-state>
            <StateDot state={agent.state} />
            {words}
          </span>
        </span>
        <span className="flex items-center gap-1.5 text-2xs text-subtle-foreground">
          <span className="min-w-0 flex-1 truncate">{agentSecondLine(agent, doing)}</span>
          {agent.harness !== "shell" && (
            <span className="shrink-0 rounded-[4px] border border-sidebar-border px-1 leading-4" title={harnessLabel(agent.harness)}>
              {agent.harness}
            </span>
          )}
        </span>
      </span>
    </a>
  );
}

/** Python's line under the list: "N stopped agents hidden · show"; the choice is remembered. */
export function StoppedAgentsLine() {
  const show = useUI((s) => s.showStoppedAgents);
  const count = useStore((s) => stoppedAgentCount(Object.values(s.agents)));
  const line = stoppedLine(count, show);
  if (!line) return null;
  return (
    <p className="mt-2 px-2 text-2xs text-subtle-foreground" data-stopped-agents>
      {line.text} ·{" "}
      <button
        type="button"
        className="underline-offset-2 outline-none hover:text-foreground hover:underline focus-visible:ring-2 focus-visible:ring-ring"
        onClick={() => {
          saveShowStopped(!show);
          ui.set({ showStoppedAgents: !show });
        }}
      >
        {line.action}
      </button>
    </p>
  );
}
