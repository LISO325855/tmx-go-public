import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { approvalToastId, shouldToastApproval, staleApprovalToasts } from '../src/lib/approval-toasts.ts';

test('only a new, open, non-imported request gets a toast', () => {
  assert.equal(shouldToastApproval({ state: 'pending' }), true);
  assert.equal(shouldToastApproval({ state: 'pending', card: {} }), true);
  assert.equal(shouldToastApproval({ state: 'pending', deferred: true }), false, 'put aside for later');
  assert.equal(shouldToastApproval({ state: 'expired' }), false, 'already decided');
  assert.equal(shouldToastApproval({ state: 'pending', card: { origin: { source: 'python@hostb', imported_by: 'owner' } } }), false, "the owner's own import");
  assert.equal(shouldToastApproval({ state: 'pending', card: { origin: { source: 'python@hostb', imported_by: 'lead-id' } } }), true, "an agent's imported open card");
  assert.equal(shouldToastApproval({ state: 'expired', card: { origin: { source: 'python@hostb', imported_by: 'lead-id' } } }), false, "an agent's imported history");
});

test('a toast is stale once its card is no longer pending', () => {
  const open = new Set(['a', 'b', 'c']);
  assert.deepEqual(staleApprovalToasts(open, { a: {}, c: {} }), ['b']);
  assert.deepEqual(staleApprovalToasts(open, {}), ['a', 'b', 'c']);
  assert.deepEqual(staleApprovalToasts(new Set(), { a: {} }), []);
  assert.equal(approvalToastId('x1'), 'approval:x1');
});

// Structural guards; the browser shows the real thing.
test('toasts can be closed and closing one never answers the card', () => {
  const app = readFileSync(new URL('../src/App.tsx', import.meta.url), 'utf8');
  assert.match(app, /<Toaster position="bottom-right" closeButton \/>/, 'every toast has a close button');
  const hook = app.slice(app.indexOf('function useApprovalToasts'), app.indexOf('function Boot'));
  assert.match(hook, /shouldToastApproval\(a\)/);
  assert.match(hook, /staleApprovalToasts\(/);
  assert.match(hook, /label: "Dismiss all"/);
  assert.match(hook, /if \(open\.size === 1\) for \(const redraw of open\.values\(\)\) redraw\(true\);/, 'the toast that was alone gets "Dismiss all" when a second one arrives');
  assert.doesNotMatch(hook, /decide|answer|\.request\(|api\./i, 'the toast code must not call the API: dismissing only hides');
});
