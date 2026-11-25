import { useEffect, useMemo, useRef, useState } from "react";
import { CheckCircle2, CirclePause, MessageCircleQuestion, ShieldAlert, Sparkles, SquareTerminal } from "lucide-react";
import { Kbd } from "@/components/ui/kbd";
import { JobQuarantineCards } from "@/components/job-quarantine";
import { OwnerCardRecovery } from "@/components/owner-card-recovery";
import { AgentAvatar } from "@/components/agent-avatar";
import { ApprovalActions, type ApprovalActionsHandle, ApprovalBody, approvalHeadline, GuardFootnote } from "@/components/approval";
import { guardOf } from "@/components/guard";
import { HandToCompanion } from "@/components/companion/approval";
import { EmptyState, PageBody, PageHeader, PageTitle } from "@/components/page";
import { TurnDeck } from "@/components/phone/turn-deck";
import { StateDot } from "@/components/state";
import { useClassic } from "@/hooks/use-classic";
import { usePhoneClassic } from "@/hooks/use-phone-classic";
import { isTyping } from "@/hooks/use-hotkeys";
import { useNow } from "@/hooks/use-now";
import { absoluteTime, relativeTime } from "@/lib/format";
import { PAUSE_LABEL } from "@/lib/resources";
import { harnessLabel, terminalMenuOf } from "@/lib/harness";
import { href, paths } from "@/lib/router";
import { pendingApprovals, useStore } from "@/lib/store";
import { cardAnswerKey, useCardSends, cardsWithSavedAnswers, useCardAnswers, useCardDrafts } from "@/lib/card-answers";
import { filterCardGroups, groupCardsByProject, projectChips } from "@/lib/card-choices";
import type { Agent, Approval } from "@/lib/types";
import { cn } from "@/lib/utils";

interface Group {
  agent: Agent | undefined;
  agentId: string;
  items: Approval[];
}

function ApprovalCard({
  a,
  agent,
  selected,
  onSelect,
  actionsRef,
  showAgent,
  roomy,
}: {
  a: Approval;
  agent?: Agent;
  selected: boolean;
  onSelect: () => void;
  actionsRef?: React.Ref<ApprovalActionsHandle>;
  /** classic: the cards sit under their project, so each one names its agent */
  showAgent?: boolean;
  /** the card has the screen to itself (a phone's Your turn): nothing is folded to a few lines */
  roomy?: boolean;
}) {
  const menu = terminalMenuOf(a);
  const Icon = menu
    ? SquareTerminal
    : a.kind === "question"
      ? MessageCircleQuestion
      : a.kind === "brake"
        ? CirclePause
        : a.kind === "companion"
          ? Sparkles
          : ShieldAlert;
  const title = menu
    ? harnessLabel(menu.harness)
    : a.kind === "companion"
      ? a.tool === "companion_task"
        ? "Hand to Companion"
        : a.tool === "companion_message"
          ? "Message to Companion"
          : "Companion asks"
      : a.kind === "question"
      ? "Question"
      : a.kind === "guard"
        ? (guardOf(a)?.rule_name ?? "Held by the guard")
        : a.kind === "brake"
          ? (PAUSE_LABEL[a.tool] ?? "Paused")
          : a.tool;
  return (
    <article
      data-approval={a.id}
      onMouseDown={onSelect}
      onFocusCapture={onSelect}
      className={cn(
        "rounded-xl border bg-card transition-[border-color,box-shadow] duration-150",
        selected ? "border-border-strong ring-2 ring-ring/30" : "hover:border-border-strong",
      )}
    >
      <header className="flex items-center gap-2 px-4 pt-3 text-xs">
        <Icon className="size-3.5 shrink-0 text-[color-mix(in_oklch,var(--state-waiting)_80%,var(--foreground))]" />
        <span className="font-medium text-foreground">{title}</span>
        <span className={cn("text-muted-foreground", a.kind === "guard" && "hidden sm:inline")}>{a.kind === "guard"
            ? "held by the guard"
            : a.kind === "companion"
              ? a.tool === "companion_task" || a.tool === "companion_message"
                ? "the agent asks you to send this"
                : "about a task tmx handed it"
              : approvalHeadline(a)}</span>
        <span className="tabular ml-auto flex shrink-0 items-center gap-2 text-2xs text-subtle-foreground">
          {showAgent && (
            <a href={href(paths.agent(a.agent_id))} className="max-w-[12rem] truncate text-muted-foreground hover:text-foreground hover:underline" data-card-agent>
              {agent?.name ?? a.agent_id}
            </a>
          )}
          <span title={absoluteTime(a.created_at)}>{relativeTime(a.created_at)}</span>
        </span>
      </header>
      <div className="space-y-3 px-4 pt-2.5 pb-3.5">
        <ApprovalBody a={a} agent={agent} compact={!roomy} />
        {/* data-card-actions: a phone sizes these buttons as thumb targets (index.css, "Your turn on that phone") */}
        <div data-card-actions>
          <ApprovalActions ref={actionsRef} a={a} showKeys={selected} />
        </div>
        {a.kind === "guard" && <GuardFootnote />}
        <HandToCompanion a={a} />
      </div>
    </article>
  );
}

export function InboxPage() {
  const approvals = useStore((s) => s.approvals);
  const scope = useStore((s) => s.transport?.baseURL || "");
  const sending = useCardSends((s) => s.items);
  const pendingAnswers = useCardAnswers((s) => s.items);
  const drafts = useCardDrafts((s) => s.items);
  const agents = useStore((s) => s.agents);
  const projects = useStore((s) => s.projects);
  const classic = useClassic();
  // Classic on a phone: Python's Your turn, one card per screen (components/phone/turn-deck.tsx).
  const phone = usePhoneClassic();
  useNow(20_000);
  const list = useMemo(() => {
    const merged = cardsWithSavedAnswers(approvals, scope, pendingAnswers, drafts);
    return pendingApprovals(merged).filter((a) => !sending[cardAnswerKey(scope, a.id)]);
  }, [approvals, pendingAnswers, drafts, scope, sending]);
  const sendingCount = Object.values(pendingAnswers).filter((p) => p.scope === scope && sending[cardAnswerKey(scope, p.card.id)]).length;
  const deferred = Object.values(approvals).filter((a) => a.state === "pending" && a.deferred);

  const groups = useMemo(() => {
    const m = new Map<string, Group>();
    for (const a of list) {
      const g = m.get(a.agent_id) ?? { agent: agents[a.agent_id], agentId: a.agent_id, items: [] };
      g.items.push(a);
      m.set(a.agent_id, g);
    }
    return [...m.values()];
  }, [list, agents]);
  // Classic (Python's "Your turn"): the cards by project, with chips to show one project.
  const byProject = useMemo(() => groupCardsByProject(list, agents, projects), [list, agents, projects]);
  const chips = useMemo(() => projectChips(byProject), [byProject]);
  const [chip, setChip] = useState<string | null>(null);
  const shownProjects = useMemo(() => filterCardGroups(byProject, chip), [byProject, chip]);
  // the chip in force: a chip whose project has no cards left falls back to all projects
  const activeChip = chip !== null && byProject.some((g) => g.key === chip) ? chip : null;
  const flat = useMemo(() => (classic ? shownProjects.flatMap((g) => g.items) : groups.flatMap((g) => g.items)), [classic, shownProjects, groups]);

  const initial = new URLSearchParams(window.location.hash.split("?")[1] ?? "").get("id");
  const [sel, setSel] = useState<string | null>(initial);
  // A link to a card that arrives while this page is open: no project chip may hide that card.
  const [linked, setLinked] = useState(initial);
  if (initial !== linked) {
    setLinked(initial);
    if (initial) setChip(null);
  }
  const selected = flat.find((a) => a.id === sel) ?? flat[0];
  const actions = useRef<ApprovalActionsHandle>(null);

  useEffect(() => {
    if (!selected) return;
    document.querySelector(`[data-approval="${selected.id}"]`)?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [selected?.id]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (isTyping(e.target) || e.metaKey || e.ctrlKey || e.altKey || document.querySelector("[role=dialog]")) return;
      if (!flat.length) return;
      const i = Math.max(0, flat.findIndex((a) => a.id === selected?.id));
      if (e.key === "j" || e.key === "ArrowDown") {
        e.preventDefault();
        setSel(flat[Math.min(flat.length - 1, i + 1)].id);
      } else if (e.key === "k" || e.key === "ArrowUp") {
        e.preventDefault();
        setSel(flat[Math.max(0, i - 1)].id);
      } else if (e.key === "a") {
        e.preventDefault();
        actions.current?.allow();
      } else if (e.key === "d") {
        e.preventDefault();
        actions.current?.deny();
      } else if (/^[1-9]$/.test(e.key)) {
        e.preventDefault();
        actions.current?.pick(Number(e.key) - 1);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [flat, selected?.id]);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageHeader>
        <OwnerCardRecovery />
        <PageTitle>{classic ? "Your turn" : "Needs you"}</PageTitle>
        {list.length > 0 && <span className="tabular text-xs text-muted-foreground">{list.length} pending</span>}
        {!classic && list.length > 1 && (
          <span className="ml-auto hidden items-center gap-1.5 text-2xs text-subtle-foreground md:flex">
            <Kbd>J</Kbd>
            <Kbd>K</Kbd> to move · <Kbd>A</Kbd> allow · <Kbd>D</Kbd> deny
          </span>
        )}
      </PageHeader>
      {phone ? (
        <TurnDeck
          cards={flat}
          total={list.length}
          chips={chips}
          chip={activeChip}
          onChip={setChip}
          later={deferred}
          agents={agents}
          projects={projects}
          initialId={initial}
          renderCard={(a, agent) => <ApprovalCard key={a.id} a={a} agent={agent} selected={false} onSelect={() => {}} roomy />}
          empty={
            <EmptyState icon={CheckCircle2} title="Nothing needs you" className="border-none bg-transparent">
              When an agent asks for permission or has a question for you, it shows up here.
            </EmptyState>
          }
        >
          <JobQuarantineCards />
          {sendingCount > 0 && <p role="status" className="mb-3 text-sm text-muted-foreground">Sending {sendingCount === 1 ? "your saved answer" : `${sendingCount} saved answers`}? Your words stay saved until delivery is confirmed.</p>}
        </TurnDeck>
      ) : (
      <PageBody width="max-w-[51.25rem]">
        <JobQuarantineCards />
        {sendingCount > 0 && <p role="status" className="mb-4 text-sm text-muted-foreground">Sending {sendingCount === 1 ? "your saved answer" : `${sendingCount} saved answers`}? Your words stay saved until delivery is confirmed.</p>}
        {list.length === 0 ? (
          <EmptyState icon={CheckCircle2} title="Nothing needs you" className="mt-6">
            When an agent asks for permission or has a question for you, it shows up here.
          </EmptyState>
        ) : classic ? (
          <>
            {chips.length > 0 && (
              <div className="mb-5 flex flex-wrap gap-1.5" role="group" aria-label="Projects" data-project-chips>
                {chips.map((c) => {
                  const on = c.key === activeChip;
                  return (
                    <button
                      key={c.key ?? "*"}
                      type="button"
                      aria-pressed={on}
                      onClick={() => setChip(c.key)}
                      className={cn(
                        "h-7 rounded-full border px-3 text-xs outline-none transition-colors duration-150 focus-visible:ring-2 focus-visible:ring-ring",
                        on ? "border-brand/50 bg-brand-soft font-medium text-foreground" : "text-muted-foreground hover:bg-muted/50 hover:text-foreground",
                      )}
                    >
                      {c.label}
                    </button>
                  );
                })}
              </div>
            )}
            <div className="space-y-8">
              {shownProjects.map((g) => (
                <section key={g.key || "no-project"} aria-label={g.name} data-card-project={g.key}>
                  <div className="mb-2.5 flex items-center gap-2.5">
                    {g.key ? (
                      <a href={href(paths.project(g.key))} className="text-[0.8125rem] font-medium hover:underline">
                        {g.name}
                      </a>
                    ) : (
                      <span className="text-[0.8125rem] font-medium">{g.name}</span>
                    )}
                    {g.items.length > 1 && <span className="tabular ml-auto text-2xs text-subtle-foreground">{g.items.length} open</span>}
                  </div>
                  <div className="space-y-2.5">
                    {g.items.map((a) => (
                      <ApprovalCard
                        key={a.id}
                        a={a}
                        agent={agents[a.agent_id]}
                        selected={a.id === selected?.id}
                        onSelect={() => setSel(a.id)}
                        actionsRef={a.id === selected?.id ? actions : undefined}
                        showAgent
                      />
                    ))}
                  </div>
                </section>
              ))}
            </div>
          </>
        ) : (
          <div className="space-y-8">
            {groups.map((g) => {
              const project = g.agent?.project_id ? projects[g.agent.project_id] : undefined;
              return (
                <section key={g.agentId} aria-label={g.agent?.name}>
                  <div className="mb-2.5 flex items-center gap-2.5">
                    {g.agent && <AgentAvatar agent={g.agent} className="size-5 text-[0.625rem]" />}
                    <a href={href(paths.agent(g.agentId))} className="text-[0.8125rem] font-medium hover:underline">
                      {g.agent?.name ?? g.agentId}
                    </a>
                    {g.agent && <StateDot state={g.agent.state} />}
                    {project && (
                      <a href={href(paths.project(project.id))} className="text-xs text-muted-foreground hover:text-foreground">
                        {project.name}
                      </a>
                    )}
                    {g.items.length > 1 && <span className="tabular ml-auto text-2xs text-subtle-foreground">{g.items.length} pending</span>}
                  </div>
                  <div className="space-y-2.5">
                    {g.items.map((a) => (
                      <ApprovalCard
                        key={a.id}
                        a={a}
                        agent={g.agent}
                        selected={a.id === selected?.id}
                        onSelect={() => setSel(a.id)}
                        actionsRef={a.id === selected?.id ? actions : undefined}
                      />
                    ))}
                  </div>
                </section>
              );
            })}
          </div>
        )}
        {deferred.length > 0 && <section className="mt-8 space-y-2" aria-label="Saved for later">
          <h2 className="text-sm font-medium">Saved for later</h2>
          {deferred.map((a) => <ApprovalCard key={a.id} a={a} agent={agents[a.agent_id]} selected={false} onSelect={() => {}} />)}
        </section>}
      </PageBody>
      )}
    </div>
  );
}
