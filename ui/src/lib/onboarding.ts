// The first-run setup (components/onboarding.tsx; DESIGN.md "Onboarding"): Welcome, WhatsApp, a
// phone, Companion, Done. It opens by itself once, in the owner's window on this computer (the
// desktop app, or a 127.0.0.1 page with the owner token; never a paired phone or another device),
// until config.json "onboarded_at" says it was finished or closed. Settings → Setup and the
// command palette open it again. Its rules are in lib/onboarding-rules.ts (pure, tested in node).
import { api } from "./api";
import { isComputerWindow, shouldOffer } from "./onboarding-rules";
import type { Transport } from "./transport";
import { ui } from "./ui";

export * from "./onboarding-rules";

let offered = false;

/** Once per page: opens the setup when it has never been finished or closed. */
export async function offerOnboarding(tr: Transport | null) {
  if (offered || !isComputerWindow(tr)) return;
  offered = true;
  try {
    if (shouldOffer(await api.getConfig(), tr)) ui.openOnboarding(true);
  } catch {
    /* an older daemon, or offline: nothing to offer */
  }
}

/** Finished or closed: it does not open by itself again (Settings → Setup still has it). */
export async function markOnboarded() {
  try {
    await api.putConfig({ onboarded_at: Date.now() });
  } catch {
    /* offline: it may show once more, which is harmless */
  }
}
