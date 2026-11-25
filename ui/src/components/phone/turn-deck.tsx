// Your turn on a phone in the classic profile, as in the Python dashboard's mobile app: ONE card
// per screen, moved with a swipe or "Previous · n / N · Next"; project chips on top; the choices
// are full-width thumb targets with the recommended one first (the order and the mark come from
// lib/card-choices.ts, the sizes are in index.css, "Your turn on that phone"). The cards
// themselves are the ones the computer shows (pages/inbox.tsx renders them).
import { useRef, useState, type ReactNode } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { AgentAvatar } from "@/components/agent-avatar";
import { PhoneHeadline } from "@/components/page";
import { StateDot } from "@/components/state";
import type { ProjectChip } from "@/lib/card-choices";
import { deckIndex, swipeStep } from "@/lib/phone";
import { href, paths } from "@/lib/router";
import type { Agent, Approval, Project } from "@/lib/types";

const step = "flex h-full min-w-[5.5rem] items-center justify-center gap-1 rounded-md px-3 text-sm font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-35";

export function TurnDeck({
  cards,
  total,
  chips,
  chip,
  onChip,
  later,
  agents,
  projects,
  initialId,
  renderCard,
  empty,
  children,
}: {
  /** what waits for the owner, by project, already narrowed to the chip in force */
  cards: Approval[];
  /** how many wait in all, whatever the chip */
  total: number;
  /** Python's project chips (lib/card-choices.ts): none when the cards are of one project */
  chips: ProjectChip[];
  chip: string | null;
  onChip: (key: string | null) => void;
  /** the cards saved with "Later" */
  later: Approval[];
  agents: Record<string, Agent>;
  projects: Record<string, Project>;
  initialId?: string | null;
  renderCard: (a: Approval, agent: Agent | undefined) => ReactNode;
  /** shown when nothing waits */
  empty?: ReactNode;
  /** notices above the card (interrupted jobs, answers still being sent) */
  children?: ReactNode;
}) {
  const [showLater, setShowLater] = useState(false);
  const [chosen, setChosen] = useState<string | null>(initialId ?? null);
  // where the deck stands, kept so that an answered card is followed by the one that took its place
  const [pos, setPos] = useState(0);
  const inLater = showLater && later.length > 0;
  const list = inLater ? later : cards;
  // A link to a card (Home's card, a notification) also arrives while Your turn is open: follow
  // it, out of the parked cards.
  const [linked, setLinked] = useState(initialId ?? null);
  if ((initialId ?? null) !== linked) {
    setLinked(initialId ?? null);
    if (initialId) {
      setShowLater(false);
      setChosen(initialId);
    }
  }
  const index = deckIndex(list.map((a) => a.id), chosen, pos);
  if (index >= 0 && index !== pos) setPos(index);
  const card = index >= 0 ? list[index] : undefined;

  const scroller = useRef<HTMLDivElement>(null);
  const start = useRef<{ x: number; y: number } | null>(null);
  const move = (delta: number) => {
    const next = list[index + delta];
    if (!next) return;
    setChosen(next.id);
    scroller.current?.scrollTo({ top: 0 });
  };

  const agent = card ? agents[card.agent_id] : undefined;
  const project = agent?.project_id ? projects[agent.project_id] : undefined;
  return (
    <div className="flex min-h-0 flex-1 flex-col" data-turn-deck>
      <div
        ref={scroller}
        className="scroll-thin relative min-h-0 flex-1 overflow-y-auto px-4 py-3"
        onTouchStart={(e) => {
          const t = e.touches[0];
          // a drag inside a code block, a text field or the chips scrolls or selects there
          start.current = e.touches.length === 1 && !(e.target as Element).closest("pre, textarea, input, [data-no-swipe]") ? { x: t.clientX, y: t.clientY } : null;
        }}
        onTouchEnd={(e) => {
          const s = start.current;
          start.current = null;
          const t = e.changedTouches[0];
          if (s && t) move(swipeStep(t.clientX - s.x, t.clientY - s.y));
        }}
      >
        <PhoneHeadline title="Your turn">
          {inLater ? `${later.length} saved for later` : total === 0 ? "Nothing waits for you." : `${total} ${total === 1 ? "waits" : "wait"} for you`}
        </PhoneHeadline>
        {!inLater && chips.length > 0 && (
          <div className="scroll-thin -mx-4 mb-3 flex gap-[7px] overflow-x-auto px-4 pb-1" role="group" aria-label="Projects" data-project-chips data-no-swipe>
            {chips.map((c) => {
              const on = c.key === chip;
              return (
                <button
                  key={c.key ?? "*"}
                  type="button"
                  aria-pressed={on}
                  onClick={() => {
                    onChip(c.key);
                    setChosen(null);
                    setPos(0);
                  }}
                  className={
                    "min-h-11 shrink-0 rounded-full border px-3.5 text-[12px] whitespace-nowrap outline-none focus-visible:ring-2 focus-visible:ring-ring " +
                    (on ? "border-primary bg-primary text-primary-foreground" : "bg-card")
                  }
                >
                  {c.label}
                </button>
              );
            })}
          </div>
        )}
        {children}
        {card && (
          <section aria-label={`Card ${index + 1} of ${list.length}`}>
            <div className="mb-2.5 flex min-h-11 items-center gap-2.5">
              {agent && <AgentAvatar agent={agent} className="size-8 rounded-lg text-[13px]" />}
              <a href={href(paths.agent(card.agent_id))} className="flex min-h-11 min-w-0 flex-1 flex-col justify-center">
                <span className="flex items-center gap-2">
                  <span className="truncate text-[14px] font-semibold">{agent?.name ?? card.agent_id}</span>
                  {agent && <StateDot state={agent.state} />}
                </span>
                {project && <span className="truncate text-[12px] text-muted-foreground">{project.name}</span>}
              </a>
              {inLater && <span className="shrink-0 rounded-full border px-2 py-0.5 text-[12px] text-muted-foreground">Saved for later</span>}
            </div>
            {renderCard(card, agent)}
          </section>
        )}
        {!card && empty}
        {later.length > 0 && (
          <button
            type="button"
            data-turn-later
            aria-pressed={inLater}
            onClick={() => {
              setShowLater((v) => !v);
              setChosen(null);
              setPos(0);
            }}
            className="mt-4 flex min-h-11 w-full items-center justify-center rounded-lg text-[13px] text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
          >
            {inLater ? (total > 0 ? `Back to what waits (${total})` : "Back") : `Saved for later (${later.length})`}
          </button>
        )}
      </div>
      {list.length > 1 && (
        <nav aria-label="Cards" className="flex h-12 shrink-0 items-center justify-between border-t bg-background px-2 py-1">
          <button type="button" className={step} disabled={index <= 0} onClick={() => move(-1)} aria-label="Previous card">
            <ChevronLeft className="size-4" />
            Previous
          </button>
          <span className="tabular text-[13px] text-muted-foreground" aria-hidden>
            {index + 1} / {list.length}
          </span>
          <button type="button" className={step} disabled={index >= list.length - 1} onClick={() => move(1)} aria-label="Next card">
            Next
            <ChevronRight className="size-4" />
          </button>
        </nav>
      )}
    </div>
  );
}
