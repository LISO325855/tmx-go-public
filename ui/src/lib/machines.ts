// A machine's page (pages/machine.tsx): the fleet roster (GET /api/fleet: this machine's last
// reading of every other machine's agents) and the relay (POST /api/remote) that works an agent
// there. The pure parts are in machines-format.ts; this file is the store and the requests.
import { create } from "zustand";
import type { RemoteRequest, RosterSession } from "./machines-format";
import { useStore } from "./store";

interface RosterState {
  loaded: boolean;
  sessions: RosterSession[];
  /** The last failure reading the roster, in the daemon's own words; "" when it was read. */
  error: string;
}

export const useRoster = create<RosterState>(() => ({ loaded: false, sessions: [], error: "" }));

function transport() {
  const t = useStore.getState().transport;
  if (!t) throw new Error("Not connected yet.");
  return t;
}

export async function loadRoster() {
  try {
    const v = await transport().request<{ sessions: RosterSession[] | null }>("GET", "/api/fleet");
    useRoster.setState({ loaded: true, sessions: v.sessions ?? [], error: "" });
  } catch (e) {
    // Keep the last roster: a failed refresh must not empty the page.
    useRoster.setState({ loaded: true, error: e instanceof Error ? e.message : String(e) });
  }
}

const POLL_MS = 10_000;
let watchers = 0;
let timer: ReturnType<typeof setInterval> | undefined;

/** Keeps the roster fresh while a machine page is open. Returns the cleanup. */
export function watchRoster(): () => void {
  if (watchers++ === 0) timer = setInterval(() => void loadRoster(), POLL_MS);
  void loadRoster();
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

/** Sends one request to another machine through this daemon's relay. The relay decides what it
 * admits (the owner only, for controls and a launch); a refusal comes back as the thrown error. */
export function remote<T = unknown>(req: RemoteRequest): Promise<T> {
  return transport().request<T>("POST", "/api/remote", req);
}

/** Mail to an agent on another machine: the ordinary message route, addressed name@machine. */
export function sendRemoteMail(name: string, host: string, body: string): Promise<unknown> {
  return transport().request("POST", "/api/messages", { to: `${name}@${host}`, body });
}
