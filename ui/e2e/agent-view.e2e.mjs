// Linux CI only, using the existing throwaway daemon/browser harness.
// The Chat / Terminal / Details switch of an agent under the classic UI profile (the owner #7,
// 17 Nov): Python's toggle. A click only changes the view, the agent keeps running the way it
// ran, there is no card or bar, the message box is under both, and the last of Chat / Terminal
// is remembered per agent across a reload. Without the profile the tabs are the default ones.
import { test } from "node:test";
import assert from "node:assert/strict";
import { launchBrowser, openPage, startDaemon } from "./harness.mjs";

const HEAD = "hd3k9q2m7x"; // the mock's head: Claude Code (classic: in its own screen)
const LEAD = "ld8q2w4e6r"; // Claude Code
const CODEX = "cx8m2n4b6v"; // Codex, in chat mode under classic too

test("classic: Chat / Terminal / Details only change the view, and the view is remembered per agent", { timeout: 90_000 }, async () => {
  const d = await startDaemon();
  const browser = await launchBrowser();
  try {
    const { page, errors } = await openPage(browser);
    await page.goto(`${d.url}/?mock=still&ui=classic#/agents/${HEAD}`);
    // Wide Classic defaults to split panes; this case exercises the single-pane tabs.
    await page.getByRole("button", { name: "Focus view", exact: true }).click();
    const tab = (name) => page.getByRole("tab", { name, exact: true });
    const selected = async (name) => (await tab(name).getAttribute("aria-selected")) === "true";
    const go = (id) => page.evaluate((x) => { location.hash = `/agents/${x}`; }, id);
    const box = page.getByRole("textbox", { name: /^Message / });

    // Python's words; an agent in its own screen still opens on Chat
    await tab("Chat").waitFor();
    await tab("Terminal").waitFor();
    await tab("Details").waitFor();
    assert.equal(await tab("Conversation").count(), 0);
    assert.equal(await tab("Claude Code").count(), 0);
    assert.ok(await selected("Chat"), "opens on Chat");
    assert.ok(await box.isVisible(), "the message box is under Chat");
    assert.equal(await page.getByText("own screen").count(), 0, "no notice about the mode above the message box");

    // Terminal: the live screen at once, nothing around it
    await tab("Terminal").click();
    assert.ok(await selected("Terminal"));
    await page.locator(".xterm").first().waitFor();
    for (const gone of ["Back to chat", "Don't switch", "Got it", "Stay here"]) {
      assert.equal(await page.getByRole("button", { name: gone }).count(), 0, `no "${gone}" button`);
    }
    assert.equal(await page.getByText("opens when this turn ends").count(), 0);
    assert.ok(await box.isVisible(), "the message box is under Terminal too");

    // looking did not change how the agent runs
    await tab("Details").click();
    await page.getByText("its own screen (read by tmx)").waitFor();
    assert.equal(await page.getByText("when this turn ends").count(), 0, "no pending switch from looking");

    // remembered per agent: another agent still opens on Chat, this one comes back on Terminal
    await go(LEAD);
    await tab("Chat").waitFor();
    assert.ok(await selected("Chat"), "another agent is not moved");
    await go(HEAD);
    await tab("Terminal").waitFor();
    assert.ok(await selected("Terminal"), "the last of Chat / Terminal is remembered, not Details");
    await page.reload();
    await tab("Terminal").waitFor();
    assert.ok(await selected("Terminal"), "remembered across a reload");

    // an agent in chat mode has no screen: one line and an explicit button, nothing happens by looking
    await go(CODEX);
    await tab("Terminal").waitFor();
    assert.ok(await selected("Chat"));
    await tab("Terminal").click();
    await page.getByRole("button", { name: "Open Codex's screen" }).waitFor();
    assert.equal(await page.locator(".xterm").count(), 0);
    await tab("Details").click();
    await page.getByText("Codex · chat").waitFor();
    assert.equal(await page.getByText("when this turn ends").count(), 0);

    // and one click goes back
    await tab("Chat").click();
    assert.ok(await selected("Chat"));
    await box.waitFor();
    assert.deepEqual(errors, []);
  } finally { await browser.close(); await d.stop(); }
});

test("without the classic profile the agent's tabs are the default ones", { timeout: 90_000 }, async () => {
  const d = await startDaemon();
  const browser = await launchBrowser();
  try {
    const { page, errors } = await openPage(browser);
    await page.goto(`${d.url}/?mock=still#/agents/${HEAD}`);
    const tab = (name) => page.getByRole("tab", { name, exact: true });
    await tab("Conversation").waitFor();
    await tab("Claude Code").waitFor();
    await tab("Details").waitFor();
    assert.equal(await tab("Chat").count(), 0);
    assert.equal(await tab("Terminal").count(), 0);
    assert.deepEqual(errors, []);
  } finally { await browser.close(); await d.stop(); }
});
