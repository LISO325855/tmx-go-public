import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

// Structural regression guard; actual scrolling still requires browser verification.
test('recording detail wrapper constrains its PageBody scroll region', () => {
  const source = readFileSync(new URL('../src/pages/recordings.tsx', import.meta.url), 'utf8');
  const wrapper = source.match(/<div className=\{cn\("([^"]+)", !id && "hidden lg:block"\)\}>\s*\{rec \?/);
  assert.ok(wrapper, 'recording detail wrapper not found');
  const classes = new Set(wrapper[1].split(/\s+/));
  for (const required of ['flex', 'min-h-0', 'min-w-0', 'flex-1', 'flex-col']) {
    assert.ok(classes.has(required), `recording detail wrapper missing ${required}`);
  }
});

// The detail sits next to the list instead of centred in the remaining width (cn drops
// PageBody's mx-auto for the later mx-0).
test('recording detail is aligned to the list', () => {
  const source = readFileSync(new URL('../src/pages/recordings.tsx', import.meta.url), 'utf8');
  assert.match(source, /<PageBody width="max-w-\[55rem\]" className="mx-0 space-y-5">/);
  const page = readFileSync(new URL('../src/components/page.tsx', import.meta.url), 'utf8');
  assert.match(page, /cn\("mx-auto [^"]*", width, className\)/, 'PageBody must merge className after mx-auto');
});
