import test from 'node:test';
import assert from 'node:assert/strict';
import { clearSubmittedDraft } from '../src/lib/composer-draft.ts';

test('successful send clears the submitted draft', () => {
  const drafts = new Map([['head', 'spoken words']]);
  assert.equal(clearSubmittedDraft(drafts, 'head', 'spoken words'), true);
  assert.equal(drafts.has('head'), false);
});

test('typing or transcription arriving while send waits remains in the next draft', () => {
  for (const next of ['typed words with late transcript', 'replacement typed words']) {
    const drafts = new Map([['head', next], ['other', 'another draft']]);
    assert.equal(clearSubmittedDraft(drafts, 'head', 'typed words'), false);
    assert.equal(drafts.get('head'), next);
    assert.equal(drafts.get('other'), 'another draft');
  }
});
