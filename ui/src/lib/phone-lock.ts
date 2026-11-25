// The phone lock on a paired phone's page (docs/PHONE-PAIRING.md; the daemon's side is
// internal/api/phonelock.go). The daemon decides and enforces; this page:
//   - covers tmx the moment it goes to the background (so the app switcher's picture shows
//     nothing) and tells the daemon (keepalive POST …/lock/hidden),
//   - on return locks at once when it was away for "Lock after", else asks the daemon
//     (…/alive, also every heartbeat while in front),
//   - shows the lock screen (components/phone-lock.tsx) over the app whenever the daemon says
//     423, and reconnects when it is open again. The app stays mounted underneath, so a
//     message half written is still there after unlocking,
//   - offers the fingerprint / Face ID once after pairing (a WebAuthn passkey; https only).
// The computer's own window (desktop app, 127.0.0.1) and the owner token are never locked.
// Ported from Mustneer's v0.1.20 phone-lock.ts; ours unlocks with the owner PIN.
import { create } from "zustand";
import { creationOptions, credentialJSON, dueOnReturn, lockActive, requestOptions, type LockStatus } from "./phone-lock-core";
import { retryNow, useStore } from "./store";
import { ApiError, onDeviceLocked, type Transport } from "./transport";

/** off: this page does not lock (not a paired phone, the lock is off, no owner PIN);
 *  open; locked; face-offer: "Unlock with your fingerprint next time?" */
export type LockPhase = "off" | "open" | "locked" | "face-offer";

interface LockState {
  phase: LockPhase;
  status: LockStatus | null;
  /** the page is in the background: show nothing of tmx */
  covered: boolean;
  /** bumped when the lock screen should offer the fingerprint by itself */
  prompt: number;
}

export const useLock = create<LockState>(() => ({ phase: "off", status: null, covered: false, prompt: 0 }));
const set = useLock.setState;
const get = useLock.getState;

let tr: Transport | null = null;
let hiddenAt = 0;
let listening = false;
let faceAttempt: { controller: AbortController; done: Promise<void> } | null = null;

/** Whether this page can lock: a paired browser (its session is a cookie, it has no owner
 *  token) that is not the computer itself. */
export function lockApplies(t: Transport | null): boolean {
  if (!t || t.kind !== "http" || t.desktop || t.token || typeof window === "undefined") return false;
  return !/^(127\.0\.0\.1|localhost|\[::1\]|wails\.localhost)$/.test(window.location.hostname);
}

async function call<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  if (!tr) throw new ApiError(0, "not connected");
  return tr.request<T>(method, path, body, signal);
}

// Settle on cancellation even if a browser/provider delays rejecting its operation.
async function abortable<T>(operation: Promise<T>, signal: AbortSignal): Promise<T> {
  let cancel: () => void = () => {};
  const cancelled = new Promise<never>((_resolve, reject) => {
    cancel = () => reject(signal.reason);
    signal.addEventListener("abort", cancel, { once: true });
    if (signal.aborted) cancel();
  });
  try {
    return await Promise.race([operation, cancelled]);
  } finally {
    signal.removeEventListener("abort", cancel);
  }
}

// Bound both the headers and body read; cancellation reaches the actual HTTP fetch.
async function passkeyCall<T>(path: string, signal: AbortSignal, body?: unknown): Promise<T> {
  const request = new AbortController();
  const cancel = () => request.abort(signal.reason);
  signal.addEventListener("abort", cancel, { once: true });
  const timeout = setTimeout(() => request.abort(new DOMException("Fingerprint request timed out", "TimeoutError")), 15_000);
  try {
    signal.throwIfAborted();
    return await abortable(call<T>("POST", path, body, request.signal), request.signal);
  } finally {
    clearTimeout(timeout);
    signal.removeEventListener("abort", cancel);
  }
}

/** At start: finds out where the lock stands. Never blocks the app: a locked phone gets 423
 *  from the daemon anyway, and the lock screen covers what little shows before that. */
export function initPhoneLock(t: Transport): void {
  if (!lockApplies(t)) return;
  tr = t;
  listen();
  void refresh(true);
}

const FACE_ASKED = "tmx.lock-face-asked";

function faceAsked(): boolean {
  try {
    return localStorage.getItem(FACE_ASKED) === "1";
  } catch {
    return true; // no storage: do not ask on every start
  }
}

function rememberFaceAsked() {
  try {
    localStorage.setItem(FACE_ASKED, "1");
  } catch {
    /* asked again next time */
  }
}

function apply(st: LockStatus, first = false) {
  if (!lockActive(st)) {
    set({ phase: "off", status: st, covered: false });
    return;
  }
  if (st.locked) return lockHere(st);
  const was = get().phase;
  set({ status: st });
  if (was !== "face-offer") opened(st);
  if (first) offerFace(st);
}

// Once per phone, where it can work: "Unlock with your fingerprint next time?" Asked right
// after pairing, or after the first unlock with the PIN.
function offerFace(st: LockStatus) {
  if (!st.passkey_possible || st.passkey || faceAsked()) return;
  void platformAuthenticator().then((ok) => {
    if (ok && get().phase === "open") set({ phase: "face-offer" });
  });
}

function opened(st?: LockStatus) {
  const wasLocked = get().phase === "locked";
  set({ phase: "open", covered: document.visibilityState === "hidden", ...(st ? { status: st } : {}) });
  const store = useStore.getState();
  // The daemon refused this page while it was locked: connect again now. (Not at a normal
  // start, where the app is connecting by itself.)
  if (store.transport && (store.status === "locked" || (wasLocked && store.conn !== "open"))) retryNow();
}

function lockHere(st?: LockStatus) {
  const s = get();
  set({ phase: "locked", covered: false, status: st ?? s.status, prompt: s.prompt + 1 });
  if (!st) void refresh();
}

/** Re-reads the lock (the lock screen shows what the daemon says: waits, the fingerprint). */
export async function refresh(first = false): Promise<void> {
  try {
    apply(await call<LockStatus>("GET", "/api/devices/lock"), first);
  } catch (e) {
    // Not paired (any more): nothing to lock. Offline: whatever shows stays.
    if (e instanceof ApiError && (e.status === 401 || e.status === 403)) set({ phase: "off", covered: false });
  }
}

async function alive() {
  try {
    const st = await call<LockStatus>("POST", "/api/devices/lock/alive");
    if (st.locked) lockHere(st);
    else set({ status: st });
  } catch {
    /* offline, or signed out: the app shows that */
  }
}

/** A request that still leaves when the page is going away. */
function beacon(path: string) {
  try {
    void fetch(path, { method: "POST", credentials: "same-origin", keepalive: true, headers: { "Content-Type": "application/json" }, body: "{}" }).catch(() => {});
  } catch {
    /* the daemon's backstop locks it */
  }
}

function onHide() {
  faceAttempt?.controller.abort();
  if (!tr) return;
  const s = get();
  if (s.phase !== "open") return;
  if (!hiddenAt) hiddenAt = Date.now();
  set({ covered: true });
  beacon("/api/devices/lock/hidden");
}

function onShow() {
  if (!tr) return;
  const since = hiddenAt;
  hiddenAt = 0;
  const s = get();
  if (s.phase === "open" && s.status && dueOnReturn(since, Date.now(), s.status.after_seconds)) {
    lockHere(); // away long enough: no flash of tmx while the daemon is asked
    return;
  }
  set({ covered: false, prompt: s.phase === "locked" ? s.prompt + 1 : s.prompt });
  if (s.phase === "open") void alive();
}

function listen() {
  if (listening || typeof document === "undefined") return;
  listening = true;
  // Any request refused with 423: the daemon locked this phone.
  onDeviceLocked(() => {
    if (get().phase !== "locked") lockHere();
  });
  document.addEventListener("visibilitychange", () => (document.visibilityState === "hidden" ? onHide() : onShow()));
  window.addEventListener("pagehide", onHide);
  window.addEventListener("pageshow", (e) => {
    if ((e as PageTransitionEvent).persisted) onShow(); // back from the browser's cache
  });
  // A visible page says it is alive; the daemon locks one it has not heard from (phones drop
  // the background request now and then).
  const beat = () => {
    const s = get();
    if (document.visibilityState === "visible" && s.phase === "open") void alive();
    setTimeout(beat, s.status?.heartbeat_ms || 10_000);
  };
  setTimeout(beat, 10_000);
}

// ---- what the screens do ----------------------------------------------------------------------

/** The owner PIN opens the lock. Throws the daemon's error (ApiError) when it does not. */
export async function unlockWithPin(pin: string): Promise<void> {
  faceAttempt?.controller.abort();
  const st = await call<LockStatus>("POST", "/api/devices/lock/unlock", { pin });
  opened(st);
  offerFace(st);
}

/** Locks this phone now. */
export async function lockNow(): Promise<void> {
  lockHere(await call<LockStatus>("POST", "/api/devices/lock"));
}

/** Is a fingerprint reader or Face ID there to be used on this page? */
export async function platformAuthenticator(): Promise<boolean> {
  try {
    return (
      typeof window !== "undefined" &&
      window.isSecureContext &&
      typeof PublicKeyCredential !== "undefined" &&
      (await PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable())
    );
  } catch {
    return false;
  }
}

/** "Use my fingerprint": makes this phone's passkey for this address. */
export async function setUpFace(): Promise<void> {
  rememberFaceAsked();
  const opts = await call<Record<string, unknown>>("POST", "/api/devices/lock/passkey/register");
  const cred = await navigator.credentials.create(creationOptions(opts) as unknown as CredentialCreationOptions);
  if (!cred) throw new Error("The fingerprint was not set up.");
  const st = await call<LockStatus>("POST", "/api/devices/lock/passkey/register/finish", credentialJSON(cred as unknown as Parameters<typeof credentialJSON>[0]));
  opened(st);
}

/** "Not now" on the offer. */
export function skipFace() {
  rememberFaceAsked();
  opened(get().status ?? undefined);
}

/** No more fingerprint at this address (the PIN stays). */
export async function removeFace(): Promise<void> {
  set({ status: await call<LockStatus>("DELETE", "/api/devices/lock/passkey") });
}

/** "Unlock with my fingerprint". */
export async function unlockWithFace(automatic = false): Promise<void> {
  // Concurrent prompts must not replace the challenge of an active assertion. After a
  // background interruption, wait for its requests to settle before issuing a new one.
  if (faceAttempt) {
    if (!faceAttempt.controller.signal.aborted) return faceAttempt.done;
    await faceAttempt.done.catch(() => {});
    return unlockWithFace(automatic);
  }
  const controller = new AbortController();
  const { signal } = controller;
  const current = () => {
    signal.throwIfAborted();
    if (document.visibilityState !== "visible" || get().phase !== "locked") throw new DOMException("Unlock interrupted", "AbortError");
  };
  const done = (async () => {
    for (let attempt = 0; ; attempt++) {
      current();
      // Never keep options across attempts, retries, or a trip to the background.
      const opts = await passkeyCall<Record<string, unknown>>("/api/devices/lock/passkey/unlock", signal);
      current();
      const cred = await abortable(navigator.credentials.get({ ...(requestOptions(opts) as unknown as CredentialRequestOptions), signal }), signal);
      current();
      if (!cred) throw new Error("The fingerprint did not unlock tmx.");
      try {
        const st = await passkeyCall<LockStatus>("/api/devices/lock/passkey/unlock/finish", signal, credentialJSON(cred as unknown as Parameters<typeof credentialJSON>[0]));
        current();
        opened(st);
        return;
      } catch (e) {
        current();
        if (!automatic || attempt !== 0 || !(e instanceof ApiError) || e.data?.code !== "passkey_challenge") throw e;
      }
    }
  })();
  faceAttempt = { controller, done };
  try {
    await done;
  } finally {
    if (faceAttempt?.controller === controller) faceAttempt = null;
  }
}
