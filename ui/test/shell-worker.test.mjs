// The app shell kept by the service worker (public/push-sw.js). User story: as the owner, when my
// phone cannot reach my computer, tmx itself tells me so (and what to check) instead of the
// browser's error page - also when I open it cold. Nothing under /api is ever kept.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';

const ORIGIN = 'https://phone.example.ts.net';
const PAGE = '<html><script type="module" crossorigin src="./assets/index-abc.js"></script><link rel="stylesheet" href="/assets/index-def.css"></html>';

function worker({ enumerate = true } = {}) {
  const events = {}, store = new Map(), net = { up: true, calls: [] };
  const response = (body, ok = true) => ({ ok, body, clone() { return response(body, ok); }, async text() { return body; } });
  const key = r => (typeof r === 'string' ? r : new URL(r.url).pathname);
  const cache = {
    match: async r => store.get(key(r)),
    put: async (r, res) => { store.set(key(r), res); },
    keys: async () => [...store.keys()],
    delete: async k => store.delete(k),
  };
  const namedCaches = new Map();
  const cacheStorage = {
    open: async name => { namedCaches.set(name, cache); return cache; },
    ...(enumerate ? { keys: async () => [...namedCaches.keys()], delete: async name => namedCaches.delete(name) } : {}),
  };
  const fetch = async (r) => {
    const path = key(r);
    net.calls.push(path);
    if (!net.up) throw new TypeError('Failed to fetch');
    if (path === '/') return response(PAGE);
    if (path.startsWith('/assets/')) return response('asset ' + path);
    return response('other');
  };
  const self = { location: { origin: ORIGIN }, addEventListener: (name, fn) => { events[name] = fn; }, clients: { claim: async () => {} }, skipWaiting() {} };
  runInNewContext(readFileSync(new URL('../public/push-sw.js', import.meta.url), 'utf8'),
    { self, URL, fetch, caches: cacheStorage, AbortController, setTimeout, clearTimeout, crypto: { randomUUID: () => 'r' } });
  // get: what a page's request receives (undefined = the worker left it to the browser).
  const get = async (path, mode = 'no-cors', method = 'GET') => {
    let answer; const waits = [];
    events.fetch({ request: { url: ORIGIN + path, method, mode }, respondWith: p => { answer = p; }, waitUntil: p => waits.push(p) });
    const res = answer && await answer;
    await Promise.all(waits);
    return res;
  };
  const activate = async () => { let p; events.activate({ waitUntil: x => { p = x; } }); await p; };
  return { get, activate, store, net };
}

test('one visit keeps the page and the assets it names; offline, the kept shell opens', async () => {
  const w = worker();
  await w.activate();
  assert.deepEqual([...w.store.keys()].sort(), ['/', '/assets/index-abc.js', '/assets/index-def.css']);
  w.net.up = false;
  assert.equal((await w.get('/', 'navigate')).body, PAGE);
  assert.equal((await w.get('/assets/index-abc.js')).body, 'asset /assets/index-abc.js');
});

test('online, the page always comes from the computer; assets come from the kept copy', async () => {
  const w = worker();
  const page = await w.get('/', 'navigate');
  assert.equal(page.body, PAGE);
  assert.ok(w.store.has('/assets/index-abc.js'));
  w.net.calls.length = 0;
  await w.get('/', 'navigate');
  assert.deepEqual(w.net.calls, ['/']); // asked again, nothing re-downloaded
  await w.get('/assets/index-abc.js');
  assert.deepEqual(w.net.calls, ['/']);
  await w.get('/assets/lazy-123.js'); // a chunk loaded later is kept when it is first used
  assert.ok(w.store.has('/assets/lazy-123.js'));
});

test('never the API, other pages, other origins or writes', async () => {
  const w = worker();
  assert.equal(await w.get('/api/snapshot'), undefined);
  assert.equal(await w.get('/api/snapshot', 'navigate'), undefined);
  assert.equal(await w.get('/owner-bootstrap', 'navigate'), undefined);
  assert.equal(await w.get('/', 'navigate', 'POST'), undefined);
  assert.equal(await w.get('/manifest.webmanifest'), undefined);
  assert.equal(w.store.size, 0);
});

test('offline with nothing kept: the browser shows its own error', async () => {
  const w = worker();
  w.net.up = false;
  await assert.rejects(w.get('/', 'navigate'), /Failed to fetch/);
});

test('current offline shell works when CacheStorage enumeration is unavailable', async () => {
  const w = worker({ enumerate: false });
  await w.activate();
  w.net.up = false;
  assert.equal((await w.get('/', 'navigate')).body, PAGE);
  assert.equal((await w.get('/assets/index-abc.js')).body, 'asset /assets/index-abc.js');
});
