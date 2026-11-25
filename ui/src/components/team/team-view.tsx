// The Team page's body (#/team): for one group, four numbers, then one block per person and one
// line per computer: Node, Build, Last seen, Connections, Phone, E-mail, WhatsApp, Companion,
// Agents, Activity, Contract.
// Every state is an icon and a word (never colour alone), in large text. Pure: the page and the
// UI test hand it the data.
import { AlertTriangle, CheckCircle2, ChevronRight, CircleDashed, Laptop, Server, ShieldCheck, Users, XCircle } from "lucide-react";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";
import {
  buildCell,
  changeWords,
  contractCell,
  emailCell,
  figures,
  linksCell,
  phoneCell,
  scopeWords,
  seenCell,
  whatsappCell,
  activityCell,
  agentsCell,
  companionCell,
  localDay,
  type Cell,
} from "@/lib/team-format";
import type { TeamGroup, TeamNodeRow, TeamPage, TeamPerson, Tone } from "@/lib/team-types";

const TONE_ICON: Record<Tone, React.ComponentType<{ className?: string; "aria-hidden"?: boolean }>> = {
  ok: CheckCircle2,
  warn: AlertTriangle,
  bad: XCircle,
  none: CircleDashed,
};

const TONE_TEXT: Record<Tone, string> = {
  ok: "text-state-ok",
  warn: "text-[color-mix(in_oklch,var(--state-waiting)_72%,var(--foreground))]",
  bad: "text-state-error",
  none: "text-muted-foreground",
};

/** One cell: an icon for the tone, the word, and a smaller second line. */
export function StatusCell({ cell, className }: { cell: Cell; className?: string }) {
  const Icon = TONE_ICON[cell.tone];
  return (
    <div className={cn("flex min-w-0 items-start gap-2", className)} data-tone={cell.tone}>
      <Icon className={cn("mt-[0.1875rem] size-[1.0625rem] shrink-0", TONE_TEXT[cell.tone])} aria-hidden />
      <div className="min-w-0">
        <div className="text-[0.9375rem] leading-6 font-medium">{cell.label}</div>
        {cell.detail && <div className="text-[0.8125rem] leading-5 break-words text-muted-foreground">{cell.detail}</div>}
      </div>
    </div>
  );
}

const COLUMNS = ["Node", "Build", "Last seen", "Connections", "Phone", "E-mail", "WhatsApp", "Companion", "Agents", "Activity", "Contract"] as const;
const WIDTHS = ["13%", "9%", "8%", "10%", "8%", "9%", "9%", "8%", "9%", "9%", "8%"];

export function TeamView({
  page,
  group,
  now,
  onOpen,
  onPick,
}: {
  page: TeamPage;
  group: TeamGroup;
  now: number;
  /** the admin opens or closes the team to its members */
  onOpen?: (chatId: string, open: boolean) => void;
  /** pick another group (several) */
  onPick?: (chatId: string) => void;
}) {
  const names: Record<string, string> = {};
  for (const p of group.people) for (const n of p.nodes) names[n.peer_id] = p.name;
  return (
    <div className="space-y-7" data-testid="team-view">
      {page.groups.length > 1 && (
        <div className="flex flex-wrap gap-2" role="tablist" aria-label="Groups">
          {page.groups.map((g) => (
            <button
              key={g.chat_id}
              role="tab"
              aria-selected={g.chat_id === group.chat_id}
              onClick={() => onPick?.(g.chat_id)}
              className={cn(
                "rounded-full border px-3.5 py-1.5 text-sm transition-colors",
                g.chat_id === group.chat_id ? "border-foreground/30 bg-accent font-medium text-foreground" : "text-muted-foreground hover:bg-accent/60",
              )}
            >
              {g.name}
            </button>
          ))}
        </div>
      )}

      <section className="space-y-3" aria-label="Who sees this">
        <div>
          <h2 className="text-xl font-semibold tracking-tight text-balance">{group.name}</h2>
          <p className="mt-1 text-[0.9375rem] leading-6 text-muted-foreground">{scopeWords(group)}</p>
        </div>
        {group.admin && onOpen && (
          <label className="flex max-w-xl cursor-pointer items-start gap-3 rounded-xl border bg-card px-4 py-3">
            <Switch className="mt-1" checked={group.open} onCheckedChange={(v) => onOpen(group.chat_id, v)} aria-label="Everyone who shares sees the whole team" />
            <span className="min-w-0">
              <span className="block text-[0.9375rem] font-medium">Everyone who shares sees the whole team</span>
              <span className="block text-[0.8125rem] leading-5 text-muted-foreground">
                Off: each member sees only their own row; you see everyone.
              </span>
            </span>
          </label>
        )}
      </section>

      {group.scope === "all" && (
        <section className="grid grid-cols-2 gap-3 xl:grid-cols-4" aria-label="Summary">
          {figures(group).map((f) => {
            const Icon = TONE_ICON[f.tone];
            return (
              <div key={f.key} data-figure={f.key} className="min-w-0 rounded-xl border bg-card px-4 py-3.5">
                <div className="flex items-center gap-2">
                  <Icon className={cn("size-5 shrink-0", TONE_TEXT[f.tone])} aria-hidden />
                  <span className="tabular text-[1.75rem] leading-9 font-semibold tracking-tight">{f.value}</span>
                </div>
                <div className="mt-0.5 text-[0.9375rem] leading-6 font-medium">{f.label}</div>
                <div className="text-[0.8125rem] leading-5 text-muted-foreground">
                  {f.detail}
                </div>
              </div>
            );
          })}
        </section>
      )}

      <div className="space-y-5">
        {group.people.map((p) => (
          <PersonBlock key={p.name + (p.me ? "-me" : "")} person={p} floor={page.floor} now={now} names={names} admin={group.admin} />
        ))}
      </div>
    </div>
  );
}

function PersonBlock({ person, floor, now, names, admin }: { person: TeamPerson; floor: string; now: number; names: Record<string, string>; admin: boolean }) {
  const reported = person.nodes.some((n) => n.has_report);
  const verdict: Cell = !reported
    ? { tone: "none", label: "No report yet" }
    : person.passing
      ? { tone: "ok", label: "Passes" }
      : { tone: "bad", label: "Does not pass yet" };
  return (
    <section className="@container overflow-hidden rounded-xl border bg-card" aria-label={person.name} data-person={person.name}>
      <header className="flex flex-wrap items-center gap-x-3 gap-y-1.5 border-b px-4 py-3">
        <Users className="size-[1.125rem] shrink-0 text-muted-foreground" aria-hidden />
        <h3 className="min-w-0 truncate text-base font-semibold">{person.name}</h3>
        {person.me && <Tag>You</Tag>}
        {person.admin && <Tag>Admin</Tag>}
        <StatusCell cell={verdict} className="ml-auto" />
      </header>
      <table className="w-full border-collapse text-left @5xl:table-fixed">
        {/* the same widths in every block, so the columns line up from person to person */}
        <colgroup className="hidden @5xl:table-column-group">
          {WIDTHS.map((w, i) => (
            <col key={i} style={{ width: w }} />
          ))}
        </colgroup>
        <thead className="hidden border-b bg-muted/40 @5xl:table-header-group">
          <tr>
            {COLUMNS.map((c) => (
              <th key={c} scope="col" className="px-3 py-2 text-[0.8125rem] font-medium text-muted-foreground first:pl-4">
                {c}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {person.nodes.map((n) => (
            <NodeRows key={n.peer_id} n={n} floor={floor} now={now} names={names} admin={admin} />
          ))}
        </tbody>
      </table>
    </section>
  );
}

function Tag({ children }: { children: React.ReactNode }) {
  return <span className="rounded-md border px-1.5 py-0.5 text-[0.8125rem] leading-4 text-muted-foreground">{children}</span>;
}

function NodeRows({ n, floor, now, names, admin }: { n: TeamNodeRow; floor: string; now: number; names: Record<string, string>; admin: boolean }) {
  const r = n.report;
  const label = n.label || (n.has_report ? "Computer" : "A computer");
  const NodeIcon = r && !r.node.personal ? Server : Laptop;
  const cells: [string, React.ReactNode][] = [
    [
      "Node",
      <div key="node" className="flex min-w-0 items-start gap-2">
        <NodeIcon className="mt-[0.1875rem] size-[1.0625rem] shrink-0 text-muted-foreground" aria-hidden />
        <div className="min-w-0">
          <div className="text-[0.9375rem] leading-6 font-medium break-words">{label}</div>
          <div className="text-[0.8125rem] leading-5 text-muted-foreground">
            {r ? `${osName(r.node.os)}${r.node.personal ? "" : " server"}` : "not known"}
            {n.me ? " · this computer" : ""}
          </div>
        </div>
      </div>,
    ],
    ["Build", <StatusCell key="build" cell={buildCell(n, floor)} />],
    ["Last seen", <StatusCell key="seen" cell={seenCell(n, now)} />],
    ["Connections", <StatusCell key="links" cell={linksCell(n)} />],
    ["Phone", <StatusCell key="phone" cell={phoneCell(r)} />],
    ["E-mail", <StatusCell key="email" cell={emailCell(r)} />],
    ["WhatsApp", <StatusCell key="wa" cell={whatsappCell(r)} />],
    ["Companion", <StatusCell key="companion" cell={companionCell(r)} />],
    ["Agents", <StatusCell key="agents" cell={agentsCell(r)} />],
    ["Activity", <StatusCell key="activity" cell={activityCell(r, localDay(now))} />],
    ["Contract", <StatusCell key="contract" cell={contractCell(n)} />],
  ];
  const shown: [string, React.ReactNode][] = n.has_report
    ? cells
    : [
        cells[0],
        [
          "Report",
          <StatusCell key="none" cell={{ tone: "none", label: "No report yet", detail: "Not shared with you yet, or this computer runs an older tmx." }} />,
        ],
      ];
  return (
    <>
      <tr data-node={n.peer_id} className="grid grid-cols-2 gap-x-4 gap-y-3 border-b px-4 py-3.5 @xl:grid-cols-3 @3xl:grid-cols-4 @5xl:table-row @5xl:border-b-0 @5xl:p-0">
        {shown.map(([col, el], i) => (
          <td
            key={col}
            data-col={col}
            colSpan={col === "Report" ? COLUMNS.length - 1 : undefined}
            className={cn("min-w-0 align-top @5xl:table-cell @5xl:px-3 @5xl:py-3 @5xl:first:pl-4", (i === 0 || col === "Report") && "col-span-full")}
          >
            {col !== "Report" && <div className="mb-0.5 text-2xs font-medium tracking-wide text-subtle-foreground uppercase @5xl:hidden">{col}</div>}
            {el}
          </td>
        ))}
      </tr>
      <tr className="block border-b last:border-b-0 @5xl:table-row">
        <td colSpan={COLUMNS.length} className="block px-4 pb-3 @5xl:table-cell @5xl:pt-0">
          <Details n={n} names={names} admin={admin} now={now} />
        </td>
      </tr>
    </>
  );
}

function Details({ n, names, admin, now }: { n: TeamNodeRow; names: Record<string, string>; admin: boolean; now: number }) {
  const changes = n.changes ?? [];
  return (
    <details className="group/details">
      <summary className="inline-flex cursor-pointer list-none items-center gap-1 rounded-md py-1 text-[0.8125rem] text-muted-foreground hover:text-foreground [&::-webkit-details-marker]:hidden">
        <ChevronRight className="size-3.5 transition-transform group-open/details:rotate-90" aria-hidden />
        <ShieldCheck className="size-3.5" aria-hidden />
        The contract's ten checks{admin && changes.length > 0 ? ` and ${changes.length} recent change${changes.length === 1 ? "" : "s"}` : ""}
      </summary>
      <div className="mt-2 grid gap-4 md:grid-cols-2">
        <ul className="space-y-1.5" aria-label="Contract checks">
          {n.checks.map((c) => (
            <li key={c.id} data-check={c.id} className="flex items-start gap-2 text-[0.875rem] leading-5">
              {c.result === "pass" ? (
                <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-state-ok" aria-hidden />
              ) : c.result === "fail" ? (
                <XCircle className="mt-0.5 size-4 shrink-0 text-state-error" aria-hidden />
              ) : (
                <CircleDashed className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden />
              )}
              <span className="min-w-0">
                <span className="tabular font-medium">{c.id}</span>{" "}
                <span className={c.result === "not_checked" ? "text-muted-foreground" : ""}>
                  {c.result === "not_checked" ? "Not checked" : c.title}
                  {c.result === "pass" ? ": passes" : c.result === "fail" ? ": fails" : ""}
                </span>
                {c.detail && <span className="block text-[0.8125rem] text-muted-foreground">{c.detail}</span>}
              </span>
            </li>
          ))}
        </ul>
        {admin && changes.length > 0 && (
          <ul className="space-y-1.5" aria-label="Recent changes">
            {changes.map((c, i) => (
              <li key={i} className="text-[0.875rem] leading-5">
                <span className="tabular text-muted-foreground">{when(c.at, now)}</span> {changeWords(c.field, c.old, c.new, names)}
              </li>
            ))}
          </ul>
        )}
      </div>
    </details>
  );
}

function when(at: number, now: number): string {
  const d = new Date(at);
  const t = `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
  if (new Date(now).toDateString() === d.toDateString()) return t;
  return `${d.getDate()} ${d.toLocaleString("en", { month: "short" })} ${t}`;
}

function osName(os: string): string {
  switch (os) {
    case "darwin":
      return "macOS";
    case "windows":
      return "Windows";
    case "linux":
      return "Linux";
  }
  return os || "Unknown system";
}
