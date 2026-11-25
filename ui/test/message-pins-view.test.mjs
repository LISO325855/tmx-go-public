import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { pinsStripVisible } from '../src/lib/message-pins-view.ts';

test('the pins strip is absent without pins and present with any pin or a sync error', () => {
  assert.equal(pinsStripVisible(0, ''), false);
  assert.equal(pinsStripVisible(1, ''), true);
  assert.equal(pinsStripVisible(0, 'offline'), true, 'a sync error must stay visible');
});

// Structural guard; the browser e2e (Linux CI) checks the real page.
test('MessagePinsStrip renders nothing when the rule says so, after its hooks', () => {
  const source = readFileSync(new URL('../src/components/message-pins.tsx', import.meta.url), 'utf8');
  const strip = source.slice(source.indexOf('export function MessagePinsStrip'));
  const guard = strip.indexOf('if (!pinsStripVisible(pins.length, error)) return null;');
  assert.ok(guard > 0, 'the strip does not return null for an empty list');
  assert.ok(guard > strip.lastIndexOf('useEffect('), 'the early return must come after every hook');
  assert.ok(guard < strip.indexOf('<section'), 'the early return must come before the markup');
});
