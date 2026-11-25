import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { pendingChanges, savedState, stagedWith } from '../src/lib/rule-changes.ts';

const rule = (id, outcome, state, extra = {}) => ({ id, name: id, outcome, state, ...extra });

test('a click chooses; choosing the saved state takes the rule out of the batch', () => {
  const disk = rule('disk', 'block', 'block');
  const sudo = rule('sudo', 'hold', 'hold');
  let staged = {};
  staged = stagedWith(staged, disk, 'hold').staged;
  staged = stagedWith(staged, sudo, 'allow').staged;
  assert.deepEqual(staged, { disk: 'hold', sudo: 'allow' });
  staged = stagedWith(staged, disk, 'block').staged; // back to what is saved
  assert.deepEqual(staged, { sudo: 'allow' });
});

test('the batch holds only what still differs from the daemon', () => {
  const rules = [rule('disk', 'block', 'block'), rule('sudo', 'hold', 'allow'), rule('fixed', 'block', 'block', { switchable: false }), rule('pipe', 'hold', undefined, { default: 'allow' })];
  assert.equal(savedState(rules[3]), 'allow', 'no state: the default');
  const pending = pendingChanges({ disk: 'hold', sudo: 'allow', fixed: 'allow', pipe: 'hold', gone: 'allow' }, rules);
  assert.deepEqual(
    pending.map((p) => [p.rule.id, p.next]),
    [['disk', 'hold'], ['pipe', 'hold']],
    'sudo is already allowed, a fixed rule has no switch, an unknown rule is ignored',
  );
});

test('the Safety page saves the choices together, with one request', () => {
  const page = readFileSync(new URL('../src/components/safety-settings.tsx', import.meta.url), 'utf8');
  assert.match(page, /api\.putRuleStates\(/, 'one batch request');
  assert.doesNotMatch(page, /api\.putRuleState\(/, 'no request per rule: that was one PIN prompt per rule');
  assert.match(page, /onClick=\{\(\) => stageRule\(r, st\)\}/, 'a switch only chooses');
  const api = readFileSync(new URL('../src/lib/api.ts', import.meta.url), 'utf8');
  assert.match(api, /withConfirmation\("\/api\/safety\/guard\/rules", c\), \{ states \}/);
});
