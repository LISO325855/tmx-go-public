// The phone lock's plain parts (docs/PHONE-PAIRING.md; the daemon's side is
// internal/api/phonelock.go): the words the screens use and WebAuthn's JSON ↔ bytes.
// Ported from Mustneer's v0.1.20 phone-lock-core.ts; ours unlocks with the owner PIN, so
// there are no PIN rules of its own here. No imports: node's tests load it as is.

/** GET /api/devices/lock and what the lock's routes answer. */
export interface LockStatus {
  device: boolean;
  enabled: boolean;
  after_seconds: number;
  locked: boolean;
  /** the owner PIN exists (without it nothing locks) */
  pin_set: boolean;
  /** this address has a passkey (fingerprint, Face ID) */
  passkey: boolean;
  /** this address can have one (https) */
  passkey_possible: boolean;
  retry_after_ms?: number;
  heartbeat_ms: number;
}

/** GET/PUT /api/phone/lock: the setting, at the computer. */
export interface LockSetting {
  enabled: boolean;
  after_seconds: number;
  choices?: number[];
  pin_set: boolean;
}

/** Does this status mean the device can lock at all? */
export function lockActive(st: LockStatus | null): boolean {
  return !!st && st.device && st.enabled && st.pin_set;
}

export function lockAfterLabel(secs: number): string {
  if (secs <= 0) return "Immediately";
  if (secs < 60) return `${secs} seconds`;
  const m = Math.round(secs / 60);
  return m === 1 ? "1 minute" : `${m} minutes`;
}

/** The phone's own name for unlocking with the body: Face ID on an iPhone, … */
export function biometricName(ua: string): string {
  if (/iPhone|iPad|iPod/i.test(ua)) return "Face ID";
  if (/Macintosh|Mac OS X/i.test(ua) && !/Mobile/i.test(ua)) return "Touch ID";
  if (/Android/i.test(ua)) return "fingerprint";
  if (/Windows/i.test(ua)) return "Windows Hello";
  return "fingerprint";
}

/** Whether a page that went to the background at hiddenAt (ms; 0 = it did not) is due to lock
 *  when it comes back at now. */
export function dueOnReturn(hiddenAt: number, now: number, afterSecs: number): boolean {
  return hiddenAt > 0 && now - hiddenAt >= afterSecs * 1000;
}

/** Whole seconds of a wait, never 0 while it runs. */
export function waitSeconds(ms: number): number {
  return ms > 0 ? Math.ceil(ms / 1000) : 0;
}

/** What the lock screen says about a refused PIN, and how long to wait before the next try.
 *  status: the HTTP status (0 = no answer); waitS: the daemon's wait_s. */
export function unlockProblem(status: number, message: string, waitS = 0): { text: string; waitMs: number } {
  if (status === 0) return { text: "Can't reach your computer right now. Try again in a moment.", waitMs: 0 };
  if (status === 429) {
    const s = waitS > 0 ? waitS : 60;
    return { text: `Too many wrong PINs. Try again in ${s} s.`, waitMs: s * 1000 };
  }
  if (status === 403 && /wrong PIN/i.test(message)) return { text: "Wrong PIN. Try again.", waitMs: 0 };
  if (status === 401) return { text: "This phone is no longer connected. On your computer, click Connect a phone and scan the code again.", waitMs: 0 };
  return { text: message ? message.charAt(0).toUpperCase() + message.slice(1) : "That didn't work.", waitMs: 0 };
}

// ---- WebAuthn: the daemon speaks JSON (base64url), the browser wants bytes ----------------------

export function b64urlDecode(s: string): Uint8Array<ArrayBuffer> {
  const b64 = s.replace(/-/g, "+").replace(/_/g, "/") + "===".slice((s.length + 3) % 4);
  const bin = atob(b64);
  const out = new Uint8Array(new ArrayBuffer(bin.length));
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export function b64urlEncode(buf: ArrayBuffer | ArrayBufferView): string {
  const bytes = buf instanceof ArrayBuffer ? new Uint8Array(buf) : new Uint8Array(buf.buffer, buf.byteOffset, buf.byteLength);
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

type Json = Record<string, unknown>;

function withIds(list: unknown): Json[] | undefined {
  if (!Array.isArray(list)) return undefined;
  return list.map((c: Json) => ({ ...c, id: b64urlDecode(String(c.id)) }));
}

/** POST …/passkey/register's answer ({publicKey}) as navigator.credentials.create's options. */
export function creationOptions(json: Json): { publicKey: Json } {
  const pk = (json.publicKey ?? {}) as Json;
  const user = (pk.user ?? {}) as Json;
  return {
    publicKey: {
      ...pk,
      challenge: b64urlDecode(String(pk.challenge)),
      user: { ...user, id: b64urlDecode(String(user.id)) },
      ...(pk.excludeCredentials ? { excludeCredentials: withIds(pk.excludeCredentials) } : {}),
    },
  };
}

/** POST …/passkey/unlock's answer ({publicKey}) as navigator.credentials.get's options. */
export function requestOptions(json: Json): { publicKey: Json } {
  const pk = (json.publicKey ?? {}) as Json;
  return {
    publicKey: {
      ...pk,
      challenge: b64urlDecode(String(pk.challenge)),
      ...(pk.allowCredentials ? { allowCredentials: withIds(pk.allowCredentials) } : {}),
    },
  };
}

interface CredentialLike {
  id: string;
  rawId: ArrayBuffer;
  type: string;
  authenticatorAttachment?: string | null;
  response: {
    clientDataJSON: ArrayBuffer;
    attestationObject?: ArrayBuffer;
    authenticatorData?: ArrayBuffer;
    signature?: ArrayBuffer;
    userHandle?: ArrayBuffer | null;
    getTransports?: () => string[];
  };
  getClientExtensionResults?: () => unknown;
}

/** A PublicKeyCredential (made or used) as the JSON the daemon reads. */
export function credentialJSON(c: CredentialLike): Json {
  const r = c.response;
  const response: Json = { clientDataJSON: b64urlEncode(r.clientDataJSON) };
  if (r.attestationObject) {
    response.attestationObject = b64urlEncode(r.attestationObject);
    try {
      response.transports = r.getTransports?.() ?? [];
    } catch {
      /* older browsers */
    }
  }
  if (r.authenticatorData) response.authenticatorData = b64urlEncode(r.authenticatorData);
  if (r.signature) response.signature = b64urlEncode(r.signature);
  if (r.userHandle) response.userHandle = b64urlEncode(r.userHandle);
  return {
    id: c.id,
    rawId: b64urlEncode(c.rawId),
    type: c.type,
    authenticatorAttachment: c.authenticatorAttachment ?? undefined,
    response,
    clientExtensionResults: c.getClientExtensionResults?.() ?? {},
  };
}
