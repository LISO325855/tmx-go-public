import assert from "node:assert/strict";
import { createServer } from "vite";
import { chromium } from "playwright";

const vite = await createServer({ server: { host: "127.0.0.1", port: 0 } });
let browser;
try {
  await vite.listen();
  const url = "http://127.0.0.1:" + vite.httpServer.address().port;
  browser = await chromium.launch({ executablePath: process.env.TMX_TEST_CHROME || undefined, headless: true });
  const context = await browser.newContext();
  await context.grantPermissions(["clipboard-read", "clipboard-write"], { origin: url });
  const page = await context.newPage();
  await page.goto(url + "/?mock=usage-fail#/usage");
  await page.getByRole("button", { name: "Refresh", exact: true }).waitFor();
  await page.evaluate(async () => {
    const { useStore } = await import("/src/lib/store.ts");
    const transport = useStore.getState().transport;
    const request = transport.request.bind(transport);
    window.syntheticLogin = { writes: [], starts: [], cancels: 0, terminal: null };
    transport.request = async (method, path, body) => {
      const result = await request(method, path, body);
      if (path === "/api/accounts/claude/login") {
        if (method === "POST") window.syntheticLogin.starts.push(result.session);
        else if (method === "DELETE") window.syntheticLogin.cancels++;
      }
      return result;
    };
    transport.openTerminal = (_path, _cols, _rows, handlers) => {
      window.syntheticLogin.terminal = handlers;
      queueMicrotask(() => handlers.onOpen());
      return {
        write(input) { window.syntheticLogin.writes.push(input); },
        resize() {},
        close() {},
      };
    };
    await transport.request("POST", "/api/accounts/claude/logout");
  });
  await page.getByRole("button", { name: "Connect Claude", exact: true }).click();
  const terminal = page.locator("[data-connect-dialog] .xterm-helper-textarea");
  await terminal.waitFor({ state: "attached" });
  const code = "synthetic-" + "aZ09_#-".repeat(512);
  const bracketed = "\x1b[200~" + code + "\x1b[201~";
  await page.evaluate((code) => navigator.clipboard.writeText(code), code);
  const waitWrites = (count) => page.waitForFunction((n) => window.syntheticLogin.writes.length >= n, count);
  await terminal.focus();
  await page.keyboard.press("Control+v");
  await waitWrites(1);
  await page.keyboard.press("Shift+Insert");
  await waitWrites(2);
  await page.evaluate((code) => { window.runtime = { ClipboardGetText: async () => code }; }, code);
  await page.locator("[data-connect-dialog] .xterm").click({ button: "right" });
  await waitWrites(3);
  const field = page.getByLabel("Paste the code", { exact: true });
  await field.focus();
  await page.keyboard.press("Control+v");
  assert.equal(await field.inputValue(), code);
  await page.getByRole("button", { name: "Send code", exact: true }).click();
  await waitWrites(4);
  assert.deepEqual(await page.evaluate(() => window.syntheticLogin.writes), [
    bracketed, bracketed, bracketed, bracketed + "\r",
  ]);
  await page.evaluate(() => window.syntheticLogin.terminal.onData("\r\nInvalid code\r\n"));
  await page.getByText("Invalid code", { exact: true }).waitFor();
  await page.getByRole("button", { name: "Start again", exact: true }).click();
  await page.waitForFunction(() => window.syntheticLogin.starts.length === 2);
  assert.deepEqual(await page.evaluate(() => {
    const { starts, cancels } = window.syntheticLogin;
    return { distinct: starts[0] !== starts[1], cancels };
  }), { distinct: true, cancels: 1 });
  process.stdout.write("Ctrl+V, Shift+Insert, right-click and field: exact synthetic frames. Invalid code: fresh session.\n");
} finally {
  await browser?.close();
  await vite.close();
}
