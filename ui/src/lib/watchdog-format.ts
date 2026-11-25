// Pure logic for the Watchdog page (pages/watchdog.tsx): one machine's resource history
// (GET /api/resources/history, internal/reshistory) turned into one series per resource, the
// geometry of its line, and the sentences around it. No runtime project imports, so it stays
// unit-testable without a daemon (test/watchdog.test.mjs). A history may come from another
// machine: it is numbers and one date string, rendered as plain text only.

export interface HistoryPoint {
  t: number;
  cpu: number;
  mem_free: number;
  /** absent = this build could not measure it in that bucket */
  write?: number;
  write_max?: number;
  agent_write: number;
  disk_queue?: number;
}

export interface DiskPoint {
  t: number;
  write: number;
  write_max: number;
  /** absent = the system does not report operation counts */
  write_ops?: number;
  busy?: number;
}

export interface DiskSeries {
  name: string;
  points: DiskPoint[] | null;
}

export interface HistoryView {
  /** each physical disk's own series; empty where the system reports none */
  disks?: DiskSeries[] | null;
  period: string;
  /** seconds per point */
  step: number;
  from: number;
  to: number;
  points: HistoryPoint[] | null;
  day: string;
  disk_written: number;
  disk_measured: boolean;
  agents_written: number;
  mem_total: number;
  cores: number;
}

export const PERIODS = ["1h", "6h", "24h", "7d"] as const;
export type Period = (typeof PERIODS)[number];

export type Resource = "cpu" | "memory" | "disk" | "agents" | "queue" | "busy" | "ops";

/** The resources drawn once per physical disk when the machine reports its disks. */
export const PER_DISK: readonly Resource[] = ["disk", "busy", "ops"];

/** Each resource is its own chart with its own scale: never two measures on one axis. */
export const RESOURCES: { key: Resource; label: string; title: string }[] = [
  { key: "disk", label: "Disk", title: "Disk writes, every program" },
  { key: "agents", label: "Agents' writes", title: "Disk writes, the agents" },
  { key: "cpu", label: "CPU", title: "CPU, all cores" },
  { key: "memory", label: "Memory", title: "Memory in use" },
  { key: "queue", label: "Disk queue", title: "Disk queue, busiest disk" },
  { key: "busy", label: "Disk busy", title: "Time the disk was busy" },
  { key: "ops", label: "Write operations", title: "Write operations" },
];

export interface Sample {
  t: number;
  v: number;
}

function finite(n: unknown): n is number {
  return typeof n === "number" && Number.isFinite(n);
}

/** One resource's readings in time order. A point without that reading is left out (a gap),
 * never drawn as zero; so is any value that is not a finite, non-negative number. */
export function series(view: Pick<HistoryView, "points" | "mem_total"> | null | undefined, r: Resource): Sample[] {
  const out: Sample[] = [];
  for (const p of view?.points ?? []) {
    let v: number | undefined;
    switch (r) {
      case "cpu":
        v = p.cpu;
        break;
      case "memory":
        v = finite(view?.mem_total) && view.mem_total > 0 && finite(p.mem_free) ? Math.max(view.mem_total - p.mem_free, 0) : undefined;
        break;
      case "disk":
        v = p.write;
        break;
      case "agents":
        v = p.agent_write;
        break;
      case "queue":
        v = p.disk_queue;
        break;
    }
    if (finite(p.t) && finite(v) && v >= 0) out.push({ t: p.t, v });
  }
  return out.sort((a, b) => a.t - b.t);
}

/** One disk's readings of a per-disk resource, in time order; as series(), a missing reading
 * is a gap. Busy is a percentage: a value above 100 is not one. */
export function diskSamples(d: DiskSeries, r: Resource): Sample[] {
  const out: Sample[] = [];
  for (const p of d.points ?? []) {
    const v = r === "disk" ? p.write : r === "busy" ? p.busy : r === "ops" ? p.write_ops : undefined;
    if (finite(p.t) && finite(v) && v >= 0 && (r !== "busy" || v <= 100)) out.push({ t: p.t, v });
  }
  return out.sort((a, b) => a.t - b.t);
}

/** "Disk 0" for Windows' PhysicalDrive0; any other system's own name as it is. */
export function diskLabel(name: string): string {
  const m = /^PhysicalDrive(\d+)$/.exec(name);
  return m ? `Disk ${m[1]}` : name;
}

/** The charts for one resource: one per physical disk for the per-disk resources when the
 * machine reports its disks, otherwise the machine's single series. Busy and write operations
 * exist per disk only: without disks there is nothing to draw, and the list is empty. */
export function charts(view: HistoryView, r: Resource): { key: string; title: string; samples: Sample[] }[] {
  const meta = RESOURCES.find((x) => x.key === r) ?? RESOURCES[0];
  const disks = (view.disks ?? []).filter((d) => d && typeof d.name === "string" && d.name !== "");
  if (PER_DISK.includes(r)) {
    if (disks.length > 0) return disks.map((d) => ({ key: d.name, title: `${meta.title} · ${diskLabel(d.name)}`, samples: diskSamples(d, r) }));
    if (r !== "disk") return [];
  }
  return [{ key: r, title: meta.title, samples: series(view, r) }];
}

/** Runs of readings with no gap between them. Two readings further apart than two and a half
 * steps (and at least two and a half minutes: after a restart the last hour mixes minute and
 * 10-second readings) are not joined by a line: a gap means no reading. */
export function segments(samples: Sample[], stepSeconds: number): Sample[][] {
  const gap = Math.max(stepSeconds * 2500, 150_000);
  const out: Sample[][] = [];
  for (const s of samples) {
    const run = out[out.length - 1];
    if (run && s.t - run[run.length - 1].t <= gap) run.push(s);
    else out.push([s]);
  }
  return out;
}

/** The top of the y axis: 100 for a percentage, otherwise a round number above the highest
 * reading (1, 2 or 5 times a power of ten), so the axis label is readable. */
export function axisTop(r: Resource, samples: Sample[]): number {
  if (r === "cpu" || r === "busy") return 100;
  const top = samples.reduce((m, s) => Math.max(m, s.v), 0);
  if (top <= 0) return 1;
  const pow = 10 ** Math.floor(Math.log10(top));
  for (const k of [1, 2, 5, 10]) if (k * pow >= top) return k * pow;
  return 10 * pow;
}

export interface Frame {
  width: number;
  height: number;
  from: number;
  to: number;
  top: number;
}

export function xOf(f: Frame, t: number): number {
  const span = Math.max(f.to - f.from, 1);
  return (Math.min(Math.max(t - f.from, 0), span) / span) * f.width;
}

export function yOf(f: Frame, v: number): number {
  return f.height - (Math.min(Math.max(v, 0), f.top) / f.top) * f.height;
}

/** The SVG path of one run. A run of a single reading has no length: it is drawn as a dot by
 * the caller, so the path is empty. */
export function linePath(f: Frame, run: Sample[]): string {
  if (run.length < 2) return "";
  return run.map((s, i) => `${i ? "L" : "M"}${xOf(f, s.t).toFixed(1)},${yOf(f, s.v).toFixed(1)}`).join(" ");
}

/** The reading nearest to a time (the crosshair); null with no readings. */
export function nearest(samples: Sample[], t: number): Sample | null {
  let best: Sample | null = null;
  for (const s of samples) if (!best || Math.abs(s.t - t) < Math.abs(best.t - t)) best = s;
  return best;
}

function unit(n: number, units: string[], base: number): string {
  let i = 0;
  let x = n;
  while (x >= base && i < units.length - 1) {
    x /= base;
    i++;
  }
  return `${x >= 100 || i === 0 ? Math.round(x) : x.toFixed(1)} ${units[i]}`;
}

/** A reading in the resource's own unit: "12%", "1.4 MB/s", "20.0 GB", "0.42". */
export function formatValue(r: Resource, v: number): string {
  switch (r) {
    case "cpu":
    case "busy":
      return v > 0 && v < 1 ? "<1%" : `${Math.round(v)}%`;
    case "ops":
      return `${v >= 100 ? Math.round(v) : v.toFixed(1)} writes/s`;
    case "memory":
      return unit(v, ["B", "KB", "MB", "GB", "TB"], 1024);
    case "queue":
      return v.toFixed(2);
    default:
      return `${unit(v, ["B", "KB", "MB", "GB"], 1024)}/s`;
  }
}

/** "Maximum 1.4 MB/s · Latest 0 B/s" under the chart, or that there is nothing to show. */
export function summary(r: Resource, samples: Sample[]): string {
  if (samples.length === 0) return "No readings for this period.";
  const top = samples.reduce((m, s) => Math.max(m, s.v), 0);
  return `Maximum ${formatValue(r, top)} · Latest ${formatValue(r, samples[samples.length - 1].v)} · Gaps mean no reading.`;
}

/** "10-second samples" / "1-minute samples" / "15-minute samples". */
export function stepLabel(stepSeconds: number): string {
  if (stepSeconds < 60) return `${stepSeconds}-second samples`;
  return `${Math.round(stepSeconds / 60)}-minute samples`;
}

/** The line above the charts: what was written today. `budgetGB` is the machine's daily write
 * budget for the agents (0 = none). Unmeasured is said, never shown as 0. */
export function todayLine(v: Pick<HistoryView, "disk_written" | "disk_measured" | "agents_written">, budgetGB: number): string {
  const gbs = (n: number) => `${(Math.max(n, 0) / 2 ** 30).toFixed(1)} GB`;
  const disk = v.disk_measured ? `Whole disk today: ${gbs(v.disk_written)} (all programs, while tmx ran)` : "Whole disk today: not measured on this machine";
  const agents = budgetGB > 0 ? `Agents: ${gbs(v.agents_written)} / ${budgetGB.toFixed(1)} GB budget` : `Agents: ${gbs(v.agents_written)}`;
  return `${disk}. ${agents}.`;
}

/** Where a machine's history is read from: this machine's own route, or the owner's route to
 * another machine. A name that could not be one path segment gets no address. */
export function historyPath(machine: string | null, period: Period): string | null {
  const q = `?period=${period}`;
  if (machine === null) return `/api/resources/history${q}`;
  if (!/^[a-z0-9][a-z0-9-]{0,39}$/.test(machine)) return null;
  return `/api/machines/${machine}/history${q}`;
}

export function isPeriod(p: string | undefined): p is Period {
  return (PERIODS as readonly string[]).includes(p ?? "");
}

// ---- Hardware (GET /api/machine/health, internal/machinehealth): temperatures, drive health,
// the GPU. A field a device does not report is absent, and is said so, never shown as 0.

export interface DiskHealth {
  name: string;
  health?: string;
  status?: string;
  wear_pct?: number;
  temp_c?: number;
  power_on_hours?: number;
  read_errors?: number;
  write_errors?: number;
  read_uncorrected?: number;
  write_uncorrected?: number;
}

export interface GpuReading {
  name: string;
  temp_c?: number;
  used_gb?: number;
  total_gb?: number;
  util_pct?: number;
  power_w?: number;
}

export interface HealthView {
  at: number;
  /** 0 = no usable temperature reading; temps_note says why */
  temps_at: number;
  temps_note?: string;
  cpu_temp_c?: number;
  disk_temp_c?: number;
  disks: DiskHealth[] | null;
  gpu?: GpuReading | null;
}

/** Where a machine's hardware readings are read from (as historyPath). */
export function healthPath(machine: string | null): string | null {
  if (machine === null) return "/api/machine/health";
  if (!/^[a-z0-9][a-z0-9-]{0,39}$/.test(machine)) return null;
  return `/api/machines/${machine}/health`;
}

/** "27.9 °C", or that there is no reading. */
export function celsius(c: number | undefined | null): string {
  return finite(c) ? `${c.toFixed(1)} °C` : "no reading";
}

/** "Healthy · 37.0 °C · Wear 31 %": only what the drive reports. */
export function diskLine(d: DiskHealth): string {
  const bits = [d.health || "Health not reported"];
  if (d.status && d.status !== "OK") bits.push(d.status);
  if (finite(d.temp_c)) bits.push(celsius(d.temp_c));
  if (finite(d.wear_pct)) bits.push(`Wear ${d.wear_pct} %`);
  return bits.join(" · ");
}

/** "Read errors 0 · Write errors not reported · Uncorrected read 0 / write 0 · 24858 hours". */
export function diskDetail(d: DiskHealth): string {
  const n = (v: number | undefined) => (finite(v) ? String(v) : "not reported");
  const bits = [`Read errors ${n(d.read_errors)}`, `Write errors ${n(d.write_errors)}`, `Uncorrected read ${n(d.read_uncorrected)} / write ${n(d.write_uncorrected)}`];
  bits.push(finite(d.power_on_hours) ? `${d.power_on_hours} hours` : "Hours not reported");
  return bits.join(" · ");
}

/** A drive that is not plainly healthy, or reports an uncorrected error: worth the owner's eye. */
export function diskNeedsAttention(d: DiskHealth): boolean {
  if (d.health && d.health !== "Healthy") return true;
  if (d.status && d.status !== "OK") return true;
  return (d.read_uncorrected ?? 0) > 0 || (d.write_uncorrected ?? 0) > 0;
}

/** The GPU's four tiles; a value the card does not report reads "no reading". */
export function gpuStats(g: GpuReading): { label: string; value: string }[] {
  const mem = finite(g.used_gb) && finite(g.total_gb) ? `${g.used_gb.toFixed(1)} GB / ${g.total_gb.toFixed(1)} GB` : "no reading";
  return [
    { label: "Temperature", value: finite(g.temp_c) ? `${Math.round(g.temp_c)} °C` : "no reading" },
    { label: "Memory used", value: mem },
    { label: "Activity", value: finite(g.util_pct) ? `${Math.round(g.util_pct)} %` : "no reading" },
    { label: "Power", value: finite(g.power_w) ? `${Math.round(g.power_w)} W` : "no reading" },
  ];
}

// ---- Pauses: what tmx's guards paused, and what they would have paused in observe mode
// (GET /api/resources "protect.events", internal/resources/protect.go).

export interface GuardEvent {
  at: number;
  kind: string;
  agent?: string;
  volume?: string;
  detail: string;
}

export interface PauseRow {
  at: number;
  agent: string;
  detail: string;
}

/** The guard events of the last day that say "this agent would have been paused, but the
 * guard only observes": newest first, one row per agent and volume (its latest). */
export function wouldPause(events: GuardEvent[] | null | undefined, now: number): PauseRow[] {
  const seen = new Set<string>();
  const out: PauseRow[] = [];
  const day = 24 * 3600_000;
  // Whatever a daemon sends: only well-formed events are looked at, and only then sorted.
  const usable = (events ?? []).filter((e) => e && typeof e.kind === "string" && e.kind.includes("would-") && finite(e.at) && now - e.at <= day);
  for (const e of usable.sort((a, b) => b.at - a.at)) {
    const key = `${e.agent ?? ""}\u0000${e.volume ?? ""}`;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push({ at: e.at, agent: e.agent || "An agent", detail: e.detail || "" });
  }
  return out;
}
