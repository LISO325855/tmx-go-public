// The little agent heads (src/lib/agent-face.ts; the owner, 17 Nov 19:01): Python's pixel faces,
// ported one-to-one. The test carries Python's own code (static/index.html, verbatim but for
// the string building) and checks the port draws the same face for the same name and machine.
import test from "node:test";
import assert from "node:assert/strict";
import { agentFaceParts, agentHue, agentSeed, tileLightness } from "../src/lib/agent-face.ts";

// ---- tmx Python, static/index.html ----
function agentVisualSeed(a) { let n=2166136261; for(const c of a.name+'@'+a.host) n=Math.imul(n^c.charCodeAt(0),16777619); return n>>>0; }
function workspaceAgentHue(a) { return [218,272,326,8,32,154,180,200][agentVisualSeed(a)%8]; }
function pythonFace(a) {
  const n=agentVisualSeed(a), wide=(n>>>4)%2, x=wide?4:5, w=wide?16:14;
  const crown=[ 'M11 3h2v4h-2zM10 2h4v2h-4z', 'M6 4h3v4H6zM15 4h3v4h-3z', 'M8 4h8v3H8zM10 2h4v2h-4z', 'M5 5h3v3H5zM16 3h3v5h-3z' ][(n>>>6)%4];
  const eyes=[ 'M7 11h3v3H7zM14 11h3v3h-3z', 'M7 11h3v2H7zM14 11h3v2h-3zM8 10h2v1H8zM14 10h2v1h-2z', 'M7 10h3v4H7zM14 10h3v4h-3z', 'M7 11h3v3H7zM14 12h3v2h-3z' ][(n>>>8)%4];
  const mouth=[ 'M9 16h2v1h2v-1h2v2H9z', 'M10 16h4v2h-4z', 'M9 16h6v1H9z' ][(n>>>10)%3];
  const hue = workspaceAgentHue(a);
  return { hue, tile: hue>25&&hue<195?29:40, crown, head: `M${x+2} 6h${w-4}v1h2v2h2v10h-2v2H${x+2}v-2H${x}V9h2z`, ears: 'M2 11h2v6H2zM20 11h2v6h-2z', eyes, mouth, cheeks: 'M6 15h2v1H6zM16 15h2v1h-2z' };
}
// ---- end of Python's code ----

const AGENTS = [
  ["architect", "hosta"], ["tmx-lead", "hosta"], ["go-ui-desktop-f", "hostc"], ["go-merge-f", "hostc"], ["go-ui-phone-f", "hostc"],
  ["bb-coord", "hostc"], ["head", "hosta-go"], ["a", "b"], ["Zoë", "hostb"], ["worker-1", "studio"], ["worker-2", "studio"], ["", ""],
];

test("the same face as Python for the same name and machine", () => {
  for (const [name, host] of AGENTS) {
    assert.equal(agentSeed(name, host), agentVisualSeed({ name, host }), `${name}@${host} seed`);
    assert.deepEqual(agentFaceParts(name, host), pythonFace({ name, host }), `${name}@${host}`);
  }
});

test("the seed is an unsigned 32-bit number, and the machine is part of it", () => {
  for (const [name, host] of AGENTS) {
    const n = agentSeed(name, host);
    assert.ok(Number.isInteger(n) && n >= 0 && n <= 0xffffffff, String(n));
  }
  assert.notEqual(agentSeed("worker-1", "hosta"), agentSeed("worker-1", "hostc"), "the same name on another machine is another agent");
  assert.equal(agentSeed("", ""), agentVisualSeed({ name: "", host: "" }));
});

test("eight tile colours; the yellow-green ones are drawn darker", () => {
  const hues = new Set();
  for (let i = 0; i < 8; i++) hues.add(agentHue(i));
  assert.deepEqual([...hues], [218, 272, 326, 8, 32, 154, 180, 200]);
  assert.equal(tileLightness(32), 29);
  assert.equal(tileLightness(154), 29);
  assert.equal(tileLightness(180), 29);
  assert.equal(tileLightness(8), 40);
  assert.equal(tileLightness(200), 40);
  assert.equal(tileLightness(218), 40);
});

test("different agents mostly get different faces", () => {
  const faces = new Set(AGENTS.filter(([n]) => n).map(([name, host]) => JSON.stringify(agentFaceParts(name, host))));
  assert.ok(faces.size >= 9, `only ${faces.size} distinct faces among 11 agents`);
});
