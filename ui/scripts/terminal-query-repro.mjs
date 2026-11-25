import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { build } from 'esbuild';
import { chromium } from 'playwright';
const result = await build({ stdin: { contents: `import { Terminal } from '@xterm/xterm';
  const t = new Terminal(); t.open(document.getElementById('terminal'));
  window.replies = []; t.onData(data => window.replies.push(data));
  window.query = () => new Promise(resolve => t.write('\\x1b[6n', resolve));`, resolveDir: process.cwd() }, bundle: true, write: false, format: 'esm' });
const server = createServer((req,res) => {
  res.setHeader('Content-Type', req.url === '/app.js' ? 'text/javascript' : 'text/html');
  res.end(req.url === '/app.js' ? result.outputFiles[0].text : '<!doctype html><div id="terminal"></div><script type="module" src="/app.js"></script>');
});
await new Promise(resolve => server.listen(0,'127.0.0.1',resolve));
let browser;
try {
 browser = await chromium.launch({headless:true});
 const page = await browser.newPage();
 await page.goto(`http://127.0.0.1:${server.address().port}`);
 await page.waitForFunction(() => window.query);
 await page.evaluate(() => window.query());
 const replies = await page.evaluate(() => window.replies);
 assert.deepEqual(replies, ['\x1b[1;1R']);
 console.log('No user key pressed: xterm onData emitted cursor-position reply', JSON.stringify(replies));
} finally { await browser?.close(); await new Promise(resolve => server.close(resolve)); }
