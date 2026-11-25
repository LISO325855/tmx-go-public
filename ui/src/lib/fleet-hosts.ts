// Fleet-wide Overview aggregation (WP4, internal/api/fleethosts.go): other local daemons
// configured in config.json "hosts", read over loopback only - no p2p, no new network
// exposure (DESIGN.md plan phase 10, "multi-machine", is not built; issue #90 keeps p2p off).
// Line-building and status logic is pure, in fleet-hosts-format.ts; this file is the zustand
// store and the fetch, kept apart so the pure logic stays unit-testable without a connection.
import { create } from "zustand";
import { fleetHostsResult, hidesOnStatus } from "./fleet-hosts-format";
import { useStore } from "./store";

/** "host" = a loopback daemon on this machine (CPU/mem/disk/head activity, precise). "peer" =
 * a real other machine of the owner's fleet (Go or Python) - what GET /api/sessions carries:
 * its agents, and its own machine reading when it sends one (`measured`). Its
 * running/paused/needs_you are a best-effort read of each session's display state, not the
 * precise enum a same-process host gets. "self" = this machine: only ever the answer's own
 * `self` row, never in `hosts`. */
export type HostKind = "host" | "peer" | "self";

export interface HostSummary {
  kind: HostKind;
  name: string;
  port?: number;
  reachable: boolean;
  error?: string;
  machine?: string;
  agents: number;
  projects: number;
  running: number;
  paused: number;
  needs_you: number;
  cpu?: number;
  mem_free_mb?: number;
  disk_free_gb?: number;
  head_name?: string;
  head_state?: string;
  head_line?: string;
  /** "peer" only: this machine and that peer have each other's pair token (can be Reachable
   * without being Paired, or the reverse, if a token just rotated). */
  paired?: boolean;
  /** cpu, mem_free_mb and mem_total_mb are a real reading (a CPU of 0 % is one). */
  measured?: boolean;
  mem_total_mb?: number;
  disk_total_gb?: number;
  /** That machine's disk guard is holding work back now. */
  disk_busy?: boolean;
  version?: string;
  /** "go" for a Go daemon; absent for a Python peer (the two take different launch bodies). */
  daemon?: string;
  /** Unix ms of its last good answer; kept while it is unreachable. */
  seen?: number;
}

interface FleetHostsState {
  loaded: boolean;
  hosts: HostSummary[];
  /** This machine's own row; null from an older daemon. */
  self: HostSummary | null;
}

export const useFleetHosts = create<FleetHostsState>(() => ({ loaded: false, hosts: [], self: null }));

function transport() {
  const t = useStore.getState().transport;
  if (!t) throw new Error("Not connected yet.");
  return t;
}

/** Fetches the current merge. The owner only (an agent gets 403); an older daemon, 404/501. */
export async function loadFleetHosts() {
  try {
    const v = await transport().request<{ hosts: HostSummary[] | null; self?: HostSummary | null }>("GET", "/api/fleet/hosts");
    useFleetHosts.setState({ ...fleetHostsResult(v.hosts), self: v.self ?? null });
  } catch (e) {
    const status = (e as { status?: number }).status;
    if (hidesOnStatus(status)) useFleetHosts.setState({ loaded: true, hosts: [], self: null });
  }
}

const POLL_MS = 15_000;
let watchers = 0;
let timer: ReturnType<typeof setInterval> | undefined;

/** Keeps the merge fresh while at least one view shows it: one poll for all of them, however
 * many are mounted. Returns the function that stops this view's share (a useEffect cleanup). */
export function watchFleetHosts(): () => void {
  if (watchers++ === 0) timer = setInterval(() => void loadFleetHosts(), POLL_MS);
  void loadFleetHosts();
  let stopped = false;
  return () => {
    if (stopped) return;
    stopped = true;
    if (--watchers === 0 && timer !== undefined) {
      clearInterval(timer);
      timer = undefined;
    }
  };
}
