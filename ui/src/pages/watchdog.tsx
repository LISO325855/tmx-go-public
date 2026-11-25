// The Watchdog page (#/watchdog, #/watchdog/<machine>): a machine's resource history, with the
// machine, the period and the resource to choose - this machine's from its own daemon, another
// machine's through it (GET /api/machines/{name}/history, the owner only). One resource is one
// chart with its own scale; a gap in the line means no reading. Below it, for this machine: what
// is written now against the limits, the heavy-command slots and today's top writers; and for
// either machine its hardware: temperatures, each drive's health, the GPU.
import { useEffect, useMemo, useRef, useState } from "react";
import { Activity } from "lucide-react";
import { EmptyState, PageBody, PageHeader, PageTitle, Section } from "@/components/page";
import { Button } from "@/components/ui/button";
import { useFleetHosts, watchFleetHosts } from "@/lib/fleet-hosts";
import { bytes, pauseLabel, useResources } from "@/lib/resources";
import { href, navigate, paths } from "@/lib/router";
import { agentName, useStore } from "@/lib/store";
import {
  PERIODS,
  RESOURCES,
  axisTop,
  charts,
  celsius,
  diskDetail,
  diskLine,
  diskNeedsAttention,
  formatValue,
  gpuStats,
  healthPath,
  historyPath,
  linePath,
  nearest,
  segments,
  stepLabel,
  summary,
  todayLine,
  wouldPause,
  xOf,
  yOf,
  type Frame,
  type GuardEvent,
  type HealthView,
  type HistoryView,
  type Period,
  type Resource,
  type Sample,
} from "@/lib/watchdog-format";

const POLL_MS = 30_000;
const W = 600;
const H = 160;

function useHistory(machine: string | null, period: Period) {
  const [view, setView] = useState<HistoryView | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let live = true;
    setView(null);
    setError("");
    const path = historyPath(machine, period);
    async function load() {
      const t = useStore.getState().transport;
      if (!t || !path) return setError(path ? "Not connected yet." : "That machine's name cannot be addressed.");
      try {
        const v = await t.request<HistoryView>("GET", path);
        if (live) {
          setView(v);
          setError("");
        }
      } catch (e) {
        // Keep the last history on a failed refresh; say why the page is not fresh.
        if (live) setError(e instanceof Error ? e.message : String(e));
      }
    }
    void load();
    const id = setInterval(() => void load(), POLL_MS);
    return () => {
      live = false;
      clearInterval(id);
    };
  }, [machine, period]);
  return { view, error };
}

export function WatchdogPage({ machine }: { machine?: string }) {
  const self = useFleetHosts((s) => s.self);
  const hosts = useFleetHosts((s) => s.hosts);
  const local = useStore((s) => s.machine);
  const [period, setPeriod] = useState<Period>("1h");
  const [resource, setResource] = useState<Resource>("disk");
  useEffect(() => watchFleetHosts(), []);

  const selfName = self?.name || local || "This machine";
  const others = hosts.filter((h) => h.kind === "peer");
  const remote = machine && machine !== self?.name ? machine : null;
  const { view, error } = useHistory(remote, period);
  const budget = useResources((s) => s.config?.daily_write_gb ?? 0);

  return (
    <>
      <PageHeader>
        <PageTitle>Watchdog</PageTitle>
        <span className="truncate text-sm text-muted-foreground">{remote ?? selfName}</span>
      </PageHeader>
      <PageBody>
        <div className="space-y-7">
          <Section title="Resource history">
            <div className="rounded-xl border bg-card p-4">
              <div className="flex flex-wrap gap-x-6 gap-y-3">
                {others.length > 0 && (
                  <Choice label="Machine">
                    <Pick on={remote === null} onClick={() => navigate(paths.watchdog())}>
                      {selfName}
                    </Pick>
                    {others.map((h) => (
                      <Pick key={h.name} on={remote === h.name} onClick={() => navigate(paths.watchdog(h.name))}>
                        {h.name}
                      </Pick>
                    ))}
                  </Choice>
                )}
                <Choice label="Period">
                  {PERIODS.map((p) => (
                    <Pick key={p} on={period === p} onClick={() => setPeriod(p)}>
                      {p}
                    </Pick>
                  ))}
                </Choice>
                <Choice label="Resource">
                  {RESOURCES.map((r) => (
                    <Pick key={r.key} on={resource === r.key} onClick={() => setResource(r.key)}>
                      {r.label}
                    </Pick>
                  ))}
                </Choice>
              </div>
              {error && <p className="mt-3 text-xs text-state-error">{view ? `Not refreshed: ${error}` : error}</p>}
              {view ? (
                <>
                  <p className="mt-4 text-sm">{todayLine(view, remote === null ? budget : 0)}</p>
                  <p className="mt-1 text-2xs text-subtle-foreground">{stepLabel(view.step)} · Readings are averages; longer periods are aggregated.</p>
                  {charts(view, resource).length === 0 ? (
                    <p className="mt-5 text-sm text-muted-foreground">This machine reports no per-disk readings for this.</p>
                  ) : (
                    charts(view, resource).map((c) => <HistoryChart key={c.key} view={view} resource={resource} title={c.title} samples={c.samples} />)
                  )}
                </>
              ) : (
                !error && <p className="mt-4 text-sm text-muted-foreground">Reading the history…</p>
              )}
            </div>
          </Section>
          {remote === null ? (
            <NowCard />
          ) : (
            <p className="text-sm text-muted-foreground">
              Limits, slots and top writers are shown for this machine only for now.{" "}
              <a className="underline underline-offset-2" href={href(paths.machine(remote))}>
                Open {remote}'s agents
              </a>
              .
            </p>
          )}
          <HardwareCard machine={remote} />
        </div>
      </PageBody>
    </>
  );
}

function Choice({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div role="group" aria-label={label}>
      <p className="mb-1 text-2xs text-subtle-foreground">{label}</p>
      <div className="flex flex-wrap gap-1">{children}</div>
    </div>
  );
}

function Pick({ on, onClick, children }: { on: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <Button size="xs" variant={on ? "secondary" : "ghost"} aria-pressed={on} onClick={onClick}>
      {children}
    </Button>
  );
}

function clock(ms: number, withDate: boolean): string {
  const d = new Date(ms);
  const time = d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  return withDate ? `${d.toLocaleDateString(undefined, { month: "short", day: "numeric" })}, ${time}` : time;
}

/** One resource as one line. The title names the series, so there is no legend; the line wears
 * the app's brand colour and every label its text colours. Hovering shows the nearest reading. */
function HistoryChart({ view, resource, title, samples }: { view: HistoryView; resource: Resource; title: string; samples: Sample[] }) {
  const meta = { title };
  const runs = useMemo(() => segments(samples, view.step), [samples, view.step]);
  const frame: Frame = { width: W, height: H, from: view.from, to: view.to, top: axisTop(resource, samples) };
  const box = useRef<HTMLDivElement>(null);
  const [hover, setHover] = useState<Sample | null>(null);
  const long = view.to - view.from > 24 * 3600_000;

  function move(e: React.PointerEvent) {
    const r = box.current?.getBoundingClientRect();
    if (!r || r.width === 0) return;
    const frac = Math.min(Math.max((e.clientX - r.left) / r.width, 0), 1);
    setHover(nearest(samples, view.from + frac * (view.to - view.from)));
  }

  const left = (s: Sample) => `${(xOf(frame, s.t) / W) * 100}%`;
  const topOf = (s: Sample) => `${(yOf(frame, s.v) / H) * 100}%`;

  return (
    <figure className="mt-5">
      <figcaption className="mb-2 flex items-baseline gap-2">
        <span className="text-[0.8125rem] font-medium">{meta.title}</span>
        <span className="tabular ml-auto text-2xs text-subtle-foreground">top of scale {formatValue(resource, frame.top)}</span>
      </figcaption>
      <div
        ref={box}
        className="relative h-40 w-full touch-none"
        onPointerMove={move}
        onPointerLeave={() => setHover(null)}
        role="img"
        aria-label={`${meta.title}, ${view.period}. ${summary(resource, samples)}`}
      >
        <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className="absolute inset-0 size-full overflow-visible text-brand" aria-hidden>
          {[0.25, 0.5, 0.75].map((g) => (
            <line key={g} x1={0} x2={W} y1={H * g} y2={H * g} className="stroke-border" strokeWidth={1} vectorEffect="non-scaling-stroke" />
          ))}
          <line x1={0} x2={W} y1={H} y2={H} className="stroke-border-strong" strokeWidth={1} vectorEffect="non-scaling-stroke" />
          {runs.map((run) => (
            <path key={run[0].t} d={linePath(frame, run)} fill="none" stroke="currentColor" strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" vectorEffect="non-scaling-stroke" />
          ))}
          {hover && <line x1={xOf(frame, hover.t)} x2={xOf(frame, hover.t)} y1={0} y2={H} className="stroke-muted-foreground" strokeWidth={1} vectorEffect="non-scaling-stroke" />}
        </svg>
        {/* A reading with no neighbour is a dot: an SVG circle would stretch with the plot. */}
        {runs
          .filter((run) => run.length === 1)
          .map((run) => (
            <span key={run[0].t} className="absolute size-1.5 -translate-x-1/2 -translate-y-1/2 rounded-full bg-brand" style={{ left: left(run[0]), top: topOf(run[0]) }} />
          ))}
        {hover && (
          <>
            <span className="absolute size-2.5 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-card bg-brand" style={{ left: left(hover), top: topOf(hover) }} />
            <div
              className="tabular pointer-events-none absolute top-0 z-10 rounded-md border bg-popover px-2 py-1 text-2xs whitespace-nowrap text-popover-foreground shadow-sm"
              style={xOf(frame, hover.t) > W / 2 ? { right: `${100 - (xOf(frame, hover.t) / W) * 100}%`, marginRight: 8 } : { left: left(hover), marginLeft: 8 }}
            >
              <span className="font-medium">{formatValue(resource, hover.v)}</span>
              <span className="text-muted-foreground"> · {clock(hover.t, long)}</span>
            </div>
          </>
        )}
      </div>
      <div className="tabular mt-1.5 flex justify-between text-2xs text-subtle-foreground">
        <span>{clock(view.from, true)}</span>
        <span>{clock(view.to, true)}</span>
      </div>
      <p className="mt-2 text-xs text-muted-foreground">{summary(resource, samples)}</p>
      {samples.length > 0 && (
        <details className="mt-2 text-xs">
          <summary className="cursor-pointer text-muted-foreground">The last readings as a table</summary>
          <table className="tabular mt-2 w-full max-w-sm text-left">
            <thead className="text-subtle-foreground">
              <tr>
                <th className="py-0.5 pr-4 font-normal">Time</th>
                <th className="py-0.5 font-normal">{meta.title}</th>
              </tr>
            </thead>
            <tbody>
              {samples
                .slice(-30)
                .reverse()
                .map((s) => (
                  <tr key={s.t}>
                    <td className="py-0.5 pr-4">{clock(s.t, long)}</td>
                    <td className="py-0.5">{formatValue(resource, s.v)}</td>
                  </tr>
                ))}
            </tbody>
          </table>
        </details>
      )}
    </figure>
  );
}

function Stat({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <p className="text-2xs text-subtle-foreground">{label}</p>
      <p className="tabular mt-0.5 text-[0.9375rem] font-medium">{children}</p>
    </div>
  );
}

/** The guards' recent events (GET /api/resources "protect.events"), re-read every half minute
 * while the page is open; an older daemon without them gives none. */
function useGuardEvents(): GuardEvent[] {
  const [events, setEvents] = useState<GuardEvent[]>([]);
  useEffect(() => {
    let live = true;
    async function load() {
      const t = useStore.getState().transport;
      if (!t) return;
      try {
        const v = await t.request<{ protect?: { events?: GuardEvent[] | null } }>("GET", "/api/resources");
        if (live) setEvents(v.protect?.events ?? []);
      } catch {
        // keep the last list: this card is not the place to report a failed refresh twice
      }
    }
    void load();
    const id = setInterval(() => void load(), POLL_MS);
    return () => {
      live = false;
      clearInterval(id);
    };
  }, []);
  return events;
}

/** This machine now: writes against the limits, heavy-command slots, today's top writers, and
 * what the guards paused or, observing only, would have paused. */
function NowCard() {
  const sample = useResources((s) => s.sample);
  const config = useResources((s) => s.config);
  const slots = useResources((s) => s.slots);
  const agents = useStore((s) => s.agents);
  const events = useGuardEvents();
  const m = sample?.machine;
  if (!m || !config) {
    return (
      <EmptyState icon={Activity} title="No reading yet">
        Resource control has not measured this machine yet.
      </EmptyState>
    );
  }
  const mb = (n: number) => `${(Math.max(n, 0) / 2 ** 20).toFixed(1)} MB/s`;
  const gbs = (n: number) => `${(Math.max(n, 0) / 2 ** 30).toFixed(1)} GB`;
  const heavy = slots?.kinds.find((k) => k.kind === "heavy");
  const writers = [...(sample?.agents ?? [])].filter((a) => a.written_today > 0).sort((a, b) => b.written_today - a.written_today).slice(0, 6);
  const waiting = (slots?.leases ?? []).filter((l) => l.state === "waiting");
  const running = (slots?.leases ?? []).filter((l) => l.state === "held");
  const paused = Object.values(agents).filter((a) => a.state === "paused");
  const observed = wouldPause(events, Date.now());
  return (
    <Section title="This machine now">
      <div className="space-y-6 rounded-xl border bg-card p-4">
        <div className="grid grid-cols-2 gap-x-6 gap-y-4 md:grid-cols-4">
          <Stat label="Agents' disk writes now">
            {mb(m.agent_write_rate)}
            {config.disk_agent_mb_s > 0 && <span className="text-muted-foreground"> / {config.disk_agent_mb_s.toFixed(1)} MB/s limit</span>}
          </Stat>
          <Stat label="Agents written today">
            {gbs(m.written_today)}
            {config.daily_write_gb > 0 && <span className="text-muted-foreground"> / {config.daily_write_gb.toFixed(1)} GB budget</span>}
          </Stat>
          <Stat label="Disk writes now, every program">{m.disk_write_rate >= 0 ? mb(m.disk_write_rate) : "not measured"}</Stat>
          <Stat label="Disk queue">{m.disk_queue >= 0 ? m.disk_queue.toFixed(2) : "not measured"}</Stat>
          <Stat label="Heavy slots">{heavy ? `${heavy.held} / ${heavy.size} in use` : "none configured"}</Stat>
          <Stat label="Waiting commands">{heavy ? heavy.waiting : 0}</Stat>
          <Stat label="Disk free">
            {bytes(m.disk_free)} <span className="text-muted-foreground">of {bytes(m.disk_total)}</span>
          </Stat>
          <Stat label="Agents paused by tmx">{m.paused}</Stat>
        </div>
        <div className="grid gap-6 md:grid-cols-2">
          <div>
            <h3 className="mb-2 text-2xs font-medium tracking-wide text-subtle-foreground uppercase">Top writers today</h3>
            {writers.length === 0 ? (
              <p className="text-sm text-muted-foreground">No agent has written yet today.</p>
            ) : (
              <ul className="divide-y">
                {writers.map((a) => (
                  <li key={a.agent_id} className="py-1.5">
                    <a className="text-sm font-medium hover:underline" href={href(paths.agent(a.agent_id))}>
                      {agentName(agents, a.agent_id)}
                    </a>
                    <p className="tabular text-xs text-muted-foreground">
                      {gbs(a.written_today)}
                      {a.budget > 0 && ` / ${gbs(a.budget)} budget`}
                    </p>
                  </li>
                ))}
              </ul>
            )}
          </div>
          <div>
            <h3 className="mb-2 text-2xs font-medium tracking-wide text-subtle-foreground uppercase">Heavy-command queue</h3>
            <p className="text-sm text-muted-foreground">
              {running.length === 0 ? "No heavy commands running." : `${running.length} running: ${running.map((l) => l.holder).join(", ")}.`}
            </p>
            <p className="mt-1 text-sm text-muted-foreground">
              {waiting.length === 0 ? "No heavy commands waiting." : `${waiting.length} waiting: ${waiting.map((l) => l.holder).join(", ")}.`}
            </p>
          </div>
          <div>
            <h3 className="mb-2 text-2xs font-medium tracking-wide text-subtle-foreground uppercase">Paused by tmx</h3>
            {paused.length === 0 ? (
              <p className="text-sm text-muted-foreground">No agent is paused.</p>
            ) : (
              <ul className="divide-y">
                {paused.map((a) => (
                  <li key={a.id} className="py-1.5 text-sm">
                    <a className="font-medium hover:underline" href={href(paths.agent(a.id))}>
                      {a.name}
                    </a>
                    <span className="text-muted-foreground"> · {pauseLabel(a)}</span>
                  </li>
                ))}
              </ul>
            )}
          </div>
          <div>
            <h3 className="mb-2 text-2xs font-medium tracking-wide text-subtle-foreground uppercase">Would pause · observe only</h3>
            {observed.length === 0 ? (
              <p className="text-sm text-muted-foreground">Nothing in the last day: no guard that only observes would have paused an agent.</p>
            ) : (
              <ul className="divide-y">
                {observed.map((e) => (
                  <li key={`${e.at}:${e.agent}`} className="py-1.5">
                    <p className="text-sm font-medium break-words">{e.agent}</p>
                    <p className="text-xs break-words text-muted-foreground">
                      {clock(e.at, true)}
                      {e.detail && ` · ${e.detail}`}
                    </p>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      </div>
    </Section>
  );
}

const HEALTH_POLL_MS = 60_000;

/** A machine's hardware: CPU and disk temperature, each drive's health, the GPU. What a device
 * does not report is said, never shown as zero. */
function HardwareCard({ machine }: { machine: string | null }) {
  const [view, setView] = useState<HealthView | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let live = true;
    setView(null);
    setError("");
    const path = healthPath(machine);
    async function load() {
      const t = useStore.getState().transport;
      if (!t || !path) return;
      try {
        const v = await t.request<HealthView>("GET", path);
        if (live) {
          setView(v);
          setError("");
        }
      } catch (e) {
        if (live) setError(e instanceof Error ? e.message : String(e));
      }
    }
    void load();
    const id = setInterval(() => void load(), HEALTH_POLL_MS);
    return () => {
      live = false;
      clearInterval(id);
    };
  }, [machine]);

  if (!view) {
    return (
      <Section title="Hardware">
        <p className="text-sm text-muted-foreground">{error || "Reading the hardware…"}</p>
      </Section>
    );
  }
  const disks = view.disks ?? [];
  return (
    <Section title="Hardware" right={view.temps_at > 0 ? <span className="tabular text-2xs text-subtle-foreground">temperatures read {clock(view.temps_at, false)}</span> : undefined}>
      <div className="space-y-6 rounded-xl border bg-card p-4">
        {error && <p className="text-xs text-state-error">Not refreshed: {error}</p>}
        <div className="grid grid-cols-2 gap-x-6 gap-y-4 md:grid-cols-4">
          <Stat label="CPU temperature">{celsius(view.cpu_temp_c)}</Stat>
          <Stat label="Disk temperature">{celsius(view.disk_temp_c)}</Stat>
        </div>
        {view.temps_note && <p className="text-sm text-muted-foreground">{view.temps_note}</p>}
        {view.temps_at > 0 && (
          <div>
            <h3 className="mb-2 text-2xs font-medium tracking-wide text-subtle-foreground uppercase">Disk health</h3>
            {disks.length === 0 ? (
              <p className="text-sm text-muted-foreground">No drive reported its health.</p>
            ) : (
              <ul className="divide-y">
                {disks.map((d, i) => (
                  <li key={i} className="py-2">
                    <p className="text-sm font-medium break-words">{d.name}</p>
                    <p className="text-sm">
                      {diskNeedsAttention(d) && <span className="mr-1.5 rounded-sm bg-state-error/15 px-1 text-2xs font-medium text-state-error">Check this drive</span>}
                      {diskLine(d)}
                    </p>
                    <p className="tabular text-xs break-words text-muted-foreground">{diskDetail(d)}</p>
                  </li>
                ))}
              </ul>
            )}
          </div>
        )}
        <div>
          <h3 className="mb-2 text-2xs font-medium tracking-wide text-subtle-foreground uppercase">{view.gpu ? `GPU · ${view.gpu.name}` : "GPU"}</h3>
          {view.gpu ? (
            <div className="grid grid-cols-2 gap-x-6 gap-y-4 md:grid-cols-4">
              {gpuStats(view.gpu).map((s) => (
                <Stat key={s.label} label={s.label}>
                  {s.value}
                </Stat>
              ))}
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">No NVIDIA card was found on this machine (nvidia-smi).</p>
          )}
        </div>
      </div>
    </Section>
  );
}
