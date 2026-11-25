// Theme: system (default), light or dark. The class on <html> is set before first paint by the
// inline script in index.html; this keeps it in sync afterwards.
import { useSyncExternalStore } from "react";
import { pageBackground } from "./install-colors";

export type ThemePref = "system" | "light" | "dark";
const KEY = "tmx.theme";
const mq = window.matchMedia("(prefers-color-scheme: dark)");
const listeners = new Set<() => void>();

function read(): ThemePref {
  try {
    const v = localStorage.getItem(KEY);
    if (v === "light" || v === "dark") return v;
  } catch {
    /* storage blocked */
  }
  return "system";
}

let pref: ThemePref = read();

export function resolved(): "light" | "dark" {
  return pref === "system" ? (mq.matches ? "dark" : "light") : pref;
}

function apply() {
  const dark = resolved() === "dark";
  document.documentElement.classList.toggle("dark", dark);
  document.documentElement.style.colorScheme = dark ? "dark" : "light";
  // The browser's bar and the first-paint colour follow the theme in force, also one chosen here
  // against the system's (index.html sets the same before the first paint).
  document.documentElement.style.backgroundColor = pageBackground(dark);
  document.querySelectorAll('meta[name="theme-color"]').forEach((m) => m.setAttribute("content", pageBackground(dark)));
  listeners.forEach((l) => l());
}

mq.addEventListener("change", () => pref === "system" && apply());

export function setTheme(p: ThemePref) {
  pref = p;
  try {
    if (p === "system") localStorage.removeItem(KEY);
    else localStorage.setItem(KEY, p);
  } catch {
    /* storage blocked */
  }
  apply();
}

export function toggleTheme() {
  setTheme(resolved() === "dark" ? "light" : "dark");
}

function subscribe(cb: () => void) {
  listeners.add(cb);
  return () => listeners.delete(cb);
}

export function useTheme(): { pref: ThemePref; resolved: "light" | "dark" } {
  const r = useSyncExternalStore(subscribe, resolved);
  const p = useSyncExternalStore(subscribe, () => pref);
  return { pref: p, resolved: r };
}

apply();
