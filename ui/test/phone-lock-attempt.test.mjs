import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
import { HttpTransport } from "../src/lib/transport.ts";

const source = stripTypeScriptTypes(readFileSync(new URL("../src/lib/phone-lock.ts", import.meta.url), "utf8").replace(/^import .*;\r?$/gm, "")).replace(/export /g, "");
class ApiError extends Error {
  constructor(code) {
    super(code);
    this.data = { code };
  }
}
function harness(finish, getCredential = async () => ({}), request) {
  let state;
  let begins = 0;
  let finishes = 0;
  const challenges = [];
  const document = { visibilityState: "visible" };
  const create = (init) => {
    state = init();
    return { getState: () => state, setState: (patch) => Object.assign(state, patch) };
  };
  const navigator = { credentials: { get: async (options) => { challenges.push(options.challenge); return getCredential(options); } } };
  const transport = { request: async (_method, path, body, signal) => {
    if (request) return request(path, body, signal);
    if (path.endsWith("/finish")) { finishes++; return finish(finishes); }
    return { challenge: ++begins };
  } };
  const make = new Function("create", "ApiError", "requestOptions", "credentialJSON", "navigator", "document", "useStore", source + "\nreturn { unlockWithFace, unlockWithPin, onHide, pending: () => faceAttempt !== null, setup(t) { tr = t; set({ phase: 'locked' }); } };");
  const api = make(create, ApiError, (opts) => opts, (cred) => cred, navigator, document, { getState: () => ({}) });
  api.setup(transport);
  return { ...api, challenges, document, counts: () => ({ begins, finishes }) };
}

test("automatic challenge failure retries once with fresh options", async () => {
  const h = harness((n) => { if (n === 1) throw new ApiError("passkey_challenge"); return {}; });
  await h.unlockWithFace(true);
  assert.deepEqual(h.challenges, [1, 2]);
});

test("stalled begin settles on hide and resume starts a new request", async () => {
  let begins = 0;
  let firstSignal;
  const h = harness(null, undefined, (path, _body, signal) => {
    if (path.endsWith("/finish")) return {};
    if (++begins === 1) {
      firstSignal = signal;
      return new Promise(() => {}); // deliberately ignores cancellation
    }
    return { challenge: begins };
  });
  const first = h.unlockWithFace(true);
  const rejected = assert.rejects(first, { name: "AbortError" });
  h.document.visibilityState = "hidden";
  h.onHide();
  h.document.visibilityState = "visible";
  await h.unlockWithFace(true);
  await rejected;
  assert.equal(firstSignal.aborted, true);
  assert.deepEqual(h.challenges, [2]);
  assert.equal(h.pending(), false);
});

test("PIN submits immediately while biometric begin is stalled", async () => {
  let pinSubmitted = false;
  const h = harness(null, undefined, (path) => {
    if (path === "/api/devices/lock/unlock") {
      pinSubmitted = true;
      return {};
    }
    return new Promise(() => {});
  });
  const rejected = assert.rejects(h.unlockWithFace(true), { name: "AbortError" });
  await h.unlockWithPin("123456");
  await rejected;
  assert.equal(pinSubmitted, true);
  assert.equal(h.pending(), false);

  // The mounted screen must not gate the PIN input, button or submit on faceBusy.
  const screen = readFileSync(new URL("../src/components/phone-lock.tsx", import.meta.url), "utf8").split("function LockScreen()")[1].split("function FaceOffer()")[0];
  assert.match(screen, /<PinInput[^>]*disabled=\{busy \|\| waiting > 0\}/);
  assert.match(screen, /type="submit"[^>]*disabled=\{busy \|\| waiting > 0 \|\| !validPin\(pin\)\}/);
  const face = screen.split("const face = useCallback")[1].split("// The fingerprint")[0];
  assert.doesNotMatch(face, /setBusy\(/);
});

for (const stall of ["begin", "finish"]) {
  test(`${stall} fetch timeout aborts the real transport and settles the attempt`, async (t) => {
    t.mock.timers.enable({ apis: ["setTimeout"] });
    let reached;
    const ready = new Promise((resolve) => { reached = resolve; });
    let fetchSignal;
    t.mock.method(globalThis, "fetch", async (url, options) => {
      if (stall === "begin" || url.endsWith("/finish")) {
        fetchSignal = options.signal;
        reached();
        // Even a stalled response body is bounded by the request deadline.
        return { ok: true, status: 200, text: () => new Promise(() => {}) };
      }
      return { ok: true, status: 200, text: async () => JSON.stringify({ challenge: 1 }) };
    });
    const h = harness(null);
    h.setup(new HttpTransport({ baseURL: "https://example.test", token: "", platform: "android" }));
    const rejected = assert.rejects(h.unlockWithFace(true), { name: "TimeoutError" });
    await ready;
    t.mock.timers.tick(15_000);
    await rejected;
    assert.equal(fetchSignal.aborted, true);
    assert.equal(h.pending(), false);
    // A later attempt is not waiting for the abandoned response body.
    t.mock.method(globalThis, "fetch", async () => ({ ok: true, status: 200, text: async () => "{}" }));
    await h.unlockWithFace(true);
    assert.equal(h.pending(), false);
  });
}

test("automatic retry is bounded; manual and other failures never retry", async () => {
  for (const [automatic, code, count] of [[true, "passkey_challenge", 2], [false, "passkey_challenge", 1], [true, "bad_signature", 1]]) {
    const h = harness(() => { throw new ApiError(code); });
    await assert.rejects(h.unlockWithFace(automatic));
    assert.equal(h.counts().begins, count);
  }
});

test("overlapping prompts share one ceremony", async () => {
  let release;
  const wait = new Promise((resolve) => { release = resolve; });
  const h = harness(() => ({}), () => wait);
  const a = h.unlockWithFace(true);
  const b = h.unlockWithFace(true);
  release({});
  await Promise.all([a, b]);
  assert.deepEqual(h.counts(), { begins: 1, finishes: 1 });
});

test("backgrounded attempt cannot finish; resume fetches fresh options", async () => {
  let release;
  const wait = new Promise((resolve) => { release = resolve; });
  let started;
  const ready = new Promise((resolve) => { started = resolve; });
  let gets = 0;
  const h = harness(() => ({}), () => {
    started();
    return ++gets === 1 ? wait : Promise.resolve({});
  });
  const first = h.unlockWithFace(true);
  const rejected = assert.rejects(first, { name: "AbortError" });
  await ready;
  h.document.visibilityState = "hidden";
  h.onHide();
  h.document.visibilityState = "visible";
  const resumed = h.unlockWithFace(true);
  release({});
  await rejected;
  await resumed;
  assert.deepEqual(h.challenges, [1, 2]);
  assert.deepEqual(h.counts(), { begins: 2, finishes: 1 });
});
