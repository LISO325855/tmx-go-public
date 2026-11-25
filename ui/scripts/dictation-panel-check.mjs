// Headless layout check using the real panel and the production CSS (run npm run build first).
import assert from 'node:assert/strict';
import { readFile, readdir } from 'node:fs/promises';
import { createServer } from 'node:http';
import { build } from 'esbuild';
import { chromium } from 'playwright';

const app = await readFile('src/App.tsx', 'utf8');
assert.doesNotMatch(app, /<DictationOutbox/);
assert.match(app, /useEffect\(startDictationOutbox, \[\]\)/);
assert.match(await readFile('src/pages/settings.tsx', 'utf8'), /show\("voice"\) && <DictationOutbox/);
const result = await build({
  stdin: { contents: `import React from 'react'; import { createRoot } from 'react-dom/client';
    import { DictationOutbox } from './src/components/dictation-outbox';
    createRoot(document.getElementById('root')).render(<><header>Voice settings</header><DictationOutbox /></>);`,
    resolveDir: process.cwd(), loader: 'tsx' },
  bundle: true, write: false, format: 'esm', jsx: 'automatic',
  plugins: [{ name: 'fixtures', setup(b) {
    b.onResolve({ filter: /^@\/lib\/(dictation-outbox|router)$/ }, a => ({ path: a.path, namespace: 'fixture' }));
    b.onLoad({ filter: /.*/, namespace: 'fixture' }, a => ({ contents: a.path.endsWith('router')
      ? 'export const navigate = () => {};'
      : `const state = { error: '', clips: Array.from({length: 12}, (_, i) => ({id: String(i), target: {label: 'Head', path: '/'}, text: 'Saved spoken words '.repeat(20), delivered: true})) };
         export const useDictationOutbox = fn => fn(state);
         export const startDictationOutbox = () => {};
         export const dismissDictation = () => {}; export const retryDictation = () => {}; export const restoreDictation = async () => {};` }));
  }}],
});
const assets = await readdir('dist/assets');
const css = (await Promise.all(assets.filter(f => f.endsWith('.css')).map(f => readFile(`dist/assets/${f}`, 'utf8')))).join('\n');
const server = createServer((req, res) => {
  res.setHeader('Content-Type', req.url === '/app.js' ? 'text/javascript' : 'text/html');
  res.end(req.url === '/app.js' ? result.outputFiles[0].text : `<!doctype html><meta name="viewport" content="width=device-width, initial-scale=1"><style>${css}</style><div id="root"></div><script type="module" src="/app.js"></script>`);
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
let browser;
try {
  browser = await chromium.launch({ headless: true });
  for (const width of [390, 1280]) {
    const page = await browser.newPage({ viewport: { width, height: 844 } });
    await page.goto(`http://127.0.0.1:${server.address().port}`);
    await page.locator('summary').waitFor();
    assert.equal(await page.locator('details').evaluate(e => e.open), false);
    const before = await page.locator('header').boundingBox();
    await page.locator('summary').click();
    assert.equal((await page.locator('header').boundingBox()).y, before.y, 'settings heading stays in place');
    const panel = await page.locator('details > div').boundingBox();
    const cap = await page.evaluate(() => Math.min(innerHeight * 0.4, 15 * parseFloat(getComputedStyle(document.documentElement).fontSize)));
    assert.ok(panel.height <= cap + 1, `expanded panel ${panel.height}px exceeds ${cap}px cap`);
    assert.ok(await page.locator('details > div').evaluate(e => e.scrollHeight > e.clientHeight), 'long list scrolls');
    console.log(`${width}px: settings-only recovery, collapsed by default, height ${panel.height}px with scroll.`);
    await page.close();
  }
} finally {
  await browser?.close();
  await new Promise(resolve => server.close(resolve));
}
