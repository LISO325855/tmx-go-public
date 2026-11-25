import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
const read = p => readFileSync(new URL(`../src/${p}`, import.meta.url), 'utf8');

test('every dictation button has an external status in its composer', () => {
  const button = read('components/dictate-button.tsx');
  assert.doesNotMatch(button, /kept\.error|Your recording is kept|aria-live/, 'status cannot consume action-row width');
  for (const [file, target] of [
    ['components/conversation/composer.tsx', '"agent:" + key'],
    ['pages/chat.tsx', '"chat:" + chat.id'],
    ['pages/companion.tsx', '"companion"'],
    ['components/whatsapp/wa-composer.tsx', '"wa:" + chat.jid'],
    ['components/owner-card-actions.tsx', '`card:${key}:${original.version}`'],
  ]) {
    const source = read(file);
    assert.equal((source.match(/<DictationInline /g) || []).length, 1, file);
    const status = source.indexOf('<DictationInline ');
    assert.ok(source.slice(status, source.indexOf('/>', status)).includes(target), `${file}: same draft target`);
    assert.ok(status < source.indexOf('<DictateButton'), `${file}: status before actions`);
  }
  assert.doesNotMatch(read('components/conversation/conversation.tsx'), /<DictationInline/, 'the parent must not repeat the composer status');
});

test('failed dictation text wraps outside the wrapping action row', () => {
  const inline = read('components/dictation-inline.tsx');
  assert.match(inline, /role="status"[^\n]*overflow-wrap:anywhere/);
  const composer = read('components/conversation/composer.tsx');
  assert.match(composer, /data-composer-actions className="[^"]*min-w-0[^"]*flex-wrap/);
  assert.match(composer, /ml-auto flex min-w-0 flex-wrap items-center justify-end/);
});

test('app toast offsets clear the phone navigation including its safe area', () => {
  const source = read('components/ui/sonner.tsx');
  assert.match(source, /usePhoneClassic\(\)/);
  assert.match(source, /83px \+ max\(9px, env\(safe-area-inset-bottom\)\)/);
  assert.match(source, /offset=\{phoneOffset\}/);
  assert.match(source, /mobileOffset=\{phoneOffset\}/, 'Sonner has a different mobile breakpoint: cover both');
});

test('mic gestures suppress keyboard focus and delivered words never refocus the input', () => {
 const button = readFileSync(new URL('../src/components/dictate-button.tsx', import.meta.url), 'utf8');
 assert.match(button, /onPointerDown=[\s\S]*?e\.preventDefault\(\)/);
 assert.match(button, /onMouseDown=\{\(e\) => e\.preventDefault\(\)\}/);
 assert.match(button, /target\.current\?\.blur\(\)/);
 assert.doesNotMatch(button, /\.focus\(/);
 assert.doesNotMatch(button, /onPointerLeave|onPointerCancel|onBlur|onTouchEnd/);
});
