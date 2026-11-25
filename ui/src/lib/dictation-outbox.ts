import { create } from "zustand";
import { migrateDictationStores } from "./dictation-migration";
import { useStore } from "./store";
import { DictationQueue, MemoryDictations, deliverDictation, DictationSendTimeout, type DictationDelivery, type QueuedDictation } from "./dictation-queue";

export const useDictationOutbox = create<{ clips: QueuedDictation[]; notices: Record<string, QueuedDictation>; error: string }>(() => ({ clips: [], notices: {}, error: "" }));
const storage = new MemoryDictations();

let deliver: DictationDelivery = () => false;
export function setDictationDelivery(fn: typeof deliver) { deliver = fn; }
export function dictationScope() { return useStore.getState().transport?.baseURL ?? ""; }

async function request<T>(scope: string, method: string, path: string, body?: unknown): Promise<T> {
  const t = useStore.getState().transport;
  if (!t || t.baseURL !== scope) throw new Error("Not connected to this recording's computer yet.");
  if (useStore.getState().status === "locked") throw Object.assign(new Error("The phone is locked."), { status: 423 });
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      t.request<T>(method, path, body),
      new Promise<never>((_, reject) => { timer = setTimeout(() => reject(new Error("Transcription timed out.")), 150_000); }),
    ]);
  } finally { clearTimeout(timer); }
}

const queue = new DictationQueue(storage, {
  transcribe: c => request(c.scope, "POST", `/api/transcribe?request_id=${encodeURIComponent(c.requestId ?? c.id)}`, { data: c.data, ...(c.language ? { language: c.language } : {}) }),
  transcript: (id, c) => request(c.scope, "GET", `/api/transcripts/${encodeURIComponent(id)}`),
}, clip => {
  useDictationOutbox.setState(s => ({ notices: { ...s.notices, [clip.target.id]: clip } }));
});

export function clearDictationNotice(target: string) {
  useDictationOutbox.setState(s => {
    const notices = { ...s.notices }; delete notices[target]; return { notices };
  });
}

let migrationStarted = false;
export function initializeDictationStorage() {
  if (migrationStarted) return;
  migrationStarted = true;
  // No persistent writer exists. Cleanup may hang or fail in an old browser;
  // neither condition may gate new captures or reset newly queued memory work.
  try {
    void migrateDictationStores({
      indexedDB: globalThis.indexedDB, localStorage: globalThis.localStorage,
      sessionStorage: globalThis.sessionStorage,
    }, () => {}).catch(() => console.warn("Legacy dictation cleanup failed; new dictation remains available."));
  } catch { console.warn("Legacy dictation cleanup unavailable; new dictation remains available."); }
}

export async function refreshDictationOutbox() {
  const scope = dictationScope();
  await initializeDictationStorage();
  const clips = (await storage.list()).filter(c => c.scope === scope).sort((a, b) => a.created - b.created);
  useDictationOutbox.setState({ clips: clips.map(clip => ({ ...clip, data: "" })) });
}

export async function flushDictationOutbox() {
  try { await initializeDictationStorage(); }
  catch { useDictationOutbox.setState({ error: "Could not clear old dictations. Please reload." }); return; }
  const scope = dictationScope();
  if (!scope) return;
  const run = async () => {
    await queue.flush(scope, (c, signal) => dictationScope() === c.scope && deliver(c, signal));
    await refreshDictationOutbox();
  };
  try { await run(); }
  catch { useDictationOutbox.setState({ error: "Dictation failed. Please try again." }); }
}

export async function keepDictation(clip: QueuedDictation) {
  await initializeDictationStorage();
  clearDictationNotice(clip.target.id);
  try { await queue.enqueue(clip); }
  catch {
    useDictationOutbox.setState(s => ({ notices: { ...s.notices, [clip.target.id]: { ...clip, data: "", error: "Dictation failed. Please try again." } } }));
    return;
  }
  await refreshDictationOutbox();
  void flushDictationOutbox().catch(() => useDictationOutbox.setState({ error: "Could not clear old dictations. Please reload." }));
}

/** Explicit review sends these words once, without touching the composer draft. */
const accepting = new Set<string>();
export async function acceptDictation(clip: QueuedDictation) {
  if (accepting.has(clip.id) || useDictationOutbox.getState().notices[clip.target.id]?.id !== clip.id
    || dictationScope() !== clip.scope || !clip.held || !clip.text?.trim()) return;
  accepting.add(clip.id);
  try {
    if (await deliverDictation(clip, deliver) && useDictationOutbox.getState().notices[clip.target.id]?.id === clip.id)
      clearDictationNotice(clip.target.id);
  }
  catch (error) {
    if (error instanceof DictationSendTimeout && useDictationOutbox.getState().notices[clip.target.id]?.id === clip.id)
      useDictationOutbox.setState(s => ({ notices: { ...s.notices, [clip.target.id]: { ...clip, heldReason: "send_uncertain", error: error.message } } }));
    throw error;
  } finally { accepting.delete(clip.id); }
}

export function startDictationOutbox() {
  void flushDictationOutbox().catch(() => useDictationOutbox.setState({ error: "Could not clear old dictations. Please reload." }));
  const timer = setInterval(() => void flushDictationOutbox(), 5000);
  const online = () => { void flushDictationOutbox(); };
  window.addEventListener("online", online);
  const unsub = useStore.subscribe((s, prev) => {
    if (s.transport !== prev.transport) useDictationOutbox.setState({ notices: {} });
    if (s.transport !== prev.transport || (s.conn === "open" && prev.conn !== "open")) online();
  });
  return () => { clearInterval(timer); window.removeEventListener("online", online); unsub(); };
}
