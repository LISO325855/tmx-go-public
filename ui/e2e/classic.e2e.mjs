// Linux CI only, using the existing throwaway daemon/browser harness.
// The calm top under the classic UI profile (the owner #3, #9, #14; architect, 17 Nov 15:05): the
// start page on a computer is the Architect's conversation, the text is Python's size, there are
// no pins, no mail badge and no badges in the agent header, only its one line. Without the
// profile the default dashboard is as it was.
import { test } from "node:test";
import assert from "node:assert/strict";
import { launchBrowser, openPage, startDaemon } from "./harness.mjs";

const HEAD = "hd3k9q2m7x";
const rootPx = (page) => page.evaluate(() => parseFloat(getComputedStyle(document.documentElement).fontSize));

test("classic: a calm top, Python's text size, and the Architect's conversation as the start page", { timeout: 90_000 }, async () => {
  const d = await startDaemon();
  const browser = await launchBrowser();
  try {
    const { page, errors } = await openPage(browser);
    await page.goto(`${d.url}/?mock=still&ui=classic#/`);
    // the start page is the Architect's conversation
    const panes = page.getByRole("group", { name: "Panes", exact: true });
    await panes.getByRole("button", { name: "Chat", exact: true }).waitFor();
    await page.getByRole("textbox", { name: "Message head", exact: true }).waitFor();
    assert.equal(await page.getByRole("heading", { name: "Pick up a conversation" }).count(), 0, "not Home");
    await page.waitForFunction(() => parseFloat(getComputedStyle(document.documentElement).fontSize) === 16);
    assert.equal(await rootPx(page), 16, "the root is at 100%");

    // the agent view: no pins, no mail badge, no key hint, one header line
    assert.equal(await page.getByRole("region", { name: "Pinned messages" }).count(), 0);
    assert.equal(await page.getByRole("button", { name: "Pin message", exact: true }).count(), 0);
    assert.equal(await page.locator("[data-mail-receipts]").count(), 0);
    assert.equal(await page.getByText("Enter to send").count(), 0);
    await page.locator("[data-agent-line]").waitFor();
    assert.match((await page.locator("[data-agent-line]").innerText()).replace(/\s+/g, " "), /Talks with you, creates projects and delegates to leads\./);
    assert.equal(await page.locator("header [title^='claude-']").count(), 0, "no model badge in the header");

    // the sidebar: Workspace, a search box above the list, Python's rows, "+ Agent" with a project field
    await page.getByRole("link", { name: "Workspace", exact: true }).waitFor();
    assert.equal(await page.getByRole("link", { name: "Home", exact: true }).count(), 0);
    const search = page.getByRole("searchbox", { name: "Search agents, projects, machines" });
    await search.waitFor();
    const rows = page.locator("[data-agent-row]");
    const before = await rows.count();
    assert.ok(before > 1, "agents are listed");
    assert.ok((await page.locator("[data-agent-state]").first().innerText()).trim().length > 0, "the state in words");
    await search.fill("no-agent-has-this-name");
    assert.equal(await rows.count(), 0, "the search box filters the list");
    await search.fill("");
    assert.equal(await rows.count(), before);
    await page.getByRole("button", { name: "New agent", exact: true }).click();
    await page.getByRole("combobox", { name: "Project" }).waitFor();
    await page.keyboard.press("Escape");

    // on a phone the start page stays Home
    const phone = await openPage(browser, { phone: true });
    await phone.page.goto(`${d.url}/?mock=still&ui=classic#/`);
    await phone.page.getByText("Pick up a conversation").waitFor();
    assert.deepEqual([...errors, ...phone.errors], []);
  } finally { await browser.close(); await d.stop(); }
});

test("without the classic profile the start page is Home and the text size is the default", { timeout: 90_000 }, async () => {
  const d = await startDaemon();
  const browser = await launchBrowser();
  try {
    const { page, errors } = await openPage(browser);
    await page.goto(`${d.url}/?mock=still#/`);
    await page.getByText("Pick up a conversation").waitFor();
    assert.equal(await rootPx(page), 20, "Normal is 125%");
    await page.evaluate((id) => { location.hash = `/agents/${id}/conversation`; }, HEAD);
    await page.getByRole("button", { name: "Pin message", exact: true }).first().waitFor();
    assert.equal(await page.locator("[data-agent-line]").count(), 0);
    assert.deepEqual(errors, []);
  } finally { await browser.close(); await d.stop(); }
});
