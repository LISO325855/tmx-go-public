// Text size (the owner, 3 Nov: "everything is too tiny… I have bad eyes"): the whole UI is sized in
// rem, so one root font-size scales every page. Normal is 125% of the old size (the new default,
// also in index.css for the first paint); Large and Larger go further. Saved per device.
export type TextSize = "normal" | "large" | "larger";

export const TEXT_SIZES: { id: TextSize; label: string; percent: number }[] = [
  { id: "normal", label: "Normal", percent: 125 },
  { id: "large", label: "Large", percent: 140 },
  { id: "larger", label: "Larger", percent: 160 },
];

const KEY = "tmx.textSize";
/** The daemon's classic UI profile as last seen, kept only so the first paint has the right size. */
const CLASSIC_KEY = "tmx.textClassic";

/**
 * The root font-size for a text size. classic (lib/ui-profile.ts; architect, 17 Nov): Python's
 * size, where Normal is 100% (the dashboard's 0.875rem body is then Python's 14 px) and Large
 * and Larger keep their proportion to it (112%, 128%).
 */
export function textSizePercent(s: string | null | undefined, classic = false): number {
  const p = (TEXT_SIZES.find((t) => t.id === s) ?? TEXT_SIZES[0]).percent;
  return classic ? Math.round((p * 100) / TEXT_SIZES[0].percent) : p;
}

function savedClassic(): boolean {
  try {
    return localStorage.getItem(CLASSIC_KEY) === "1";
  } catch {
    return false;
  }
}

let classicText: boolean | null = null;

/** Tells the text size whether the classic profile is on (from the snapshot), and re-applies it. */
export function setClassicText(on: boolean) {
  if ((classicText ?? savedClassic()) === on) {
    classicText = on;
    return;
  }
  classicText = on;
  try {
    if (on) localStorage.setItem(CLASSIC_KEY, "1");
    else localStorage.removeItem(CLASSIC_KEY);
  } catch {
    // private mode: the size still applies for this visit
  }
  applyTextSize(savedTextSize(), false);
}

export function savedTextSize(): TextSize {
  try {
    const s = localStorage.getItem(KEY);
    return TEXT_SIZES.some((t) => t.id === s) ? (s as TextSize) : "normal";
  } catch {
    return "normal";
  }
}

/** Applies a text size to the page and saves it on this device. */
export function applyTextSize(s: TextSize, save = true) {
  const root = document.documentElement;
  root.style.fontSize = `${textSizePercent(s, classicText ?? savedClassic())}%`;
  root.dataset.textSize = s;
  if (save) {
    try {
      localStorage.setItem(KEY, s);
    } catch {
      // private mode: the size still applies for this visit
    }
  }
  window.dispatchEvent(new Event("tmx-text-size"));
}

/** How many CSS pixels one rem is now, over 16: for the few sizes JavaScript lays out in px
 * (the canvas's lanes, the Matrix list's rows, the terminal's font). */
export function remScale(): number {
  if (typeof document === "undefined") return 1;
  const px = parseFloat(getComputedStyle(document.documentElement).fontSize);
  return px > 0 ? px / 16 : 1;
}
