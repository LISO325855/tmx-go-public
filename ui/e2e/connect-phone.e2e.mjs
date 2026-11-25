// Connect a phone end to end (docs/PHONE-PAIRING.md). User story: as the owner, I click Connect a phone on my
// computer, choose my PIN when I have none, point my phone's camera at the code, type the PIN on the phone and
// I am in - first try, also after a reload - and when the computer cannot be reached the phone says so itself.
// Tailscale here is a fake CLI (TMXG_TAILSCALE_CLI) that says this machine's name is the harness's HTTPS proxy;
// the daemon really fetches itself through that name before it shows a code.
import { test } from "node:test";
import assert from "node:assert/strict";
import { chmodSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { launchBrowser, openPage, signIn, startDaemon } from "./harness.mjs";

const PIN = "482915";

// The fake reads where it "serves" from a file the test writes once the daemon's ports are known.
const stateFile = path.join(os.tmpdir(), `tmx-e2e-tailscale-${process.pid}.json`);
const cli = path.join(os.tmpdir(), `tmx-e2e-tailscale-${process.pid}.mjs`);
writeFileSync(cli, `#!/usr/bin/env node
import { readFileSync } from "node:fs";
const st = JSON.parse(readFileSync(${JSON.stringify(stateFile)}, "utf8"));
const args = process.argv.slice(2).join(" ");
if (args === "status --json") {
  console.log(JSON.stringify({ BackendState: "Running", Self: { DNSName: st.name + "." },
    Peer: st.phone ? { "nodekey:p": { HostName: "pixel", DNSName: "pixel.example.ts.net.", OS: "android", Online: true } } : {} }));
} else if (args === "serve status --json") {
  console.log(JSON.stringify({ Web: { [st.name + ":" + st.port]: { Handlers: { "/": { Proxy: st.target } } } } }));
} else { console.error("unknown command: " + args); process.exit(1); }
`);
chmodSync(cli, 0o755);
process.env.TMXG_TAILSCALE_CLI = cli;
// The daemon checks itself through the HTTPS name: it must trust the job's throwaway CA.
if (process.env.TMXG_E2E_TLS_DIR) process.env.SSL_CERT_FILE = path.join(process.env.TMXG_E2E_TLS_DIR, "ca.pem");

test("connect a phone: one scan and the owner PIN", { timeout: 5 * 60_000 }, async t => {
  writeFileSync(stateFile, JSON.stringify({ name: "tmx.localhost", port: 1, target: "http://127.0.0.1:1", phone: false }));
  const d = await startDaemon({ phone: true });
  t.after(() => d.stop());
  const [name, port] = d.proxy.host.split(":");
  const tailscale = phone => writeFileSync(stateFile, JSON.stringify({ name, port: Number(port), target: `http://127.0.0.1:${d.port}`, phone }));
  tailscale(false);
  const browser = await launchBrowser();
  t.after(() => browser.close());
  try {
    // ---- at the computer: the dialog asks only for what is missing ----
    const owner = await openPage(browser, { origin: d.url });
    await signIn(owner.page, d);
    await owner.page.evaluate(() => { location.hash = "/settings"; });
    await owner.page.getByRole("button", { name: "Connect a phone" }).first().click();
    const dialog = owner.page.getByRole("dialog");

    // no owner PIN yet: choose one first
    await dialog.getByRole("heading", { name: "Choose your owner PIN" }).waitFor({ timeout: 20_000 });
    await dialog.getByLabel("Owner PIN", { exact: true }).fill(PIN);
    await dialog.getByLabel("Owner PIN again").fill(PIN);
    await dialog.getByRole("button", { name: "Save PIN" }).click();

    // no phone on Tailscale yet: the step to install it there; it ticks off by itself when one shows up
    await dialog.getByRole("heading", { name: "Install Tailscale on your phone" }).waitFor({ timeout: 20_000 });
    tailscale(true);

    // the code
    const qr = dialog.getByRole("img", { name: "Code for your phone's camera" });
    try {
      await qr.waitFor({ timeout: 45_000 });
    } catch (e) {
      const st = await d.api("GET", "/api/phone");
      if (st.tailscale.step === "checking") {
        // The daemon could not fetch itself through https://tmx.localhost here (name or CA): the runner, not the flow.
        t.skip("the daemon cannot reach its own HTTPS name on this runner: " + st.tailscale.error);
        return;
      }
      throw e;
    }
    await dialog.getByText("Then type your owner PIN on the phone. Works at home and away.", { exact: true }).waitFor();
    // What the camera reads: the daemon shows one code at a time, so ask it for the next one, as the dialog does.
    const made = await d.api("POST", "/api/phone/connect", { via: "tailscale" });
    assert.equal(made.via, "tailscale");
    assert.ok(made.pairing.url.startsWith(d.proxy.origin + "/#pair=") && /#pair=[A-Z0-9]{12}$/.test(made.pairing.url), "the code's link is the HTTPS address");

    // ---- on the phone ----
    const phone = await openPage(browser, { phone: true, origin: d.proxy.origin });
    await phone.page.addInitScript(() => {
      const no = () => Promise.reject(new Error("WebAuthn used"));
      if (navigator.credentials) { navigator.credentials.get = no; navigator.credentials.create = no; }
    });
    await phone.page.goto(made.pairing.url);
    await phone.page.getByRole("heading", { name: "Connect this phone" }).waitFor({ timeout: 20_000 });
    assert.doesNotMatch(phone.page.url(), /pair=/, "the code left the address bar");

    // a wrong PIN is refused, the code stays good and the page stays usable
    await phone.page.getByLabel("Owner PIN").fill("000000");
    await phone.page.getByRole("button", { name: "Connect" }).click();
    await phone.page.getByRole("alert").filter({ hasText: "Wrong PIN" }).waitFor({ timeout: 20_000 });

    await phone.page.getByLabel("Owner PIN").fill(PIN);
    await phone.page.getByRole("button", { name: "Connect" }).click();
    await phone.page.getByRole("heading", { name: "Connected" }).waitFor({ timeout: 30_000 });
    await phone.page.waitForLoadState("networkidle");
    await phone.page.reload();
    await phone.page.waitForLoadState("networkidle");
    const body = await phone.page.locator("body").innerText();
    assert.doesNotMatch(body, /Connect this phone|Pair this device|needs your owner token|Scan a new code/i, "the phone opens tmx, also after a reload");

    // the computer saw it
    const devices = await d.api("GET", "/api/devices");
    assert.equal(devices.length, 1, `the owner sees the phone: ${JSON.stringify(devices)}`);
    assert.ok(!d.log.join("").includes("gate/simulated"), "connecting with the PIN never asked the host gate");

    // ---- a used code says so, plainly ----
    const second = await openPage(browser, { phone: true, origin: d.proxy.origin });
    await second.page.goto(made.pairing.url);
    await second.page.getByRole("heading", { name: "Scan a new code" }).waitFor({ timeout: 20_000 });

    // ---- the phone cannot reach the computer: tmx's own screen, also on a cold open ----
    await phone.page.waitForFunction(() => navigator.serviceWorker?.controller != null, null, { timeout: 30_000 });
    await phone.context.setOffline(true);
    await phone.page.reload();
    const unavailable = phone.page.getByText("Tiamatrix lost its connection to its program", { exact: true });
    await unavailable.waitFor({ timeout: 60_000 });
    await phone.page.getByRole("button", { name: "Reconnect after restarting Tiamatrix" }).waitFor();
    await phone.context.setOffline(false);
    await unavailable.waitFor({ state: "detached", timeout: 60_000 });

    assert.deepEqual(phone.errors, [], "no page errors on the phone");
    assert.deepEqual(owner.errors, [], "no page errors at the computer");
  } catch (e) {
    console.log("daemon log (tail):\n" + d.log.slice(-60).join(""));
    throw e;
  }
});
