// The first-run setup (components/onboarding.tsx) in a real browser, against the mock daemon:
// ?mock=onboarding is a new machine (nothing linked, no phone, no HTTPS address, Companion not
// connected); &onboarded=0 opens it where everything is; &https=1 and &pin=1 give the machine an
// HTTPS address and an owner PIN (lib/mock/devices.ts).
//
// Mustneer's walk-through (tmx-go 2996c97, PR #342, there ui/test/onboarding.test.mjs), moved
// here because on this line a browser runs in Linux CI only, never on the owner's PCs (see
// harness.mjs), and adapted: the phone step is this line's pairing (HTTPS address, pairing code,
// owner PIN), and both looks (the default dashboard and classic) open it from Settings. The rules
// themselves are tested without a browser in test/onboarding.test.mjs.
//
// Not yet in the e2e job's list (.github/workflows/e2e.yml): add e2e/onboarding.e2e.mjs there
// once it has run green in CI.
import test from "node:test";
import assert from "node:assert/strict";
import { createServer } from "vite";
import { chromium } from "playwright";

if (process.platform !== "linux" || process.env.CI !== "true") {
  throw new Error("ui/e2e runs in Linux CI only (CI=true on linux): never a real browser on the owner's PCs");
}

async function withPage(fn) {
  const server = await createServer({ server: { host: "127.0.0.1", port: 0 }, logLevel: "silent" });
  await server.listen();
  let browser;
  try {
    browser = await chromium.launch({ headless: true });
    const page = await browser.newPage({ viewport: { width: 1280, height: 860 } });
    const base = `http://127.0.0.1:${server.httpServer.address().port}/`;
    await fn(page, base);
  } finally {
    await browser?.close();
    await server.close();
  }
}

// The app's own modules, the same instances it uses (Vite serves them by path).
const onboardedAt = (page) => page.evaluate(async () => (await import("/src/lib/api.ts")).api.getConfig().then((c) => c.onboarded_at));
const appReady = (page) => page.locator("aside").first().waitFor();

test("onboarding: opens on a new machine, closing it is remembered, and it does not open once done", async () => {
  await withPage(async (page, base) => {
    await page.goto(base + "?mock=onboarding#/");
    const dlg = page.getByRole("dialog");
    await dlg.getByText("Welcome to tmx").waitFor({ timeout: 15000 });
    assert.equal(await onboardedAt(page), 0);
    await dlg.getByRole("button", { name: "Set up later" }).click();
    await dlg.waitFor({ state: "detached" });
    await page.waitForFunction(async () => ((await (await import("/src/lib/api.ts")).api.getConfig()).onboarded_at ?? 0) > 0);

    // A machine that went through it: nothing opens.
    await page.goto(base + "?mock=still#/");
    await appReady(page);
    await page.waitForTimeout(1500);
    assert.equal(await page.getByText("Welcome to tmx").count(), 0);
  });
});

test("onboarding: every step can be skipped, and Done offers each one again", async () => {
  await withPage(async (page, base) => {
    await page.goto(base + "?mock=onboarding#/");
    const dlg = page.getByRole("dialog");
    await dlg.getByRole("button", { name: "Get started" }).click();
    await dlg.getByRole("heading", { name: "Talk to your agents on WhatsApp" }).waitFor();
    await dlg.getByRole("button", { name: "Not now" }).click();
    // a new machine has no HTTPS address: the phone step says so and has nothing to scan
    await dlg.getByRole("heading", { name: "Use tmx on your phone" }).waitFor();
    await dlg.getByText("secure address (HTTPS)").waitFor();
    assert.equal(await dlg.getByRole("img", { name: "Code that opens tmx on your phone" }).count(), 0);
    await dlg.getByRole("button", { name: "Continue without it" }).click();
    await dlg.getByRole("heading", { name: "Connect Companion" }).waitFor();
    await dlg.getByRole("button", { name: "Not now" }).click();
    await dlg.getByRole("heading", { name: "You're all set" }).waitFor();
    assert.equal(await dlg.getByRole("button", { name: "Set up" }).count(), 3);
    assert.equal(await dlg.getByText("Connected").count(), 0);

    // "Set up" goes back to that step; Back goes back one.
    await dlg.getByRole("button", { name: "Set up" }).first().click();
    await dlg.getByRole("heading", { name: "Talk to your agents on WhatsApp" }).waitFor();
    await dlg.getByRole("button", { name: "Back" }).click();
    await dlg.getByRole("heading", { name: "Welcome to tmx" }).waitFor();

    // Escape closes it, and that counts as done.
    await page.keyboard.press("Escape");
    await dlg.waitFor({ state: "detached" });
    await page.waitForFunction(async () => ((await (await import("/src/lib/api.ts")).api.getConfig()).onboarded_at ?? 0) > 0);
  });
});

test("onboarding: steps already done say so, Companion only when both of its links are", async () => {
  await withPage(async (page, base) => {
    await page.goto(base + "?mock=still&onboarded=0#/");
    const dlg = page.getByRole("dialog");
    await dlg.getByRole("button", { name: "Get started" }).click();
    await dlg.getByRole("heading", { name: "WhatsApp is connected" }).waitFor();
    await dlg.getByRole("button", { name: "Continue" }).click();
    await dlg.getByRole("heading", { name: "Your phone is connected" }).waitFor();
    assert.ok(await dlg.getByRole("button", { name: "Connect another phone" }).isVisible());
    await dlg.getByRole("button", { name: "Continue" }).click();
    await dlg.getByRole("heading", { name: "Companion is connected" }).waitFor();
    assert.equal(await dlg.getByText("(done)").count(), 2);
    await dlg.getByRole("button", { name: "Continue" }).click();
    await dlg.getByRole("heading", { name: "You're all set" }).waitFor();
    assert.equal(await dlg.getByText("Connected", { exact: true }).count(), 3);
    await dlg.getByRole("button", { name: "Start using tmx" }).click();
    await dlg.waitFor({ state: "detached" });
  });
});

test("onboarding: the phone step shows the HTTPS address, and a code approved here connects the phone", async () => {
  await withPage(async (page, base) => {
    await page.goto(base + "?mock=onboarding&https=1#/"); // an HTTPS address, no owner PIN yet
    const dlg = page.getByRole("dialog");
    await dlg.getByRole("button", { name: "Get started" }).click();
    await dlg.getByRole("button", { name: "Not now" }).click();
    await dlg.getByRole("heading", { name: "Use tmx on your phone" }).waitFor();
    await dlg.getByRole("img", { name: "Code that opens tmx on your phone" }).waitFor();
    await dlg.getByText("https://studio.example.ts.net/", { exact: true }).waitFor();
    assert.equal((await dlg.innerText()).includes("http://"), false, "never a plain-HTTP address");
    const approve = dlg.getByRole("button", { name: "Approve this phone" });
    assert.equal(await approve.isDisabled(), true);
    await dlg.getByLabel("Code on your phone").fill("abcd-2345"); // typed loosely: upper case, no dash
    assert.equal(await dlg.getByLabel("Code on your phone").inputValue(), "ABCD2345");
    await approve.click();
    await dlg.getByText("Approved.").waitFor();
    // the phone claims its pairing; the step sees it and moves on by itself
    await dlg.getByRole("heading", { name: "Your phone is connected" }).waitFor({ timeout: 10000 });
    await dlg.getByRole("heading", { name: "Connect Companion" }).waitFor({ timeout: 10000 });
  });
});

test("onboarding: with an owner PIN the phone pairs itself, nothing to type here", async () => {
  await withPage(async (page, base) => {
    await page.goto(base + "?mock=onboarding&https=1&pin=1#/");
    const dlg = page.getByRole("dialog");
    await dlg.getByRole("button", { name: "Get started" }).click();
    await dlg.getByRole("button", { name: "Not now" }).click();
    await dlg.getByRole("heading", { name: "Use tmx on your phone" }).waitFor();
    await dlg.getByText("owner PIN").waitFor();
    assert.equal(await dlg.getByLabel("Code on your phone").count(), 0);
    assert.equal(await dlg.getByRole("button", { name: "Waiting for your phone…" }).isDisabled(), true);
    await dlg.getByRole("button", { name: "Not now" }).click();
    await dlg.getByRole("heading", { name: "Connect Companion" }).waitFor();
  });
});

test("onboarding: Companion ticks off its two links by itself after one Connect", async () => {
  await withPage(async (page, base) => {
    await page.goto(base + "?mock=onboarding#/");
    const dlg = page.getByRole("dialog");
    await dlg.getByRole("button", { name: "Get started" }).click();
    await dlg.getByRole("button", { name: "Not now" }).click();
    await dlg.getByRole("heading", { name: "Use tmx on your phone" }).waitFor();
    await dlg.getByRole("button", { name: "Continue without it" }).click();
    await dlg.getByRole("button", { name: "Connect Companion" }).waitFor();
    assert.equal(await dlg.getByText("(not yet)").count(), 2);
    await dlg.getByRole("button", { name: "Connect Companion" }).click();
    await dlg.getByText("in the Companion window that opens").waitFor();
    // tmx is in Companion at once; Companion's key follows; then the step moves on by itself.
    await dlg.getByRole("heading", { name: "Companion is connected" }).waitFor({ timeout: 15000 });
    await dlg.getByRole("heading", { name: "You're all set" }).waitFor({ timeout: 10000 });
    assert.equal(await dlg.getByText("Connected", { exact: true }).count(), 1);
  });
});

test("onboarding: Settings and the command palette open it again, in both looks", async () => {
  await withPage(async (page, base) => {
    // the default dashboard: Settings is one page
    await page.goto(base + "?mock=still#/settings");
    await page.getByRole("button", { name: "Run setup again" }).click();
    const dlg = page.getByRole("dialog");
    await dlg.getByRole("heading", { name: "Welcome to tmx" }).waitFor();
    await dlg.getByRole("button", { name: "Close setup" }).click();
    await dlg.waitFor({ state: "detached" });

    await page.keyboard.press(process.platform === "darwin" ? "Meta+k" : "Control+k");
    await page.getByRole("option", { name: /Set up tmx/ }).click();
    await page.getByRole("dialog").getByRole("heading", { name: "Welcome to tmx" }).waitFor();

    // classic: Settings has tabs, and Setup is under General
    await page.goto(base + "?mock=still&ui=classic#/settings");
    await page.getByRole("tab", { name: "General" }).click();
    await page.getByRole("button", { name: "Run setup again" }).click();
    await page.getByRole("dialog").getByRole("heading", { name: "Welcome to tmx" }).waitFor();
  });
});
