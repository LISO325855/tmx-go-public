import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

// Structural guards; the browser shows the real thing.
test('mail delivery is a badge in the agent header, not a block above the conversation', () => {
  const page = readFileSync(new URL('../src/pages/agent.tsx', import.meta.url), 'utf8');
  assert.equal(page.split('<MailReceipts').length - 1, 1, 'mounted exactly once');
  const tabsRow = page.indexOf('</TabsList>');
  const mount = page.indexOf('<MailReceipts');
  const body = page.indexOf('{current === "conversation"');
  assert.ok(tabsRow > 0 && tabsRow < mount && mount < page.indexOf('<AgentResourceBanner'), 'the badge sits in the tabs row');
  assert.ok(mount < body);

  const badge = readFileSync(new URL('../src/components/mail-receipts.tsx', import.meta.url), 'utf8');
  assert.doesNotMatch(badge, /<details/, 'no always-present block');
  assert.match(badge, /if \(!recent\.length\) return null;/, 'nothing without receipts');
  assert.match(badge, /<DropdownMenuTrigger[\s\S]*aria-label=\{`Mail delivery: \$\{latest\}`\}/, 'the badge names the latest state');
  assert.match(badge, /aria-label="Recent mail delivery receipts"/, 'the details list is kept');
});
