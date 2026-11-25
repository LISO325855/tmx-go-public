// Real IndexedDB persistence across page reloads, in desktop and mobile browser contexts.
// Standalone and headless: does not start or contact a daemon.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { transform } from 'esbuild';
import { chromium } from 'playwright';

const source = await readFile(new URL('../src/lib/dictation-queue.ts', import.meta.url), 'utf8');
const { code } = await transform(source, { loader: 'ts', format: 'esm', target: 'es2022' });
const server = createServer((req, res) => {
  res.setHeader('Content-Type', req.url === '/queue.js' ? 'text/javascript' : 'text/html');
  res.end(req.url === '/queue.js' ? code : '<!doctype html><title>Dictation storage check</title>');
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
let browser;
try {
  browser = await chromium.launch({ headless: true });
  for (const mobile of [false, true]) {
    const context = await browser.newContext({ viewport: mobile ? { width: 390, height: 844 } : { width: 1280, height: 800 }, isMobile: mobile, hasTouch: mobile });
    const page = await context.newPage();
    await page.goto(`http://127.0.0.1:${server.address().port}`);
    const saved = await page.evaluate(async () => {
      const { DictationQueue, IndexedDictations } = await import('/queue.js');
      const storage = new IndexedDictations();
      const q = new DictationQueue(storage, { transcribe: async () => { throw Object.assign(new Error('502'), { status: 502 }); } });
      await q.enqueue({ id: 'reload-proof', scope: location.origin, target: { id: 'agent:head', label: 'Head', path: '/agents/head' }, data: 'actual-stored-audio', created: Date.now() });
      await q.flush(location.origin);
      return (await storage.list())[0];
    });
    assert.match(saved.error, /recording is kept/);
    await page.reload();
    const recovered = await page.evaluate(async () => {
      const { DictationQueue, IndexedDictations } = await import('/queue.js');
      const storage = new IndexedDictations();
      let uploads = 0;
      let inserted;
      const q = new DictationQueue(storage, { transcribe: async c => { uploads++; if (c.data !== 'actual-stored-audio') throw new Error('audio changed'); return { text: 'Survived the reload.' }; } });
      await q.flush(location.origin, c => { inserted = { target: c.target.id, text: c.text }; return true; });
      await q.flush(location.origin, () => { throw new Error('duplicate delivery'); });
      return { uploads, inserted, saved: (await storage.list())[0] };
    });
    assert.equal(recovered.uploads, 1);
    assert.deepEqual(recovered.inserted, { target: 'agent:head', text: 'Survived the reload.' });
    assert.equal(recovered.saved.delivered, true);
    assert.equal(recovered.saved.data, 'actual-stored-audio');
    console.log(`${mobile ? 'Mobile' : 'Desktop'}: 502 retained audio in IndexedDB; reload recovered and delivered once.`);
    await context.close();
  }
} finally {
  await browser?.close();
  await new Promise(resolve => server.close(resolve));
}
