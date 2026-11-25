import assert from "node:assert/strict";
import { test } from "node:test";
import { setTimeout as delay } from "node:timers/promises";
import { launchBrowser, openPage, signIn, startDaemon } from "./harness.mjs";

test("daemon restart preserves open forms and waits for fresh status", { timeout: 120_000 }, async () => {
  const daemon = await startDaemon({ config: { ui: { profile: "classic" } } });
  let browser;
  let restartedPid;
  try {
    browser = await launchBrowser();
    const { page, errors } = await openPage(browser, { origin: daemon.url });
    const { page: settings } = await openPage(browser, { origin: daemon.url });
    await page.exposeFunction("restartScratchDaemon", async () => { restartedPid = await daemon.restart(); });
    await page.addInitScript(({ url, token }) => {
      window.runtime = {};
      window.go = { desktop: { App: {
        Config: async () => ({ url, token, platform: "windows" }),
        RestartDaemon: () => window.restartScratchDaemon(),
      } } };
    }, { url: daemon.url, token: daemon.token });
    const snapshot = await daemon.api("GET", "/api/snapshot");
    await page.goto(daemon.url + "/#/agents/" + snapshot.head.id + "/conversation");
    const draft = page.getByRole("textbox", { name: /^Message / });
    await draft.fill("half a chat message");
    await page.getByRole("button", { name: "New agent", exact: true }).click();
    const form = page.getByRole("dialog", { name: "New agent", exact: true });
    await form.getByRole("textbox", { name: "Name", exact: true }).fill("draft-agent");

    await signIn(settings, daemon);
    await settings.evaluate(() => { location.hash = "#/settings?section=peer-to-peer"; });
    const peers = settings.getByRole("switch", { name: "Peer-to-peer" });
    await peers.waitFor();
    await settings.route("**/api/phone?setup=1", (route) => route.abort());
    const failedRead = settings.waitForEvent("requestfailed", (request) => request.url().endsWith("/api/phone?setup=1"));
    await settings.getByRole("button", { name: /^Connect a phone/ }).first().click();
    await failedRead;
    await settings.getByText("Checking this computer…", { exact: true }).waitFor();
    assert.equal(await settings.getByRole("button", { name: "Download Tailscale" }).count(), 0);

    assert.notEqual(daemon.port, 8797);
    const killedPid = await daemon.kill();
    assert.throws(() => process.kill(killedPid, 0), { code: "ESRCH" });
    const outage = (p) => p.getByRole("dialog", { name: "Tiamatrix lost its connection to its program" });
    await delay(1000);
    assert.equal(await outage(page).count(), 0, "short disconnects use the grace period");
    assert.equal(await form.getByRole("textbox", { name: "Name", exact: true }).inputValue(), "draft-agent");
    await outage(page).waitFor();
    await outage(settings).waitFor();
    for (const route of ["/settings", "/settings?section=peer-to-peer", "/usage", "/companion", "/whatsapp", "/settings?section=voice"]) {
      await settings.evaluate((route) => { location.hash = "#" + route; }, route);
      assert.equal(await settings.getByRole("dialog").count(), 1);
      assert.equal(await settings.getByRole("button").count(), 1);
    }
    await settings.evaluate(() => { location.hash = "#/settings?section=peer-to-peer"; });
    await delay(9000);

    // The fresh peer reading is held back to prove that a prior switch does not leak through.
    let releasePeers;
    const allowPeers = new Promise(resolve => { releasePeers = resolve; });
    await settings.route("**/api/p2p", async (route) => { await allowPeers; await route.continue(); });
    try {
      await outage(page).getByRole("button", { name: "Restart it", exact: true }).click();
      await outage(page).waitFor({ state: "detached" });
      await outage(settings).waitFor({ state: "detached" });
      assert.notEqual(restartedPid, killedPid);
      assert.equal(await form.getByRole("textbox", { name: "Name", exact: true }).inputValue(), "draft-agent");
      await form.getByRole("button", { name: "Close", exact: true }).click();
      assert.equal(await draft.inputValue(), "half a chat message");
      await settings.getByRole("dialog", { name: "Connect a phone" }).getByRole("button", { name: "Close", exact: true }).click();
      assert.equal(await peers.count(), 0);
    } finally {
      releasePeers();
    }
    await peers.waitFor();
    assert.deepEqual(errors, []);
  } finally {
    await browser?.close();
    await daemon.stop();
    if (restartedPid) assert.throws(() => process.kill(restartedPid, 0), { code: "ESRCH" });
  }
});
