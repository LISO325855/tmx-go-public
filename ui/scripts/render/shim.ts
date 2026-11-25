// A minimal browser for rendering the UI to a string in Node (no real browser runs on the
// owner's PCs): just enough for the modules to load and for the render phase.
const mem = () => {
  const m = new Map<string, string>();
  return {
    getItem: (k: string) => (m.has(k) ? m.get(k)! : null),
    setItem: (k: string, v: string) => void m.set(k, String(v)),
    removeItem: (k: string) => void m.delete(k),
    clear: () => m.clear(),
  };
};
const noop = () => {};
const g = globalThis as Record<string, unknown>;
// a canvas context that accepts any drawing call (the mock draws avatars)
const ctx: unknown = new Proxy(function () {}, { get: (_t, k) => (k === "canvas" ? {} : k === "measureText" ? () => ({ width: 0 }) : ctx), apply: () => ctx, set: () => true });
const el = () => ({ getContext: () => ctx, toDataURL: () => "data:image/png;base64,", width: 0, height: 0, classList: { add: noop, remove: noop, toggle: noop, contains: () => false }, style: {}, dataset: {}, setAttribute: noop, getAttribute: () => null, appendChild: noop, addEventListener: noop, removeEventListener: noop });
g.window = g;
g.location = { hash: "", search: (process.env.RENDER_SEARCH as string) || "", protocol: "http:", href: "http://127.0.0.1/", origin: "http://127.0.0.1", host: "127.0.0.1", hostname: "127.0.0.1", pathname: "/" };
g.history = { replaceState: noop, pushState: noop };
g.addEventListener = noop;
g.removeEventListener = noop;
g.dispatchEvent = () => true;
g.localStorage = mem();
g.sessionStorage = mem();
// a 1280 px wide window (RENDER_WIDTH to change it)
const WIDTH = Number(process.env.RENDER_WIDTH) || 1280;
g.innerWidth = WIDTH;
g.innerHeight = 900;
g.matchMedia = (q: string) => {
  const min = /min-width:\s*([\d.]+)px/.exec(q);
  const max = /max-width:\s*([\d.]+)px/.exec(q);
  const matches = (!min || WIDTH >= Number(min[1])) && (!max || WIDTH <= Number(max[1])) && (!!min || !!max);
  return { matches, media: q, addEventListener: noop, removeEventListener: noop, addListener: noop, removeListener: noop };
};
g.getComputedStyle = () => ({ fontSize: "16px", getPropertyValue: () => "" });
g.document = { documentElement: el(), body: el(), head: el(), hidden: false, visibilityState: "visible", baseURI: "http://127.0.0.1/", addEventListener: noop, removeEventListener: noop, querySelector: () => null, querySelectorAll: () => [], getElementById: () => null, createElement: el, createTextNode: () => ({}) };
if (!("navigator" in g) || !(g.navigator as { platform?: string }).platform) Object.defineProperty(g, "navigator", { value: { platform: "Win32", userAgent: "node", language: "en", clipboard: {} }, configurable: true });
class Obs { observe() {} unobserve() {} disconnect() {} }
g.ResizeObserver = Obs;
g.IntersectionObserver = Obs;
g.MutationObserver = Obs;
g.requestAnimationFrame = (f: () => void) => setTimeout(f, 0);
g.cancelAnimationFrame = (id: number) => clearTimeout(id);

// Rendering to a string asks stores for a "server snapshot" (zustand: the INITIAL state; the
// router: "/"; the theme: none at all). This render wants what the page would show right now,
// so the server snapshot is the current one.
import React from "react";
const R = React as unknown as { useSyncExternalStore: (s: unknown, g: unknown, gs?: unknown) => unknown };
const use = R.useSyncExternalStore;
R.useSyncExternalStore = (subscribe, get) => use(subscribe, get, get);
export {};
