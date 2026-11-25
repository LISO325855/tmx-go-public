import test from "node:test";
import assert from "node:assert/strict";
import { agentPinKey, savedAgentPins, saveAgentPins, toggleAgentPin } from "../src/lib/agent-pins.ts";

test("device pins survive reload, distinguish machines, and unpin", () => {
  let stored = null;
  globalThis.localStorage = { getItem: () => stored, setItem: (_, value) => { stored = value; } };
  const a = agentPinKey("hostc", "agent"), b = agentPinKey("hosta", "agent");
  assert.notEqual(a, b);
  saveAgentPins(toggleAgentPin([], a));
  assert.deepEqual(savedAgentPins(), [a]);
  saveAgentPins(toggleAgentPin(savedAgentPins(), b));
  saveAgentPins(toggleAgentPin(savedAgentPins(), a));
  assert.deepEqual(savedAgentPins(), [b]);
  stored = "invalid";
  assert.deepEqual(savedAgentPins(), []);
  stored = '[1,"valid","valid"]';
  assert.deepEqual(savedAgentPins(), ["valid"]);
  globalThis.localStorage = { getItem: () => { throw Error("denied"); }, setItem: () => { throw Error("denied"); } };
  assert.deepEqual(savedAgentPins(), []);
  assert.doesNotThrow(() => saveAgentPins([a]));
  delete globalThis.localStorage;
});
