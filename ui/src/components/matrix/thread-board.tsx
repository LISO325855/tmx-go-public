// The Board view of the Matrix (classic UI profile, its default screen; lib/matrix-board.ts;
// The owner's most repeated wish, 17 Nov: "prosthetics for my brain"; modelled on the Architect's
// stopgap board page). One row per thread, grouped Needs you, Blocked, In progress, Done today:
// the title and how far it is (a bar, 0-100), where it stands in one line, what stops it or what
// the owner has to do, with his Your-turn card right on the row, who owns it and when it last
// moved. A click on the title opens the thread's panel, as in the other views.
import { useState } from "react";
import { ChevronDown, ChevronUp, GripVertical } from "lucide-react";
import { toast } from "sonner";
import { ApprovalActions, ApprovalBody } from "@/components/approval";
import { relativeTime } from "@/lib/format";
import { boardApi } from "@/lib/board";
import { BOARD_GROUPS, OWNER_ACTS, UNDO_MS, canUndo, undoNote, boardOrder, boardSections, doneEarlier, movedOrder, ownerActSaid, progressText, type BoardGroup, type OwnerAct } from "@/lib/matrix-board";
import { matrixApi, type ThreadCard } from "@/lib/matrix";
import { href, paths } from "@/lib/router";
import { useStore } from "@/lib/store";
import type { Approval } from "@/lib/types";
import { cn } from "@/lib/utils";

/** each group's colour: the rule down a row's left edge and its bar */
const RULE: Record<BoardGroup, string> = {
  needs_you: "border-l-state-waiting",
  blocked: "border-l-state-error",
  in_progress: "border-l-brand",
  done_today: "border-l-border-strong",
};
const FILL: Record<BoardGroup, string> = {
  needs_you: "bg-state-waiting",
  blocked: "bg-state-error",
  in_progress: "bg-brand",
  done_today: "bg-border-strong",
};

export function ThreadBoard({
  cards,
  questions,
  open,
  onOpen,
  now,
  onChanged,
}: {
  cards: ThreadCard[];
  /** the owner's open questions by thread (lib/matrix-head.ts questionsByThread) */
  questions: Map<string, Approval[]>;
  open: string | null;
  onOpen: (id: string) => void;
  now: number;
  /** after an act or a move: load the threads again */
  onChanged?: () => void;
}) {
  const agents = useStore((s) => s.agents);
  const projects = useStore((s) => s.projects);
  const [busy, setBusy] = useState<string | null>(null);
  const [notes, setNotes] = useState<Record<string, string>>({});
  // the row being dragged and the row it is over (desktop); a phone uses the up / down handle
  const [drag, setDrag] = useState<string | null>(null);
  const [over, setOver] = useState<string | null>(null);
  // his order shown at once, until the daemon's answer comes back with the threads
  const [pending, setPending] = useState<string[] | null>(null);
  const ranked = pending ? cards.map((c) => (pending.includes(c.thread.id) ? { ...c, thread: { ...c.thread, position: pending.indexOf(c.thread.id) + 1 } } : c)) : cards;
  const sections = boardSections(ranked, (id) => questions.get(id)?.length ?? 0, now);

  // One tap, or a note: an event of the owner's on the thread, and mail to its agent.
  // Taking back a Park it or a Not relevant: the thread as it was, and the agent told again
  // (it already got the first mail).
  const undo = async (id: string, what: "park" | "not_relevant", before: string) => {
    try {
      if (what === "not_relevant") await boardApi.patchThread(id, { archived: false });
      else await matrixApi.setState(id, { status: before, why: "Undo: not parked after all." });
      await matrixApi.ownerAct(id, { act: "note", note: undoNote(what) });
      toast.success("Undone · the agent was told");
      onChanged?.();
    } catch (err) {
      toast.error((err as Error).message || "It could not be undone here; the list view can.");
    }
  };

  const act = async (id: string, what: OwnerAct, note?: string) => {
    const before = cards.find((c) => c.thread.id === id)?.thread.status || "active";
    setBusy(id);
    try {
      const res = await matrixApi.ownerAct(id, { act: what, note });
      // Park it and Not relevant change the thread: for a few seconds the toast can take them back
      const back = canUndo(what) ? { duration: UNDO_MS, action: { label: "Undo", onClick: () => void undo(id, what, before) } } : undefined;
      if (res.mail_error) toast.warning(`Saved on the thread, but the agent was not told: ${res.mail_error}`, back);
      else toast.success(ownerActSaid(what, res.mailed_to), back);
      if (note !== undefined) setNotes((n) => ({ ...n, [id]: "" }));
      onChanged?.();
    } catch (err) {
      toast.error((err as Error).message || "That did not work.");
    }
    setBusy(null);
  };

  // His order is the priority: saved as one list, the top first; agents work top-first.
  const move = async (groupIds: string[], id: string, to: "up" | "down" | string) => {
    const next = movedOrder(boardOrder(ranked, sections), groupIds, id, to);
    if (!next) return;
    setPending(next);
    try {
      await matrixApi.setOrder(next);
      onChanged?.();
    } catch (err) {
      setPending(null);
      toast.error((err as Error).message || "The order was not saved.");
    }
  };
  const earlier = doneEarlier(cards, now);
  const count = (g: BoardGroup) => sections.find((s) => s.group === g)?.rows.length ?? 0;

  return (
    <div className="scroll-thin min-h-0 flex-1 overflow-y-auto" data-thread-board>
      <div className="mx-auto w-full max-w-[61.25rem] px-4 py-5">
        <div className="mb-5 flex flex-wrap gap-2" data-board-totals>
          {BOARD_GROUPS.map((g) => (
            <span key={g.group} className="rounded-full border bg-card px-3.5 py-1 text-sm">
              {g.label} <b className="tabular font-semibold">{count(g.group)}</b>
            </span>
          ))}
        </div>

        {sections.length === 0 && <p className="py-6 text-sm text-muted-foreground">Nothing is open on the board.</p>}

        {sections.map((s) => (
          <section key={s.group} aria-label={s.label} data-board-group={s.group}>
            <h2 className="mt-6 mb-2.5 text-2xs font-semibold tracking-[0.08em] text-muted-foreground uppercase first:mt-0">{s.label}</h2>
            <div className="space-y-2">
              {s.rows.map((r, at) => {
                const t = r.card.thread;
                const groupIds = s.rows.map((x) => x.card.thread.id);
                const sortable = s.group !== "done_today";
                const working = busy === t.id;
                const note = notes[t.id] ?? "";
                const asks = questions.get(t.id) ?? [];
                const owner = t.owner ? agents[t.owner] : undefined;
                const project = t.project_id ? projects[t.project_id] : undefined;
                const blocker = (t.blocker ?? "").trim();
                const paused = (t.status || "active") === "paused";
                return (
                  <article
                    key={t.id}
                    data-board-row={t.id}
                    draggable={sortable}
                    onDragStart={(e) => {
                      if (!sortable) return;
                      e.dataTransfer.effectAllowed = "move";
                      e.dataTransfer.setData("text/plain", t.id);
                      setDrag(t.id);
                    }}
                    onDragEnd={() => {
                      setDrag(null);
                      setOver(null);
                    }}
                    onDragOver={(e) => {
                      if (!drag || drag === t.id || !groupIds.includes(drag)) return;
                      e.preventDefault();
                      e.dataTransfer.dropEffect = "move";
                      setOver(t.id);
                    }}
                    onDrop={(e) => {
                      if (!drag || !groupIds.includes(drag)) return;
                      e.preventDefault();
                      const moved = drag;
                      setDrag(null);
                      setOver(null);
                      void move(groupIds, moved, t.id);
                    }}
                    className={cn(
                      "rounded-[10px] border border-l-4 bg-card px-3.5 py-3",
                      RULE[s.group],
                      open === t.id && "ring-2 ring-ring/30",
                      drag === t.id && "opacity-50",
                      over === t.id && drag !== t.id && "ring-2 ring-brand/50",
                    )}
                  >
                    <div className="flex items-baseline justify-between gap-3">
                      {/* his order is the priority: drag the row (computer), or the handle up / down (phone too) */}
                      {sortable && (
                        <span className="flex shrink-0 items-center self-center text-muted-foreground" data-board-handle>
                          <GripVertical aria-hidden className="hidden size-4 cursor-grab md:block" />
                          <button type="button" disabled={at === 0} onClick={() => void move(groupIds, t.id, "up")} aria-label={`Move "${t.title}" up`} className="rounded p-1 outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-30">
                            <ChevronUp className="size-4" />
                          </button>
                          <button type="button" disabled={at === s.rows.length - 1} onClick={() => void move(groupIds, t.id, "down")} aria-label={`Move "${t.title}" down`} className="rounded p-1 outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-30">
                            <ChevronDown className="size-4" />
                          </button>
                        </span>
                      )}
                      <button type="button" onClick={() => onOpen(t.id)} className="min-w-0 flex-1 rounded text-left text-sm font-semibold outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring [overflow-wrap:anywhere]">
                        {t.title}
                      </button>
                      <span className="tabular shrink-0 text-sm whitespace-nowrap text-muted-foreground" data-board-pct>
                        {r.progress === null ? "–" : `${r.progress}%`}
                      </span>
                    </div>
                    <div className="my-2 h-2 overflow-hidden rounded-full bg-muted" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={r.progress ?? 0} aria-label={`${t.title}: progress`}>
                      <div className={cn("h-full rounded-full", FILL[s.group])} style={{ width: `${r.progress ?? 0}%` }} />
                    </div>
                    {/* where it stands, in the agent's one line; else what the bar means */}
                    <p title={r.progress === null ? "The agent has not reported progress yet." : undefined} className={cn("text-sm [overflow-wrap:anywhere]", !(t.progress_why ?? "").trim() && "text-muted-foreground")} data-board-now>
                      {(t.progress_why ?? "").trim() || progressText(r.progress, "", t.status)}
                    </p>
                    {/* what stops it, or what the owner has to do: the exact words, never hidden */}
                    {blocker && (
                      <p className="mt-0.5 text-sm font-semibold [overflow-wrap:anywhere]" data-board-need>
                        {s.group === "needs_you" ? "You: " : s.group === "blocked" ? "Blocked: " : ""}
                        {blocker}
                      </p>
                    )}
                    {/* his Your-turn card, on the row: the question and its answers */}
                    {asks.map((q) => (
                      <div key={q.id} className="mt-2.5 space-y-2.5 rounded-lg border border-state-waiting/50 bg-background px-3 py-2.5" data-board-ask={q.id}>
                        <ApprovalBody a={q} agent={agents[q.agent_id]} compact />
                        <ApprovalActions a={q} />
                      </div>
                    ))}
                    {/* one tap, or a quick note: each is an event of his on the thread and mail to its agent */}
                    <div className="mt-2.5 flex flex-wrap items-center gap-1.5" data-board-acts>
                      {OWNER_ACTS.filter((a) => !(a.act === "park" && paused) && !(s.group === "done_today" && a.act !== "thanks")).map((a) => (
                        <button
                          key={a.act}
                          type="button"
                          title={a.title}
                          disabled={working}
                          onClick={() => void act(t.id, a.act)}
                          data-board-act={a.act}
                          className="h-8 rounded-full border bg-background px-3 text-xs font-medium outline-none hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50"
                        >
                          {a.label}
                        </button>
                      ))}
                      <form
                        className="flex min-w-0 flex-1 basis-52 items-center gap-1.5"
                        onSubmit={(e) => {
                          e.preventDefault();
                          if (note.trim()) void act(t.id, "note", note.trim());
                        }}
                      >
                        <input
                          value={note}
                          onChange={(e) => setNotes((n) => ({ ...n, [t.id]: e.target.value }))}
                          placeholder="Note to the agent…"
                          aria-label={`Note on "${t.title}"`}
                          maxLength={400}
                          className="h-8 min-w-0 flex-1 rounded-lg border bg-background px-2.5 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
                          data-board-note
                        />
                        <button type="submit" title="Save this note on the thread and send it to the agent." disabled={working || !note.trim()} className="h-8 shrink-0 rounded-lg border bg-background px-3 text-xs font-medium outline-none hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-40">
                          Send note
                        </button>
                      </form>
                    </div>
                    <p className="mt-1.5 flex flex-wrap gap-x-1.5 text-xs text-muted-foreground" data-board-who>
                      {owner ? (
                        <a href={href(paths.agent(owner.id))} className="hover:text-foreground hover:underline">
                          {owner.name}
                        </a>
                      ) : (
                        <span>no owner</span>
                      )}
                      {project && (
                        <>
                          <span aria-hidden>·</span>
                          <a href={href(paths.project(project.id))} className="hover:text-foreground hover:underline">
                            {project.name}
                          </a>
                        </>
                      )}
                      {paused && (
                        <>
                          <span aria-hidden>·</span>
                          <span>paused</span>
                        </>
                      )}
                      <span aria-hidden>·</span>
                      <span>{r.moved > 0 ? `updated ${relativeTime(r.moved, now)}` : "no update yet"}</span>
                    </p>
                  </article>
                );
              })}
            </div>
          </section>
        ))}

        <p className="mt-8 text-xs text-muted-foreground">
          The order inside a group is your priority: agents work from the top. Percentages are the agents' own estimates, not measurements.
          {earlier > 0 && ` ${earlier} ${earlier === 1 ? "thread" : "threads"} done before today ${earlier === 1 ? "is" : "are"} in the list view.`}
        </p>
      </div>
    </div>
  );
}
