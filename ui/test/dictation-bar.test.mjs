import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

// Structural guard; the browser shows the real thing.
test('the dictation bar is never shown for a transcription that waits to be retried', () => {
  const source = readFileSync(new URL('../src/components/dictation-bar.tsx', import.meta.url), 'utf8');
  const idle = source.indexOf('if (phase === "idle" || !target) return null;');
  const waiting = source.indexOf('if (phase === "working" && pending) return null;');
  assert.ok(idle > 0 && waiting > idle, 'the pending wait returns nothing');
  assert.ok(waiting > source.lastIndexOf('useEffect('), 'after every hook');
  assert.ok(waiting < source.indexOf('data-dictation-bar'), 'before the bar markup');
  assert.doesNotMatch(source.slice(waiting), /Transcription pending/, 'no permanent "pending" label is left in the bar');
});
