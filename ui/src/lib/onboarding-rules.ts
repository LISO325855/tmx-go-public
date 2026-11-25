// The first-run setup's rules (lib/onboarding.ts, components/onboarding.tsx), pure so node can
// test them (test/onboarding.test.mjs): where it opens by itself, how Companion's two links are
// read, and which way the phone step pairs a phone on this line (an HTTPS address, a pairing
// code and the owner PIN: docs/PHONE-PAIRING.md). The steps, shouldOffer and companionHalves are
// Mustneer's (tmx-go 2996c97, PR #342); the computer's-window check and the phone rules are ours.
import type { CompanionStatus } from "./companion";
import type { ConfigView, OwnerDevice, PinStatus } from "./types";

export const ONBOARD_STEPS = ["welcome", "whatsapp", "phone", "companion", "done"] as const;
export type OnboardStep = (typeof ONBOARD_STEPS)[number];

/** What the rules need to know of the connection (lib/transport.ts Transport). */
export interface WindowKind {
  kind: "http" | "bridge" | "mock";
  desktop: boolean;
}

/** A loopback name: the page was opened on this computer itself. */
export function isLoopbackHost(host: string): boolean {
  const h = host.trim().toLowerCase().replace(/^\[|\]$/g, "");
  return h === "127.0.0.1" || h === "localhost" || h === "::1" || h === "wails.localhost";
}

/** The owner's window on this computer: the desktop app, the mock, or a page on 127.0.0.1.
 * Any other page is a paired device behind the HTTPS address, and never gets the setup. */
export function isComputerWindow(tr: WindowKind | null, host?: string): boolean {
  if (!tr) return false;
  if (tr.kind === "mock" || tr.desktop) return true;
  return isLoopbackHost(host ?? (typeof location === "undefined" ? "" : location.hostname));
}

/** Whether the setup opens by itself: never finished or closed, in the owner's window here. */
export function shouldOffer(c: Pick<ConfigView, "onboarded_at"> | null, tr: WindowKind | null, host?: string): boolean {
  return !!c && c.onboarded_at === 0 && isComputerWindow(tr, host);
}

/** Companion's two links, in the words the setup shows. see: Companion → tmx (tmx is installed
 * into Companion with its token); ask: tmx → Companion (Companion gave tmx a key it accepts). */
export function companionHalves(st: CompanionStatus | null) {
  const i = st?.install;
  const see = !!i && i.installed && i.current && !i.foreign && !!st?.link_token;
  const refused = !!st?.connected && st.key_ok === false;
  const ask = !!st?.connected && !refused;
  return {
    see,
    ask,
    done: see && ask,
    /** Companion never ran for this user on this computer */
    missing: !!i && !i.companion_dir,
    refused,
    /** Companion was looked for and does not answer */
    notRunning: !!st?.checked && !st.reachable,
  };
}

// ---- the phone step -------------------------------------------------------------------------

/** The address a phone opens: https:// and the first trusted host the daemon answers on, or ""
 * when there is none. Always HTTPS, and only an exact DNS name with an optional port: there is
 * no plain-HTTP way to pair on this line. */
export function phoneAddress(hosts: readonly string[] | null | undefined): string {
  for (const raw of hosts ?? []) {
    const h = String(raw).trim().toLowerCase();
    if (/^(?=.{1,253}(?::|$))[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+(?::[1-9]\d{0,4})?$/.test(h)) return `https://${h.replace(/:443$/, "")}/`;
  }
  return "";
}

/** How the phone step pairs: "none" when this computer has no HTTPS address yet; "pin" when the
 * owner PIN is set (the phone pairs itself with it); "code" otherwise (the owner approves here
 * the code the phone shows). */
export type PhoneWay = "none" | "pin" | "code";

export function phoneWay(c: Pick<ConfigView, "trusted_hosts"> | null, pin: Pick<PinStatus, "available" | "set"> | null): PhoneWay {
  if (!phoneAddress(c?.trusted_hosts)) return "none";
  return pin?.available && pin.set ? "pin" : "code";
}

/** The devices that count as "a phone is connected": fully paired ones, not a restricted
 * recovery session. */
export function pairedDevices(list: readonly Pick<OwnerDevice, "recovery">[] | null | undefined): number {
  return (list ?? []).filter((d) => !d.recovery).length;
}

/** A pairing code as the owner types it: upper case, no spaces or dashes (devices.go). */
export function pairingCode(typed: string): string {
  return typed.toUpperCase().replace(/[^A-Z0-9]/g, "").slice(0, 8);
}
