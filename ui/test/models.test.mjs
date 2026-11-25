// The model choices (src/lib/models.ts; the owner #11, 17 Nov: Fable 5.2 was missing).
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { KNOWN_MODELS } from "../src/lib/models.ts";
import { modelName } from "../src/lib/format.ts";

test("Fable 5.2 is offered, first, with the three that were there", () => {
  assert.deepEqual(KNOWN_MODELS, ["claude-fable-5-2", "claude-opus-5-5", "claude-sonnet-5", "claude-haiku-4-5-20251001"]);
  assert.equal(new Set(KNOWN_MODELS).size, KNOWN_MODELS.length);
});

test("every offered model has a short name", () => {
  assert.deepEqual(KNOWN_MODELS.map(modelName), ["Fable 5.2", "Opus 5.5", "Sonnet 5", "Haiku 4.5"]);
});

test("Settings and the profile editor take the list from there", () => {
  const settings = readFileSync(new URL("../src/pages/settings.tsx", import.meta.url), "utf8");
  assert.match(settings, /import \{ KNOWN_MODELS \} from "@\/lib\/models";/);
  assert.match(settings, /export \{ KNOWN_MODELS \};/);
  assert.doesNotMatch(settings, /KNOWN_MODELS = \[/, "one list, not two");
});
