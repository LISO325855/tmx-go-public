// The phone lock's plain parts (src/lib/phone-lock-core.ts). User story: as the owner, tmx on my
// phone locks when I put it away and I open it with my fingerprint or my owner PIN; the screen
// tells me plainly what happened when a PIN is refused.
import test from "node:test";
import assert from "node:assert/strict";
import {
  b64urlDecode,
  b64urlEncode,
  biometricName,
  creationOptions,
  credentialJSON,
  dueOnReturn,
  lockActive,
  lockAfterLabel,
  requestOptions,
  unlockProblem,
  waitSeconds,
} from "../src/lib/phone-lock-core.ts";

const status = (o = {}) => ({ device: true, enabled: true, after_seconds: 300, locked: false, pin_set: true, passkey: false, passkey_possible: true, heartbeat_ms: 10000, ...o });

test("lockActive: only a paired phone with the lock on and an owner PIN", () => {
  assert.equal(lockActive(status()), true);
  assert.equal(lockActive(status({ device: false })), false);
  assert.equal(lockActive(status({ enabled: false })), false);
  assert.equal(lockActive(status({ pin_set: false })), false);
  assert.equal(lockActive(null), false);
});

test("the words", () => {
  assert.equal(lockAfterLabel(0), "Immediately");
  assert.equal(lockAfterLabel(30), "30 seconds");
  assert.equal(lockAfterLabel(60), "1 minute");
  assert.equal(lockAfterLabel(300), "5 minutes");
  assert.equal(lockAfterLabel(900), "15 minutes");
  assert.equal(biometricName("Mozilla/5.0 (Linux; Android 15; Pixel 9) Chrome/130.0 Mobile"), "fingerprint");
  assert.equal(biometricName("Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X)"), "Face ID");
  assert.equal(biometricName(""), "fingerprint");
});

test("dueOnReturn and waitSeconds", () => {
  assert.equal(dueOnReturn(0, 10_000_000, 30), false); // it never went away
  assert.equal(dueOnReturn(1000, 30_999, 30), false);
  assert.equal(dueOnReturn(1000, 31_000, 30), true);
  assert.equal(dueOnReturn(1000, 1001, 0), true); // "Immediately"
  assert.equal(waitSeconds(0), 0);
  assert.equal(waitSeconds(-5), 0);
  assert.equal(waitSeconds(1), 1);
  assert.equal(waitSeconds(1001), 2);
});

test("unlockProblem: what the lock screen says", () => {
  assert.deepEqual(unlockProblem(403, "wrong PIN"), { text: "Wrong PIN. Try again.", waitMs: 0 });
  assert.deepEqual(unlockProblem(429, "wait", 31), { text: "Too many wrong PINs. Try again in 31 s.", waitMs: 31000 });
  assert.equal(unlockProblem(429, "busy").waitMs, 60000);
  assert.match(unlockProblem(0, "The daemon is not reachable.").text, /Can't reach your computer/);
  assert.match(unlockProblem(401, "missing or invalid token").text, /Connect a phone/);
  assert.equal(unlockProblem(503, "the owner PIN can't be read").text, "The owner PIN can't be read");
});

test("WebAuthn JSON <-> bytes", () => {
  const bytes = new Uint8Array([0, 1, 2, 250, 251, 252, 253, 254, 255]);
  const s = b64urlEncode(bytes);
  assert.doesNotMatch(s, /[+/=]/);
  assert.deepEqual([...b64urlDecode(s)], [...bytes]);
  assert.deepEqual([...b64urlDecode(b64urlEncode(new Uint8Array([7]).buffer))], [7]);

  const c = creationOptions({ publicKey: { challenge: b64urlEncode(new Uint8Array([1, 2, 3])), rp: { id: "box.ts.net" }, user: { id: b64urlEncode(new Uint8Array([9])), name: "Pixel" }, excludeCredentials: [{ id: b64urlEncode(new Uint8Array([4])), type: "public-key" }] } });
  assert.deepEqual([...c.publicKey.challenge], [1, 2, 3]);
  assert.deepEqual([...c.publicKey.user.id], [9]);
  assert.equal(c.publicKey.user.name, "Pixel");
  assert.deepEqual([...c.publicKey.excludeCredentials[0].id], [4]);
  assert.equal(c.publicKey.rp.id, "box.ts.net");

  const r = requestOptions({ publicKey: { challenge: b64urlEncode(new Uint8Array([5])), rpId: "box.ts.net", allowCredentials: [{ id: b64urlEncode(new Uint8Array([6])), type: "public-key" }], userVerification: "required" } });
  assert.deepEqual([...r.publicKey.challenge], [5]);
  assert.deepEqual([...r.publicKey.allowCredentials[0].id], [6]);
  assert.equal(r.publicKey.userVerification, "required");

  const buf = (...b) => new Uint8Array(b).buffer;
  const made = credentialJSON({ id: "abc", rawId: buf(1), type: "public-key", authenticatorAttachment: "platform", response: { clientDataJSON: buf(2), attestationObject: buf(3), getTransports: () => ["internal"] }, getClientExtensionResults: () => ({}) });
  assert.deepEqual(made, { id: "abc", rawId: "AQ", type: "public-key", authenticatorAttachment: "platform", response: { clientDataJSON: "Ag", attestationObject: "Aw", transports: ["internal"] }, clientExtensionResults: {} });
  const used = credentialJSON({ id: "abc", rawId: buf(1), type: "public-key", response: { clientDataJSON: buf(2), authenticatorData: buf(4), signature: buf(5), userHandle: null } });
  assert.deepEqual(used.response, { clientDataJSON: "Ag", authenticatorData: "BA", signature: "BQ" });
});
