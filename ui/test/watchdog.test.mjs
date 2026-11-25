// The Watchdog page (src/lib/watchdog-format.ts): a machine's resource history as series,
// line geometry and the sentences around the chart.
import test from "node:test";
import assert from "node:assert/strict";
import {
  axisTop,
  formatValue,
  historyPath,
  isPeriod,
  linePath,
  nearest,
  segments,
  series,
  stepLabel,
  summary,
  todayLine,
  xOf,
  yOf,
} from "../src/lib/watchdog-format.ts";

const GB = 2 ** 30;
const MB = 2 ** 20;
const view = {
  mem_total: 64 * GB,
  points: [
    { t: 30_000, cpu: 30, mem_free: 20 * GB, write: 2 * MB, agent_write: MB, disk_queue: 0.5 },
    { t: 10_000, cpu: 10, mem_free: 44 * GB, agent_write: 0 }, // disk writes and queue not measured
    { t: 20_000, cpu: 0, mem_free: 70 * GB, write: 0, agent_write: 0, disk_queue: 0 },
  ],
};

test("series: one resource in time order; an unmeasured reading is a gap, never zero", () => {
  assert.deepEqual(series(view, "cpu"), [{ t: 10_000, v: 10 }, { t: 20_000, v: 0 }, { t: 30_000, v: 30 }]);
  assert.deepEqual(series(view, "disk"), [{ t: 20_000, v: 0 }, { t: 30_000, v: 2 * MB }]);
  assert.deepEqual(series(view, "queue").map((s) => s.t), [20_000, 30_000]);
  // Memory in use = total - free, and never below zero whatever a machine sends.
  assert.deepEqual(series(view, "memory").map((s) => s.v / GB), [20, 0, 44]);
  assert.deepEqual(series({ points: view.points, mem_total: 0 }, "memory"), []);
  assert.deepEqual(series(null, "cpu"), []);
  assert.deepEqual(series({ points: null, mem_total: 1 }, "cpu"), []);
  // Another machine's nonsense is dropped, not drawn.
  const odd = { mem_total: GB, points: [{ t: 1, cpu: -5, mem_free: 0, agent_write: "9" }, { t: "x", cpu: 5, mem_free: 0, agent_write: 1 }, { t: 2, cpu: NaN, mem_free: 0, agent_write: 1 }] };
  assert.deepEqual(series(odd, "cpu"), []);
  assert.deepEqual(series(odd, "agents"), [{ t: 2, v: 1 }]);
});

test("segments: readings further apart than two and a half steps are not joined", () => {
  const at = (...ts) => ts.map((t) => ({ t: t * 1000, v: 1 }));
  // 15-minute steps: 900 s apart joins, an hour apart does not.
  assert.deepEqual(segments(at(0, 900, 1800, 5400, 6300), 900).map((r) => r.length), [3, 2]);
  // 10-second steps right after a restart: minute readings (60 s apart) still join the 10-second ones.
  assert.deepEqual(segments(at(0, 60, 120, 130, 140), 10).map((r) => r.length), [5]);
  assert.deepEqual(segments(at(0, 10, 400), 10).map((r) => r.length), [2, 1]);
  assert.deepEqual(segments([], 10), []);
});

test("axisTop: 100 for CPU, otherwise a round number at or above the highest reading", () => {
  const of = (...vs) => vs.map((v, i) => ({ t: i, v }));
  assert.equal(axisTop("cpu", of(3)), 100);
  assert.equal(axisTop("disk", of(1.4 * MB)), 2_000_000);
  assert.equal(axisTop("disk", of(450)), 500);
  assert.equal(axisTop("queue", of(0.42)), 0.5);
  assert.equal(axisTop("queue", of(7)), 10);
  assert.equal(axisTop("disk", of(1000)), 1000);
  assert.equal(axisTop("disk", of(0, 0)), 1);
  assert.equal(axisTop("disk", []), 1);
});

test("geometry: time runs left to right, a higher reading sits higher, nothing leaves the frame", () => {
  const f = { width: 600, height: 100, from: 1000, to: 2000, top: 50 };
  assert.equal(xOf(f, 1000), 0);
  assert.equal(xOf(f, 1500), 300);
  assert.equal(xOf(f, 2000), 600);
  assert.equal(xOf(f, 9999), 600);
  assert.equal(xOf(f, 0), 0);
  assert.equal(yOf(f, 0), 100);
  assert.equal(yOf(f, 25), 50);
  assert.equal(yOf(f, 50), 0);
  assert.equal(yOf(f, 500), 0);
  assert.equal(linePath(f, [{ t: 1000, v: 0 }, { t: 1500, v: 25 }, { t: 2000, v: 50 }]), "M0.0,100.0 L300.0,50.0 L600.0,0.0");
  assert.equal(linePath(f, [{ t: 1000, v: 0 }]), ""); // a single reading is a dot, not a line
  assert.equal(xOf({ ...f, to: 1000 }, 1000), 0); // an empty window does not divide by zero
});

test("nearest: the reading under the crosshair", () => {
  const s = [{ t: 10, v: 1 }, { t: 20, v: 2 }, { t: 40, v: 3 }];
  assert.deepEqual(nearest(s, 0), { t: 10, v: 1 });
  assert.deepEqual(nearest(s, 24), { t: 20, v: 2 });
  assert.deepEqual(nearest(s, 31), { t: 40, v: 3 });
  assert.equal(nearest([], 5), null);
});

test("formatValue, summary and stepLabel: each resource in its own unit", () => {
  assert.equal(formatValue("cpu", 12.4), "12%");
  assert.equal(formatValue("cpu", 0.4), "<1%");
  assert.equal(formatValue("cpu", 0), "0%");
  assert.equal(formatValue("disk", 1.4 * MB), "1.4 MB/s");
  assert.equal(formatValue("agents", 0), "0 B/s");
  assert.equal(formatValue("memory", 20 * GB), "20.0 GB");
  assert.equal(formatValue("memory", 512 * GB), "512 GB");
  assert.equal(formatValue("queue", 0.416), "0.42");
  assert.equal(summary("disk", series(view, "disk")), "Maximum 2.0 MB/s · Latest 2.0 MB/s · Gaps mean no reading.");
  assert.equal(summary("cpu", series(view, "cpu")), "Maximum 30% · Latest 30% · Gaps mean no reading.");
  assert.equal(summary("queue", []), "No readings for this period.");
  assert.equal(stepLabel(10), "10-second samples");
  assert.equal(stepLabel(60), "1-minute samples");
  assert.equal(stepLabel(900), "15-minute samples");
});

test("todayLine: what was written today; unmeasured is said, never shown as zero", () => {
  assert.equal(
    todayLine({ disk_written: 172.5 * GB, disk_measured: true, agents_written: 41.9 * GB }, 200),
    "Whole disk today: 172.5 GB (all programs, while tmx ran). Agents: 41.9 GB / 200.0 GB budget.",
  );
  assert.equal(
    todayLine({ disk_written: 0, disk_measured: false, agents_written: 0.5 * GB }, 0),
    "Whole disk today: not measured on this machine. Agents: 0.5 GB.",
  );
});

test("historyPath: this machine's route, or the owner's route to another machine by a plain name", () => {
  assert.equal(historyPath(null, "1h"), "/api/resources/history?period=1h");
  assert.equal(historyPath("hostc", "7d"), "/api/machines/hostc/history?period=7d");
  for (const name of ["../config", "a/b", "x?y", "", "Hostc", "a b"]) assert.equal(historyPath(name, "1h"), null, name);
  assert.equal(isPeriod("24h"), true);
  assert.equal(isPeriod("1y"), false);
  assert.equal(isPeriod(undefined), false);
});

// ---- Hardware: temperatures, drive health, the GPU.
test("healthPath, celsius: this machine or another by a plain name; no reading is said", async () => {
  const { healthPath, celsius } = await import("../src/lib/watchdog-format.ts");
  assert.equal(healthPath(null), "/api/machine/health");
  assert.equal(healthPath("hostc"), "/api/machines/hostc/health");
  assert.equal(healthPath("../x"), null);
  assert.equal(celsius(27.94), "27.9 °C");
  assert.equal(celsius(undefined), "no reading");
  assert.equal(celsius(null), "no reading");
});

test("diskLine, diskDetail, diskNeedsAttention: only what a drive reports", async () => {
  const { diskLine, diskDetail, diskNeedsAttention } = await import("../src/lib/watchdog-format.ts");
  const corsair = { name: "Corsair MP400", health: "Healthy", status: "OK", wear_pct: 31, temp_c: 37, power_on_hours: 24858 };
  assert.equal(diskLine(corsair), "Healthy · 37.0 °C · Wear 31 %");
  assert.equal(diskDetail(corsair), "Read errors not reported · Write errors not reported · Uncorrected read not reported / write not reported · 24858 hours");
  assert.equal(diskNeedsAttention(corsair), false);
  // A wear of 0 % and 0 errors are readings; a missing temperature is not 0 °C.
  const samsung = { name: "Samsung SSD 850 EVO", health: "Healthy", status: "OK", wear_pct: 0, read_errors: 0 };
  assert.equal(diskLine(samsung), "Healthy · Wear 0 %");
  assert.equal(diskDetail(samsung), "Read errors 0 · Write errors not reported · Uncorrected read not reported / write not reported · Hours not reported");
  const failing = { name: "Old disk", health: "Warning", status: "OK,Predictive Failure", read_uncorrected: 3 };
  assert.equal(diskLine(failing), "Warning · OK,Predictive Failure");
  assert.equal(diskNeedsAttention(failing), true);
  assert.equal(diskNeedsAttention({ name: "x", health: "Healthy", status: "OK", write_uncorrected: 1 }), true);
  assert.equal(diskLine({ name: "stick" }), "Health not reported");
  assert.equal(diskNeedsAttention({ name: "stick" }), false);
});

test("gpuStats: four tiles; an old card without power draw says so", async () => {
  const { gpuStats } = await import("../src/lib/watchdog-format.ts");
  assert.deepEqual(gpuStats({ name: "RTX 5060 Ti", temp_c: 41, used_gb: 5.6, total_gb: 15.93, util_pct: 1, power_w: 14.22 }).map((s) => s.value), [
    "41 °C",
    "5.6 GB / 15.9 GB",
    "1 %",
    "14 W",
  ]);
  assert.deepEqual(gpuStats({ name: "GT 1030", temp_c: 49, used_gb: 0.95, total_gb: 2, util_pct: 0 }).map((s) => s.value), ["49 °C", "0.9 GB / 2.0 GB", "0 %", "no reading"]);
});

// ---- Per-disk resources: one chart per physical disk.
test("charts: one per disk for disk writes, busy and write operations; the machine's line otherwise", async () => {
  const { charts, diskLabel, diskSamples, axisTop: top, formatValue: fmt } = await import("../src/lib/watchdog-format.ts");
  const disks = [
    { name: "PhysicalDrive0", points: [{ t: 2000, write: 2 * MB, write_max: 3 * MB, write_ops: 120, busy: 35 }, { t: 1000, write: 0, write_max: 0, write_ops: 0, busy: 0 }] },
    { name: "nvme0n1", points: [{ t: 1000, write: MB, write_max: MB }] }, // a system without operation counts
  ];
  const v = { ...view, disks };
  assert.deepEqual(charts(v, "disk").map((c) => [c.key, c.title, c.samples.length]), [
    ["PhysicalDrive0", "Disk writes, every program · Disk 0", 2],
    ["nvme0n1", "Disk writes, every program · nvme0n1", 1],
  ]);
  assert.deepEqual(charts(v, "busy")[0].samples, [{ t: 1000, v: 0 }, { t: 2000, v: 35 }]); // time order; 0 % is a reading
  assert.deepEqual(charts(v, "ops")[1].samples, []); // not reported: a gap, not zero
  // CPU and the others stay the machine's single line.
  assert.deepEqual(charts(v, "cpu").map((c) => c.key), ["cpu"]);
  // No per-disk readings: disk writes fall back to the machine's total; busy and operations have nothing to draw.
  assert.deepEqual(charts(view, "disk").map((c) => [c.key, c.samples.length]), [["disk", 2]]);
  assert.deepEqual(charts({ ...view, disks: [] }, "busy"), []);
  assert.deepEqual(charts({ ...view, disks: null }, "ops"), []);
  // Another machine's nonsense: a nameless disk is skipped, a busy share above 100 % is no reading.
  assert.deepEqual(charts({ ...view, disks: [{ name: "", points: [] }, null, { name: "d", points: null }] }, "disk").map((c) => c.key), ["d"]);
  assert.deepEqual(diskSamples({ name: "d", points: [{ t: 1, write: 1, write_max: 1, busy: 250 }, { t: 2, write: -4, write_max: 0, busy: 50 }] }, "busy"), [{ t: 2, v: 50 }]);
  assert.equal(diskLabel("PhysicalDrive12"), "Disk 12");
  assert.equal(diskLabel("sda"), "sda");
  assert.equal(top("busy", [{ t: 1, v: 3 }]), 100);
  assert.equal(fmt("busy", 35.4), "35%");
  assert.equal(fmt("ops", 120.4), "120 writes/s");
  assert.equal(fmt("ops", 4.25), "4.3 writes/s");
});

test("wouldPause: observe-mode events of the last day, newest first, one per agent and volume", async () => {
  const { wouldPause } = await import("../src/lib/watchdog-format.ts");
  const now = 100 * 3600_000;
  const h = 3600_000;
  const events = [
    { at: now - 3 * h, kind: "disk-reserve-would-brake", agent: "builder", volume: "C:", detail: "older" },
    { at: now - 1 * h, kind: "disk-reserve-would-brake", agent: "builder", volume: "C:", detail: "newer" },
    { at: now - 2 * h, kind: "disk-reserve-would-brake", agent: "builder", volume: "D:", detail: "other volume" },
    { at: now - 2 * h, kind: "disk-reserve-brake", agent: "writer", volume: "C:", detail: "really paused: not in this list" },
    { at: now - 30 * h, kind: "disk-reserve-would-brake", agent: "old", volume: "C:", detail: "more than a day ago" },
    { at: now - h, kind: "memguard-warn", detail: "a level, not a pause" },
    null,
    { at: "x", kind: "disk-reserve-would-brake", agent: "bad", detail: "" },
  ];
  assert.deepEqual(wouldPause(events, now), [
    { at: now - h, agent: "builder", detail: "newer" },
    { at: now - 2 * h, agent: "builder", detail: "other volume" },
  ]);
  assert.deepEqual(wouldPause(null, now), []);
  assert.deepEqual(wouldPause([{ at: now, kind: "x-would-pause", detail: "" }], now), [{ at: now, agent: "An agent", detail: "" }]);
});
