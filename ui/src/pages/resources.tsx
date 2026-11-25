// Resources (#/resources, docs/RESOURCES.md): everything the agents can reach to act in the
// world - channels, tools, accounts, devices, storage - one line each: what it is, who holds
// it, whether it works. Opening a line shows what it is for, how agents use it and the rule.
// Read only. Never on this page: a password, a token, a code, a full number or address; a
// secret appears as its name and "stored" or "missing". Agents read the same list through
// the tools resources and resource.
import { useCallback, useEffect, useMemo, useState } from "react";
import { Boxes, ChevronRight, PlugZap, RefreshCw, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { EmptyState, PageBody, PageHeader, PageTitle, Section } from "@/components/page";
import { useNow } from "@/hooks/use-now";
import { relativeTime } from "@/lib/format";
import { href, navigate, paths } from "@/lib/router";
import { toolboxApi } from "@/lib/toolbox";
import {
  ACTION_LABEL,
  countResources,
  countsLine,
  filterResources,
  findResource,
  groupResources,
  holderFull,
  holderShort,
  identityText,
  routeText,
  secretText,
  statusOf,
  verifiedText,
  type Resource,
  type ResourceList,
  type StatusTone,
} from "@/lib/toolbox-format";
import { cn } from "@/lib/utils";

const TONE: Record<StatusTone, string> = {
  ok: "border-state-ok/40 bg-state-ok/10 text-state-ok",
  warn: "border-state-waiting/40 bg-state-waiting/10 text-foreground",
  down: "border-state-error/40 bg-state-error/10 text-state-error",
  idle: "border-border bg-muted/60 text-muted-foreground",
  unknown: "border-brand/30 bg-brand-soft text-brand",
};

function sentence(s: string): string {
  const t = s.trim();
  return /[.!?]$/.test(t) ? t : t + ".";
}

export function ResourcesPage({ id }: { id?: string }) {
  const [data, setData] = useState<ResourceList | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [query, setQuery] = useState("");
  const now = useNow(30_000);

  const load = useCallback(async () => {
    setBusy(true);
    try {
      setData(await toolboxApi.list());
      setError(null);
    } catch (e) {
      setError((e as Error).message || "The list could not be read");
    } finally {
      setBusy(false);
    }
  }, []);
  useEffect(() => void load(), [load]);

  const all = useMemo(() => data?.resources ?? [], [data]);
  const counts = useMemo(() => countResources(all), [all]);
  const groups = useMemo(() => groupResources(filterResources(all, query)), [all, query]);
  const byId = useMemo(() => new Map(all.map((r) => [r.id, r])), [all]);
  const open = findResource(all, id)?.id;
  const toggle = (rid: string) => navigate(paths.resources(open === rid ? undefined : rid), { replace: true });

  return (
    <>
      <PageHeader
        actions={
          <Button variant="ghost" size="sm" onClick={() => void load()} disabled={busy} aria-label="Check again">
            <RefreshCw className={cn(busy && "animate-spin")} />
            <span className="hidden sm:inline">Check again</span>
          </Button>
        }
      >
        <Boxes className="size-4 shrink-0 text-muted-foreground" />
        <PageTitle>Resources</PageTitle>
        {data && <span className="tabular truncate text-sm text-muted-foreground">{countsLine(counts)}</span>}
      </PageHeader>
      <PageBody>
        {!data ? (
          error ? (
            <EmptyState
              icon={PlugZap}
              title="Could not load the resources"
              actions={
                <Button variant="outline" size="sm" onClick={() => void load()}>
                  <RefreshCw />
                  Try again
                </Button>
              }
            >
              {sentence(error)}
            </EmptyState>
          ) : (
            <div className="space-y-3" aria-busy="true" aria-label="Loading the resources">
              <Skeleton className="h-4 w-96 max-w-full" />
              <Skeleton className="h-14 w-full" />
              <Skeleton className="h-14 w-full" />
              <Skeleton className="h-14 w-full" />
            </div>
          )
        ) : (
          <>
            <p className="mb-5 max-w-[46rem] text-sm text-muted-foreground">
              Everything the agents can reach to act in the world. Each line says who holds it and whether it works; open one for how agents use it and
              under which rule. Agents read this same list. Lines that are down or not verified come first.
            </p>
            <div className="relative mb-6">
              <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input className="pl-8" placeholder="Search resources, holders, rules" aria-label="Search resources" value={query} onChange={(e) => setQuery(e.target.value)} />
            </div>
            {all.length === 0 ? (
              <EmptyState icon={Boxes} title="No resources listed yet">Nothing has been written down for this machine yet.</EmptyState>
            ) : groups.length === 0 ? (
              <EmptyState icon={Boxes} title="No resources match">Try another search.</EmptyState>
            ) : (
              <div className="space-y-7">
                {groups.map((g) => (
                  <Section key={g.kind} title={g.label} right={<span className="tabular text-2xs text-muted-foreground">{g.resources.length}</span>}>
                    <ul className="divide-y rounded-xl border bg-card">
                      {g.resources.map((r) => (
                        <ResourceLine key={r.id} r={r} open={open === r.id} onToggle={() => toggle(r.id)} parent={r.parent ? byId.get(r.parent) : undefined} now={now} />
                      ))}
                    </ul>
                  </Section>
                ))}
              </div>
            )}
            <p className="mt-6 text-2xs text-subtle-foreground">
              As {data.machine} sees it, {relativeTime(data.updated_at, now)}. "Measured" lines are read from services this machine runs; the others were written
              down by an agent, with the date they were last checked.
            </p>
          </>
        )}
      </PageBody>
    </>
  );
}

function ResourceLine({ r, open, onToggle, parent, now }: { r: Resource; open: boolean; onToggle: () => void; parent?: Resource; now: number }) {
  const status = statusOf(r);
  const holder = holderShort(r);
  const panel = `resource-${r.id}`;
  return (
    <li data-resource={r.id}>
      <button
        type="button"
        aria-expanded={open}
        aria-controls={panel}
        onClick={onToggle}
        className="flex w-full items-start gap-2 px-4 py-3 text-left outline-none transition-colors duration-150 hover:bg-muted/40 focus-visible:ring-2 focus-visible:ring-ring"
      >
        <ChevronRight className={cn("mt-0.5 size-4 shrink-0 text-muted-foreground transition-transform duration-150", open && "rotate-90")} />
        <span className="min-w-0 flex-1">
          <span className="flex flex-wrap items-start gap-x-2 gap-y-1">
            <span className="min-w-0 flex-1 basis-48 text-sm font-medium break-words">{r.name}</span>
            <span className={cn("inline-flex h-5 shrink-0 items-center rounded-[5px] border px-1.5 text-2xs font-medium", TONE[status.tone])}>{status.word}</span>
          </span>
          <span className="mt-1 block text-sm break-words text-muted-foreground">
            {holder || "Nobody holds it yet"}
            {r.meter && <span className="tabular"> · {r.meter.text}</span>}
          </span>
          {r.health && r.health.level !== "good" && r.health.reason && (
            <span className={cn("mt-1 block text-sm break-words", r.health.level === "down" ? "text-state-error" : "text-foreground")}>{r.health.reason}</span>
          )}
        </span>
      </button>
      {open && (
        <div id={panel} className="space-y-3 border-t bg-muted/20 px-4 py-4 pl-10 text-sm">
          <p className="break-words">{r.summary}</p>
          <dl className="space-y-3">
            <Row label="State">
              {status.word}. {verifiedText(r)}
              {r.health && (
                <span className="block text-muted-foreground">
                  Checked {relativeTime(r.health.checked_at, now)}
                  {r.health.reason ? `: ${r.health.reason}` : "."}
                </span>
              )}
            </Row>
            {r.meter && (
              <Row label="What is left">
                {r.meter.label}: {r.meter.text}
              </Row>
            )}
            {r.how && <Row label="How agents use it">{r.how}</Row>}
            {r.actions.length > 0 && (
              <Row label="Actions">
                <ul className="space-y-1">
                  {r.actions.map((a) => (
                    <li key={a.name + a.type} className="break-words">
                      <span className="font-medium">{a.name}</span> <span className="text-muted-foreground">({ACTION_LABEL[a.type]})</span>: {routeText(a.route)}
                      {a.note && <span className="text-muted-foreground">. {a.note}</span>}
                    </li>
                  ))}
                </ul>
              </Row>
            )}
            <Row label="Rule">{r.rule.text}</Row>
            {identityText(r) && <Row label="Whose face it shows">{identityText(r)}</Row>}
            {(holderFull(r) || r.machine) && (
              <Row label="Who holds it">
                {[r.machine && `On ${r.machine}`, holderFull(r)].filter(Boolean).join(". ")}
              </Row>
            )}
            {r.maintainer && <Row label="Ask when broken">{r.maintainer}</Row>}
            {r.parent && (
              <Row label="Depends on">
                <a className="text-brand hover:underline" href={href(paths.resources(r.parent))}>
                  {parent?.name ?? r.parent}
                </a>
              </Row>
            )}
            {r.secrets && r.secrets.length > 0 && (
              <Row label="Secrets it needs">
                {r.secrets.map(secretText).join(" · ")}
                <span className="block text-muted-foreground">Names only. The values are never shown.</span>
              </Row>
            )}
          </dl>
          {r.page && (
            <p>
              <a className="text-brand hover:underline" href={href(r.page)}>
                Open its page
              </a>
            </p>
          )}
          <p className="tabular text-2xs break-all text-subtle-foreground">{r.id}</p>
        </div>
      )}
    </li>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="md:grid md:grid-cols-[10rem_1fr] md:gap-x-6">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
    </div>
  );
}
