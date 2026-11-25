// The first-run setup's rules (lib/onboarding-rules.ts), without a browser: where it opens by
// itself, how Companion's two links are read, and how the phone step pairs on this line (an
// HTTPS address, a pairing code, the owner PIN; never plain HTTP). The first test's cases are
// Mustneer's (tmx-go 2996c97, PR #342); the walk through the screens is e2e/onboarding.e2e.mjs.
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import {
  ONBOARD_STEPS,
  companionHalves,
  isComputerWindow,
  isLoopbackHost,
  pairedDevices,
  pairingCode,
  phoneAddress,
  phoneWay,
  shouldOffer,
} from "../src/lib/onboarding-rules.ts";

const read = (p) => readFileSync(new URL(p, import.meta.url), "utf8");

test("onboarding: when it opens by itself, and only there", () => {
  const http = { kind: "http", desktop: false };
  assert.equal(shouldOffer({ onboarded_at: 0 }, http, "127.0.0.1"), true, "a 127.0.0.1 page with the owner token, never set up");
  assert.equal(shouldOffer({ onboarded_at: 0 }, http, "localhost"), true);
  assert.equal(shouldOffer({ onboarded_at: 5 }, http, "127.0.0.1"), false, "finished or closed before");
  assert.equal(shouldOffer({ onboarded_at: 0 }, http, "192.168.1.5"), false, "never on a page opened from another device");
  assert.equal(shouldOffer({ onboarded_at: 0 }, http, "mac.tail-abc.ts.net"), false, "never on a phone through the trusted host");
  assert.equal(shouldOffer({}, http, "127.0.0.1"), false, "an older daemon that does not say: not offered");
  assert.equal(shouldOffer(null, http, "127.0.0.1"), false);
  assert.equal(shouldOffer({ onboarded_at: 0 }, null, "127.0.0.1"), false, "not connected yet");
  assert.equal(isComputerWindow({ kind: "http", desktop: true }, "wails"), true, "the desktop app");
  assert.equal(isComputerWindow({ kind: "bridge", desktop: true }, ""), true);
  assert.equal(isComputerWindow({ kind: "mock", desktop: false }, "example.com"), true, "the mock");
  assert.equal(isComputerWindow(http), false, "no page address at all (node): not the computer's window");
  for (const h of ["127.0.0.1", "localhost", "LOCALHOST", "::1", "[::1]", "wails.localhost"]) assert.equal(isLoopbackHost(h), true, h);
  for (const h of ["", "127.0.0.2", "10.0.0.5", "localhost.example.com", "tmx.localhost", "mac.tail-abc.ts.net"]) assert.equal(isLoopbackHost(h), false, h);
});

test("onboarding: the steps, in order", () => {
  assert.deepEqual([...ONBOARD_STEPS], ["welcome", "whatsapp", "phone", "companion", "done"]);
});

test("onboarding: Companion counts as connected only with both of its links", () => {
  const st = (o) => ({
    install: { installed: true, current: true, foreign: false, companion_dir: true, ...o.install },
    link_token: true,
    connected: true,
    key_ok: true,
    checked: true,
    reachable: true,
    ...o,
    ...(o.install ? { install: { installed: true, current: true, foreign: false, companion_dir: true, ...o.install } } : {}),
  });
  const both = companionHalves(st({}));
  const noKey = companionHalves(st({ connected: false, key_ok: undefined }));
  const refused = companionHalves(st({ key_ok: false }));
  const notInstalled = companionHalves(st({ install: { installed: false }, link_token: false }));
  const missing = companionHalves(st({ install: { companion_dir: false, installed: false }, connected: false, reachable: false }));
  assert.deepEqual([both.see, both.ask, both.done], [true, true, true]);
  assert.deepEqual([noKey.see, noKey.ask, noKey.done], [true, false, false]);
  assert.deepEqual([refused.ask, refused.refused, refused.done], [false, true, false]);
  assert.deepEqual([notInstalled.see, notInstalled.ask, notInstalled.done], [false, true, false]);
  assert.deepEqual([missing.missing, missing.notRunning, missing.done], [true, true, false]);
  const nothing = companionHalves(null);
  assert.deepEqual([nothing.see, nothing.ask, nothing.done, nothing.missing, nothing.notRunning], [false, false, false, false, false]);
});

test("onboarding: the phone's address is the trusted HTTPS host, or nothing", () => {
  assert.equal(phoneAddress(["studio.example.ts.net"]), "https://studio.example.ts.net/");
  assert.equal(phoneAddress(["Box.Example.net:8443"]), "https://box.example.net:8443/");
  assert.equal(phoneAddress(["trial.example.ts.net:443"]), "https://trial.example.ts.net/", "the default port is not shown");
  assert.equal(phoneAddress(["not a host", "second.example.net"]), "https://second.example.net/", "the first usable one");
  for (const none of [undefined, null, [], [""], ["localhost"], ["http://box.example.net"], ["https://box.example.net"], ["box.example.net/path"], ["*.example.net"], ["box.example.net:0"], ["box.example.net:port"], ["-box.example.net"], ["box..example.net"], ["user@box.example.net"]]) {
    assert.equal(phoneAddress(none), "", JSON.stringify(none));
  }
  // whatever comes in, what goes out is https:// or nothing
  for (const hosts of [["a.b"], ["x.y:1"], ["http://a.b"], ["javascript:alert(1)"], ["a.b#frag"], ["a.b?x=1"]]) assert.match(phoneAddress(hosts), /^$|^https:\/\/[a-z0-9.:-]+\/$/);
});

test("onboarding: how the phone step pairs", () => {
  const host = { trusted_hosts: ["studio.example.ts.net"] };
  assert.equal(phoneWay(null, null), "none", "nothing known: nothing to scan");
  assert.equal(phoneWay({}, { available: true, set: true }), "none", "no HTTPS address: no pairing, whatever the PIN");
  assert.equal(phoneWay({ trusted_hosts: [] }, { available: true, set: true }), "none");
  assert.equal(phoneWay({ trusted_hosts: ["http://192.168.1.5:8787"] }, { available: true, set: true }), "none", "a plain-HTTP address is not an address to pair on");
  assert.equal(phoneWay(host, { available: true, set: true }), "pin", "the phone pairs itself with the owner PIN");
  assert.equal(phoneWay(host, { available: true, set: false }), "code", "no PIN yet: the owner approves the code here");
  assert.equal(phoneWay(host, { available: false, set: true }), "code", "the PIN cannot be used on this machine");
  assert.equal(phoneWay(host, null), "code");
});

test("onboarding: which devices count as a connected phone, and how a code is typed", () => {
  assert.equal(pairedDevices(null), 0);
  assert.equal(pairedDevices([]), 0);
  assert.equal(pairedDevices([{ recovery: true }]), 0, "a restricted recovery session is not a paired phone");
  assert.equal(pairedDevices([{}, { recovery: false }, { recovery: true }]), 2);
  assert.equal(pairingCode("abcd-2345"), "ABCD2345");
  assert.equal(pairingCode(" ab cd 23 45 67 "), "ABCD2345", "never longer than a code");
  assert.equal(pairingCode(""), "");
});

test("onboarding: the screen pairs a phone only this line's way", () => {
  const screen = read("../src/components/onboarding.tsx");
  const rules = read("../src/lib/onboarding-rules.ts");
  // no one-scan phone connect, no LAN or plain-HTTP pairing path
  for (const gone of ["connect-phone", "ConnectPhone", "/api/phone", "lan.devices", "lan_access", "isLANPage", "usingDeviceToken"]) {
    assert.equal(screen.includes(gone), false, `components/onboarding.tsx: ${gone}`);
    assert.equal(rules.includes(gone), false, `lib/onboarding-rules.ts: ${gone}`);
  }
  assert.equal(/http:\/\//.test(screen), false, "no plain-HTTP address in the screen");
  assert.equal(/["'`]http:\/\//.test(rules), false, "no plain-HTTP address in the rules");
  // the code on screen is the address from phoneAddress, and approving a code goes through the owner's proof
  assert.match(screen, /const address = phoneAddress\(config\?\.trusted_hosts\)/);
  assert.match(screen, /<QRCode value=\{address\}/);
  assert.match(screen, /api\.approveDevice\(code\)/);
  assert.match(read("../src/lib/api.ts"), /approveDevice: \(code: string\) => ownerAction\(/);
});

test("onboarding: it is mounted once, offered once, and both looks can open it again", () => {
  const app = read("../src/App.tsx");
  assert.match(app, /<OnboardingDialog \/>/);
  assert.match(app, /void offerOnboarding\(useStore\.getState\(\)\.transport\)/);
  const settings = read("../src/pages/settings.tsx");
  assert.match(settings, /show\("general"\) && isComputerWindow\(transport\) && <Section title="Setup">/, "under General in classic, on the one page otherwise; never on a paired device");
  assert.match(settings, /\["general", "General", \[[^\]]*"setup"/);
  assert.match(settings, /ui\.openOnboarding\(true\)/);
  assert.match(read("../src/components/command-palette.tsx"), /isComputerWindow\(transport\) && \(\s*<CommandItem[^>]*onSelect=\{\(\) => ui\.openOnboarding\(true\)\}/);
  // closing, skipping or finishing all remember it was shown
  const screen = read("../src/components/onboarding.tsx");
  assert.match(screen, /const close = \(\) => \{\s*ui\.openOnboarding\(false\);\s*void markOnboarded\(\);/);
});
