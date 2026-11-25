// "Connect a phone" (components/connect-phone.tsx, components/scan-pair.tsx;
// docs/PHONE-PAIRING.md): the daemon's answers (internal/api/phone.go), which step of the
// guided setup comes next, and the phone's side of the scan. Plain words only: people never
// see Tailscale's jargon, only the app's name where they install or sign in.
// Ported from Mustneer's v0.1.20 phone-connect.ts / phone-pair.ts onto our pairing.
// No imports: the tests run this file as it is.

export type PhoneWay = "tailscale" | "wifi";

export interface PhoneStatus {
  port: number;
  pin_set: boolean;
  wifi: { on: boolean; addresses: { ip: string; interface: string }[] };
  tailscale: {
    installed: boolean;
    state?: string;
    host?: string;
    serving: boolean;
    trusted: boolean;
    ready: boolean;
    url?: string;
    step?: string;
    error?: string;
    link?: string;
    phone_online: boolean;
  };
  best: PhoneWay;
}

export interface PhonePairing {
  code: string;
  host: string;
  url: string;
  expires_at: number;
}

export interface PhoneCode {
  via: PhoneWay;
  pairing: PhonePairing;
}

/** A step of the guided setup, one per screen. */
export type SetupStep = "pin" | "install" | "signin" | "allow" | "phone" | "finish" | "problem";

/** The steps the progress dots count. */
export const SETUP_STEPS: SetupStep[] = ["pin", "install", "signin", "allow", "phone"];

/** The next step from the daemon's check. phoneDone: the owner said the phone has the app
 *  (the daemon seeing a phone online on the tailnet ticks it off by itself). */
export function setupStep(st: PhoneStatus, phoneDone: boolean): SetupStep {
  if (!st.pin_set) return "pin";
  const ts = st.tailscale;
  if (!ts.installed) return "install";
  switch (ts.step) {
    case "install":
      return "install";
    case "start":
    case "login":
      return "signin";
    case "magicdns":
    case "enable":
      return "allow";
    case "busy":
    case "denied":
    case "failed":
      return "problem";
  }
  return phoneDone || ts.phone_online ? "finish" : "phone";
}

/** A step of the same Wi-Fi fallback: the PIN first, then its switch, then a network to be on. */
export type WifiStep = "pin" | "switch" | "network" | "finish";

export function wifiStep(st: PhoneStatus): WifiStep {
  if (!st.pin_set) return "pin";
  if (!st.wifi.on) return "switch";
  if (st.wifi.addresses.length === 0) return "network";
  return "finish";
}

/** Whether this page is opened from another device (a paired phone or browser), not in the
 *  computer's own window: not the desktop app and not a loopback address. */
export function remotePage(hostname: string, desktop: boolean): boolean {
  return !desktop && !/^(127\.0\.0\.1|localhost|\[::1\]|::1|wails\.localhost)$/.test(hostname);
}

/** A paired device as GET /api/devices lists it. kind: "phone" (a phone or tablet), "computer",
 *  or "" for one paired before kinds were kept. */
export interface PairedDevice {
  id: string;
  name: string;
  host: string;
  recovery?: boolean;
  kind?: string;
  lan?: boolean;
}

/** How many phones are connected: every paired device that is not known to be a computer. */
export function phonesConnected(list: PairedDevice[] | null | undefined): number {
  return (list ?? []).filter((d) => d.kind !== "computer" && !d.recovery).length;
}

/** What the phone button in the top bar says. */
export function phoneButtonLabel(n: number): string {
  if (n <= 0) return "Connect a phone";
  return n === 1 ? "1 phone connected · connect another" : `${n} phones connected · connect another`;
}

/** Step N for the progress dots (finish and problem count as the last one reached). */
export function setupIndex(step: SetupStep): number {
  const i = SETUP_STEPS.indexOf(step);
  return i < 0 ? SETUP_STEPS.length - 1 : i;
}

/** What "Allow secure connections" asks the owner to click on the page it opens. */
export function allowHint(st: PhoneStatus | null): string {
  const ts = st?.tailscale;
  if (ts?.step === "enable" && ts.link?.includes("/f/")) return "On the page that opens, click Enable. This page notices by itself.";
  return "On the page that opens, turn on MagicDNS and HTTPS Certificates. This page notices by itself.";
}

/** A problem in plain words, for errors the setup cannot fix by itself. */
export function problemText(st: PhoneStatus | null): { title: string; text: string; command?: string } {
  const ts = st?.tailscale;
  switch (ts?.step) {
    case "busy":
      return { title: "Something else is using the connection", text: "This computer already shares other things through Tailscale. Ask whoever set that up, or stop one of them." };
    case "denied":
      return { title: "One more permission", text: "Tailscale needs your OK once. Copy this into a terminal, press Enter, then come back.", command: "sudo tailscale set --operator=$USER" };
  }
  return { title: "That didn't work", text: ts?.error ? cap(ts.error) + "." : "Something went wrong. Try again in a moment." };
}

/** Where people get Tailscale; on a phone the page offers the App Store or Google Play. */
export const TAILSCALE_DOWNLOAD = "https://tailscale.com/download";

/** The daemon's words, made plain. */
export function plainError(msg: string): string {
  if (/no local network address/i.test(msg)) return "This computer isn't connected to Wi-Fi. Connect it, then try again.";
  if (/^cancelled\.?$/i.test(msg) || /not confirmed/i.test(msg)) return "Not confirmed. Try again when you're ready.";
  if (/does not answer at/i.test(msg)) return "Your computer's secure address isn't answering yet. This can take a minute the first time. Try again.";
  return msg ? cap(msg) : "Something went wrong.";
}

function cap(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

// ---- the phone's side ------------------------------------------------------------------------

/** The code in a #pair=<code> address ("" when it is not one). */
export function pairCodeOf(hash: string): string {
  const m = /^#pair=([A-Za-z0-9-]{4,40})$/.exec(hash);
  return m ? m[1] : "";
}

/** Takes #pair=<code> from the URL once, and out of the address bar and history. */
export function takePairCode(): string {
  if (typeof window === "undefined") return "";
  const code = pairCodeOf(window.location.hash);
  if (code) history.replaceState(null, "", window.location.pathname + window.location.search + "#/");
  return code;
}

/** A short name for this device from its user agent, e.g. "Android · Chrome". */
export function deviceName(ua: string): string {
  const os = /iPad/.test(ua)
    ? "iPad"
    : /iPhone|iPod/.test(ua)
      ? "iPhone"
      : /Android/.test(ua)
        ? "Android"
        : /CrOS/.test(ua)
          ? "Chromebook"
          : /Windows/.test(ua)
            ? "Windows"
            : /Macintosh|Mac OS X/.test(ua)
              ? "Mac"
              : /Linux/.test(ua)
                ? "Linux"
                : "";
  const browser = /Edg(e|A|iOS)?\//.test(ua)
    ? "Edge"
    : /OPR\/|Opera/.test(ua)
      ? "Opera"
      : /SamsungBrowser\//.test(ua)
        ? "Samsung Internet"
        : /Firefox\/|FxiOS\//.test(ua)
          ? "Firefox"
          : /Chrome\/|CriOS\//.test(ua)
            ? "Chrome"
            : /Safari\//.test(ua)
              ? "Safari"
              : "";
  return [os, browser].filter(Boolean).join(" · ") || "Device";
}

/** A failed scan request: the HTTP status (0 = no answer at all) and the daemon's words. */
export class ScanError extends Error {
  status: number;
  waitS: number;
  constructor(status: number, message: string, waitS = 0) {
    super(message);
    this.status = status;
    this.waitS = waitS;
  }
}

async function scanPost<T>(path: string, body: unknown): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
  } catch {
    throw new ScanError(0, "Your phone can't reach your computer right now.");
  }
  const text = await res.text();
  let data: Record<string, unknown> = {};
  try {
    data = text ? JSON.parse(text) : {};
  } catch {
    /* plain text */
  }
  if (!res.ok) throw new ScanError(res.status, typeof data.error === "string" ? data.error : `Request failed (HTTP ${res.status}).`, Number(data.wait_s) || 0);
  return data as T;
}

/** Is the scanned code still good? Asked as the page opens. */
export function checkScanCode(code: string): Promise<{ ok: boolean; pin_set: boolean }> {
  return scanPost("/api/pair/scan/check", { code });
}

/** The scan itself: code + owner PIN. The session arrives as a cookie. */
export function scanPair(code: string, name: string, pin: string): Promise<{ id: string; name: string }> {
  return scanPost("/api/pair/scan", { code, name, pin });
}

/** What to tell the person holding the phone, and whether typing the PIN again can help. */
export function scanProblem(e: unknown): { text: string; retry: boolean } {
  const err = e instanceof ScanError ? e : new ScanError(-1, (e as Error)?.message || "Pairing failed.");
  switch (err.status) {
    case 0:
      return { text: "Your phone can't reach your computer right now. Check that the computer is on, then try again.", retry: true };
    case 403:
      if (/wrong PIN/i.test(err.message)) return { text: "Wrong PIN. Try again.", retry: true };
      return { text: cap(err.message) + ".", retry: false };
    case 404:
      return { text: "This code expired or was already used. On your computer, click Connect a phone again and scan the new code.", retry: false };
    case 409:
      return { text: "Your owner PIN isn't set yet. On your computer, click Connect a phone again: it asks for one first.", retry: false };
    case 429:
      return { text: err.waitS ? `Too many wrong tries. Wait ${err.waitS} s, then try again.` : "Too many tries. Wait a minute, then try again.", retry: true };
  }
  return { text: cap(err.message), retry: true };
}

// ---- a paired phone that cannot reach the computer -------------------------------------------

export interface ReachCause {
  kind: "internet" | "tailscale" | "wifi" | "computer";
  title: string;
  text: string;
}

/** What to check, in order, when the page cannot reach the computer. online: navigator.onLine;
 *  host: the address the page was opened at (a ts.net name is reached through Tailscale). */
export function cantReachCauses(online: boolean, host: string): ReachCause[] {
  const out: ReachCause[] = [];
  if (!online) out.push({ kind: "internet", title: "This phone is offline", text: "Switch on Wi-Fi or mobile data." });
  if (/\.ts\.net$/i.test(host))
    out.push({ kind: "tailscale", title: "Tailscale on this phone", text: "Open the Tailscale app and check that it is switched on and signed in. It is what connects this phone to your computer." });
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(host))
    out.push({ kind: "wifi", title: "The same Wi-Fi", text: "This phone was connected over your home Wi-Fi: it only reaches your computer while both are on that Wi-Fi." });
  out.push({ kind: "computer", title: "Your computer", text: "It has to be on, awake and connected to the internet, with tmx running." });
  return out;
}
