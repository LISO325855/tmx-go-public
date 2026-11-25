// The classic UI profile (src/lib/ui-profile.ts; the owner, 17 Nov): one switch, set in the daemon's
// config.json ("ui": {"profile": "classic"}) and sent in the snapshot as ui_profile. Off unless
// the daemon says classic, so the default dashboard is untouched.
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { isClassic, mockUIProfile, UI_PROFILE_CLASSIC } from "../src/lib/ui-profile.ts";

const src = (p) => readFileSync(new URL(`../src/${p}`, import.meta.url), "utf8");

test("classic only when the daemon says classic", () => {
  assert.equal(UI_PROFILE_CLASSIC, "classic");
  for (const p of ["classic", "Classic", " classic "]) assert.equal(isClassic(p), true);
  for (const p of [undefined, null, "", "default", "trial", "production", "classic2"]) assert.equal(isClassic(p), false);
});

test("the mock daemon reports classic with ?ui=classic, and nothing otherwise", () => {
  assert.equal(mockUIProfile("?mock=1&ui=classic"), "classic");
  assert.equal(mockUIProfile("?mock=still&ui=Classic"), "classic");
  for (const s of ["?mock=1", "", null, undefined, "?ui=retro", "?mock=1&ui="]) assert.equal(mockUIProfile(s), "");
});

test("the snapshot's ui_profile reaches the store, and the hook reads only that", () => {
  assert.match(src("lib/types.ts"), /ui_profile\?: string/);
  assert.match(src("lib/store.ts"), /uiProfile: s\.ui_profile \?\? ""/);
  const hook = src("hooks/use-classic.ts");
  assert.match(hook, /isClassic\(s\.uiProfile\)/);
  assert.doesNotMatch(hook, /localStorage|location/, "one switch: the daemon's, not a per-browser one");
  assert.match(src("lib/mock/transport.ts"), /ui_profile: this\.uiProfile \|\| undefined/);
});
