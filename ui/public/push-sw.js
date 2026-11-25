/* Notifications contain no private details or approval capability. */
self.addEventListener("push", (event) => {
  let payload = {};
  try { payload = event.data?.json() || {}; } catch { /* generic alert */ }
  const incident = typeof payload.incident === "string" && /^[a-f0-9]{64}$/.test(payload.incident) ? payload.incident : null;
  event.waitUntil(self.registration.showNotification("Approval needed", {
    body: "Open Tiamatrix to review",
    icon: "/apple-touch-icon.png",
    tag: incident || "tmx-" + crypto.randomUUID(),
    renotify: true,
    requireInteraction: true,
    silent: false,
  }));
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  event.waitUntil((async () => {
    const target = new URL("/#/inbox", self.location.origin).href;
    const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
    const existing = windows.find(client => new URL(client.url).origin === self.location.origin);
    if (existing) {
      await existing.navigate(target);
      return existing.focus();
    }
    return self.clients.openWindow(target);
  })());
});

// Renewal needs the paired phone's live owner session; never store credentials
// in this worker or silently enroll a new endpoint without that confirmation.
self.addEventListener("pushsubscriptionchange", (event) => {
  event.waitUntil((async () => {
    for (const client of await self.clients.matchAll({ type: "window", includeUncontrolled: true })) {
      client.postMessage({ type: "push-renewal-needed" });
    }
  })());
});

// The app's shell: the page itself and its hashed assets, nothing else. A paired phone that
// cannot reach the computer (Tailscale off, computer asleep) then still opens tmx's own
// "Can't reach your computer" screen instead of the browser's error page. Never /api: no
// data and no credentials are kept here, and the page is always asked from the computer first.
const SHELL = "tmx-shell-v2-no-saved-dictation";
const SHELL_MAX = 150;
const SHELL_WAIT = 8000;

async function shellCacheNames() {
  try { return typeof caches.keys === "function" ? await caches.keys() : []; }
  catch { return []; } // current shell remains usable if enumeration is unavailable
}

async function shellTrim(cache) {
  const keys = await cache.keys();
  for (const key of keys.slice(0, Math.max(0, keys.length - SHELL_MAX))) await cache.delete(key);
}

// Keeps the page and the assets it names ("./assets/…" or "/assets/…"), so the shell works
// after ONE visit.
async function shellKeep(page) {
  const cache = await caches.open(SHELL);
  const html = await page.clone().text();
  const wanted = [...html.matchAll(/(?:src|href)="\.?(\/assets\/[^"]+)"/g)].map(m => m[1]);
  for (const path of wanted) {
    if (await cache.match(path)) continue;
    const res = await fetch(path);
    if (!res.ok) throw new Error("Shell asset unavailable");
    await cache.put(path, res);
  }
  await cache.put("/", page);
  for (const name of await shellCacheNames()) {
    if (name.startsWith("tmx-shell-") && name !== SHELL && typeof caches.delete === "function") await caches.delete(name);
  }
  await shellTrim(cache);
}

async function shellFallback(request) {
  for (const name of [SHELL, ...(await shellCacheNames()).filter(n => n.startsWith("tmx-shell-") && n !== SHELL)]) {
    const kept = await (await caches.open(name)).match(request);
    if (kept) return kept;
  }
}

async function shellPage(event) {
  const stop = new AbortController();
  const timer = setTimeout(() => stop.abort(), SHELL_WAIT);
  try {
    const res = await fetch(event.request, { signal: stop.signal });
    if (res.ok) event.waitUntil(shellKeep(res.clone()).catch(() => {}));
    return res;
  } catch (err) {
    const kept = await shellFallback("/");
    if (kept) return kept;
    throw err;
  } finally {
    clearTimeout(timer);
  }
}

async function shellAsset(event) {
  const cache = await caches.open(SHELL);
  const kept = await shellFallback(event.request);
  if (kept) return kept;
  const res = await fetch(event.request);
  if (res.ok) event.waitUntil(cache.put(event.request, res.clone()).then(() => shellTrim(cache)).catch(() => {}));
  return res;
}

self.addEventListener("fetch", (event) => {
  const req = event.request;
  if (req.method !== "GET") return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;
  if (req.mode === "navigate" && url.pathname === "/") event.respondWith(shellPage(event));
  else if (url.pathname.startsWith("/assets/")) event.respondWith(shellAsset(event));
});

self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", event => event.waitUntil((async () => {
  await self.clients.claim();
  try {
    const page = await fetch("/", { cache: "no-cache" });
    if (page.ok) await shellKeep(page);
  } catch { /* offline now: the next visit keeps it */ }
})()));
