// Connect a phone (src/lib/phone-connect.ts): which setup step shows, and what the phone says.
// User story: as the owner, I click Connect a phone and am led one step at a time to a code my
// phone's camera opens; on the phone I type my PIN and I am in, or I am told plainly what to do.
import test from "node:test";
import assert from "node:assert/strict";
import {
  allowHint,
  deviceName,
  pairCodeOf,
  plainError,
  problemText,
  ScanError,
  scanProblem,
  setupIndex,
  setupStep,
  SETUP_STEPS,
} from "../src/lib/phone-connect.ts";

const ready = {
  port: 8787,
  pin_set: true,
  wifi: { on: false, addresses: [] },
  tailscale: { installed: true, state: "Running", serving: true, trusted: true, ready: true, phone_online: true },
  best: "tailscale",
};
const withTs = (ts, top = {}) => ({ ...ready, ...top, tailscale: { ...ready.tailscale, ...ts } });

test("setupStep: the first thing still missing, in order", () => {
  assert.equal(setupStep(withTs({}, { pin_set: false }), true), "pin"); // the PIN comes before anything else
  assert.equal(setupStep(withTs({ installed: false, step: "install" }), false), "install");
  assert.equal(setupStep(withTs({ step: "start" }), false), "signin");
  assert.equal(setupStep(withTs({ step: "login" }), false), "signin");
  assert.equal(setupStep(withTs({ step: "magicdns" }), false), "allow");
  assert.equal(setupStep(withTs({ step: "enable" }), false), "allow");
  assert.equal(setupStep(withTs({ step: "busy" }), false), "problem");
  assert.equal(setupStep(withTs({ step: "denied" }), true), "problem");
  // No phone on the tailnet yet: install it there, unless the owner says it is done.
  assert.equal(setupStep(withTs({ phone_online: false }), false), "phone");
  assert.equal(setupStep(withTs({ phone_online: false }), true), "finish");
  assert.equal(setupStep(ready, false), "finish");
  // Serving but not trusted or not answering yet is the finish step's business (it asks once, then waits).
  assert.equal(setupStep(withTs({ trusted: false, ready: false }), false), "finish");
  assert.equal(setupStep(withTs({ ready: false, step: "checking" }), false), "finish");
});

test("setupIndex: finish and problem count as the last dot", () => {
  assert.equal(setupIndex("pin"), 0);
  assert.equal(setupIndex("phone"), SETUP_STEPS.length - 1);
  assert.equal(setupIndex("finish"), SETUP_STEPS.length - 1);
  assert.equal(setupIndex("problem"), SETUP_STEPS.length - 1);
});

test("plain words for the owner", () => {
  assert.match(allowHint(withTs({ step: "enable", link: "https://login.tailscale.com/f/serve?node=x" })), /click Enable/);
  assert.match(allowHint(withTs({ step: "magicdns", link: "https://login.tailscale.com/admin/dns" })), /MagicDNS and HTTPS/);
  assert.equal(problemText(withTs({ step: "denied" })).command, "sudo tailscale set --operator=$USER");
  assert.match(problemText(withTs({ step: "failed", error: "tailscale serve failed: boom" })).text, /^Tailscale serve failed: boom\.$/);
  assert.match(plainError("tmx does not answer at https://box.ts.net yet: timeout"), /isn't answering yet/);
  assert.match(plainError("owner confirmation was not confirmed"), /^Not confirmed/);
});

test("pairCodeOf: only a #pair= address is a scan", () => {
  assert.equal(pairCodeOf("#pair=ABCDEFGHJKLM"), "ABCDEFGHJKLM");
  assert.equal(pairCodeOf("#pair=abcd-efgh-jklm"), "abcd-efgh-jklm");
  assert.equal(pairCodeOf("#/settings"), "");
  assert.equal(pairCodeOf("#pair="), "");
  assert.equal(pairCodeOf("#pair=AB<script>"), "");
  assert.equal(pairCodeOf("#token=ABCDEFGH"), "");
});

test("deviceName: what the computer lists the phone as", () => {
  assert.equal(deviceName("Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Mobile Safari/537.36"), "Android · Chrome");
  assert.equal(deviceName("Mozilla/5.0 (Linux; Android 14; SM-S921B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/26.0 Chrome/122.0 Mobile Safari/537.36"), "Android · Samsung Internet");
  assert.equal(deviceName("Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1"), "iPhone · Safari");
  assert.equal(deviceName(""), "Device");
});

test("scanProblem: what the phone says, and whether typing the PIN again can help", () => {
  assert.deepEqual(scanProblem(new ScanError(403, "wrong PIN")), { text: "Wrong PIN. Try again.", retry: true });
  assert.equal(scanProblem(new ScanError(404, "this code expired")).retry, false);
  assert.match(scanProblem(new ScanError(404, "x")).text, /Connect a phone again/);
  assert.match(scanProblem(new ScanError(429, "wait", 31)).text, /Wait 31 s/);
  assert.equal(scanProblem(new ScanError(429, "too many wrong codes")).retry, true);
  assert.equal(scanProblem(new ScanError(409, "")).retry, false);
  assert.match(scanProblem(new ScanError(0, "x")).text, /can't reach your computer/);
  assert.equal(scanProblem(new ScanError(403, "open the trusted HTTPS address on this device")).retry, false);
  assert.equal(scanProblem(new Error("boom")).text, "Boom");
});

test("cantReachCauses: what to check, in order", async () => {
  const { cantReachCauses } = await import("../src/lib/phone-connect.ts");
  assert.deepEqual(cantReachCauses(true, "box.example.ts.net").map((c) => c.kind), ["tailscale", "computer"]);
  assert.deepEqual(cantReachCauses(false, "box.example.ts.net").map((c) => c.kind), ["internet", "tailscale", "computer"]);
  // Another kind of HTTPS address (the owner's own proxy): no word about Tailscale.
  assert.deepEqual(cantReachCauses(true, "tmx.example.com").map((c) => c.kind), ["computer"]);
});

test("wifiStep: the same Wi-Fi fallback asks for the PIN, then its switch, then a network", async () => {
  const { wifiStep } = await import("../src/lib/phone-connect.ts");
  const wifi = (on, n, top = {}) => ({ ...ready, ...top, wifi: { on, addresses: Array.from({ length: n }, (_, i) => ({ ip: `192.168.1.${i + 2}`, interface: "Wi-Fi" })) } });
  assert.equal(wifiStep(wifi(true, 1, { pin_set: false })), "pin");
  assert.equal(wifiStep(wifi(false, 1)), "switch");
  assert.equal(wifiStep(wifi(true, 0)), "network");
  assert.equal(wifiStep(wifi(true, 2)), "finish");
});

test("remotePage: a phone or another computer, not the computer's own window", async () => {
  const { remotePage } = await import("../src/lib/phone-connect.ts");
  assert.equal(remotePage("box.example.ts.net", false), true);
  assert.equal(remotePage("192.168.1.50", false), true); // the same Wi-Fi fallback
  assert.equal(remotePage("127.0.0.1", false), false);
  assert.equal(remotePage("localhost", false), false);
  assert.equal(remotePage("wails.localhost", true), false);
  assert.equal(remotePage("box.example.ts.net", true), false);
});

test("cantReachCauses: a phone connected over the Wi-Fi is told about the Wi-Fi", async () => {
  const { cantReachCauses } = await import("../src/lib/phone-connect.ts");
  assert.deepEqual(cantReachCauses(true, "192.168.1.50").map((c) => c.kind), ["wifi", "computer"]);
  assert.deepEqual(cantReachCauses(false, "10.0.0.7").map((c) => c.kind), ["internet", "wifi", "computer"]);
});

test("the phone in the top bar: a dot when a phone is connected", async () => {
  const { phonesConnected, phoneButtonLabel } = await import("../src/lib/phone-connect.ts");
  assert.equal(phonesConnected(null), 0);
  assert.equal(phonesConnected([]), 0);
  // a paired computer's browser is not a phone; one paired before kinds were kept counts
  assert.equal(phonesConnected([{ id: "a", name: "Windows · Chrome", host: "h", kind: "computer" }]), 0);
  assert.equal(phonesConnected([{ id: "a", name: "Android · Chrome", host: "h", kind: "phone" }, { id: "b", name: "My phone", host: "h" }, { id: "c", name: "x", host: "h", kind: "computer" }]), 2);
  assert.equal(phonesConnected([{ id: "a", name: "x", host: "h", kind: "phone", recovery: true }]), 0);
  assert.equal(phoneButtonLabel(0), "Connect a phone");
  assert.equal(phoneButtonLabel(1), "1 phone connected · connect another");
  assert.equal(phoneButtonLabel(3), "3 phones connected · connect another");
});
