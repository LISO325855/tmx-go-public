import { useStore } from "./store";

// Each capture has its own ID: another tab finishing cannot clear this one's lease.
// An unreachable daemon must not prevent capture on the device. A reachable daemon that
// already committed its handover refuses admission, avoiding a start/restart race.
export async function audioCaptureLease(): Promise<() => void> {
  const id = crypto.randomUUID();
  const transport = useStore.getState().transport;
  const send = (active: boolean) => transport?.request("POST", "/api/audio/capture", { id, active }) ?? Promise.resolve();
  let timeout: ReturnType<typeof setTimeout> | undefined;
  try {
    if (useStore.getState().conn === "open") {
      await Promise.race([send(true), new Promise<void>(resolve => { timeout = setTimeout(resolve, 2500); })]);
    } else {
      // During an outage capture locally at once; do not wait for a dead node before opening the mic.
      void send(true).catch(() => {});
    }
  } catch (e) { if ((e as { status?: number })?.status === 409) throw e; }
  finally { clearTimeout(timeout); }
  const timer = setInterval(() => void send(true).catch(() => {}), 10_000);
  const unsub = useStore.subscribe((s, prev) => {
    if (s.transport === transport && s.conn === "open" && prev.conn !== "open") void send(true).catch(() => {});
  });
  return () => { clearInterval(timer); unsub(); void send(false).catch(() => {}); };
}
