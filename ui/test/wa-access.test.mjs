import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { accessList, accessProfiles, isPrivate, otherProfiles, profileLabel, readsAnyway, withAccessChat, withAccessList, withPrivate } from '../src/lib/wa-access.ts';

const base = { read_receipts: false, agents_send: 'ask', allowed_chats: ['bo@s.whatsapp.net'], notify_head_chats: ['fam@g.us'] };

test('the profiles that may use WhatsApp: the head unless the owner set others', () => {
  assert.deepEqual(accessProfiles(base), ['head']);
  assert.deepEqual(accessProfiles({ ...base, agent_profiles: [] }), ['head']);
  assert.deepEqual(accessProfiles({ ...base, agent_profiles: ['head', 'lead'] }), ['head', 'lead']);
  assert.deepEqual(otherProfiles(base), [], 'the head alone: no other role to open chats to');
  assert.deepEqual(otherProfiles({ ...base, agent_profiles: ['head', 'lead'] }), ['lead']);
  assert.equal(profileLabel('head'), 'The head');
  assert.equal(profileLabel('lead'), 'Lead agents');
});

test('the head reads every chat except the ones marked private', () => {
  assert.equal(isPrivate(base, 'anna@s.whatsapp.net'), false, 'nothing is private by default');
  let s = withPrivate(base, 'anna@s.whatsapp.net', true);
  s = withPrivate(s, 'anna@s.whatsapp.net', true); // twice: once
  assert.deepEqual(s.private_chats, ['anna@s.whatsapp.net']);
  assert.equal(isPrivate(s, 'anna@s.whatsapp.net'), true);
  assert.equal(base.private_chats, undefined, 'the settings given are not changed');
  s = withPrivate(s, 'anna@s.whatsapp.net', false);
  assert.deepEqual(s.private_chats, []);
});

test('opening and closing a chat for another role', () => {
  let s = withAccessChat(base, 'lead', 'read', 'anna@s.whatsapp.net', true);
  s = withAccessChat(s, 'lead', 'read', 'anna@s.whatsapp.net', true);
  s = withAccessChat(s, 'lead', 'send', 'carl@s.whatsapp.net', true);
  assert.deepEqual(s.chat_access, { lead: { read: ['anna@s.whatsapp.net'], send: ['carl@s.whatsapp.net'] } });
  assert.deepEqual(accessList(s, 'lead', 'read'), ['anna@s.whatsapp.net']);
  assert.deepEqual(accessList(s, 'worker', 'read'), [], 'another profile has nothing');
  assert.equal(base.chat_access, undefined, 'the settings given are not changed');

  s = withAccessChat(s, 'lead', 'read', 'anna@s.whatsapp.net', false);
  assert.deepEqual(s.chat_access, { lead: { read: [], send: ['carl@s.whatsapp.net'] } });
  s = withAccessList(s, 'lead', 'send', []);
  assert.deepEqual(s.chat_access, {}, 'a profile left with no chats drops out of the table');
});

test('a chat agents write to without asking is readable by the other roles', () => {
  assert.match(readsAnyway(base, 'lead', 'bo@s.whatsapp.net'), /without asking/);
  assert.equal(readsAnyway(base, 'lead', 'fam@g.us'), '', 'what the head is told about is not open to others');
  assert.equal(readsAnyway(base, 'lead', 'anna@s.whatsapp.net'), '');
  assert.equal(readsAnyway(base, 'head', 'bo@s.whatsapp.net'), '', 'the head is governed by private chats, not by lists');
});

test('the Settings page and a chat menu offer private chats and the access per other role', () => {
  const settings = readFileSync(new URL('../src/components/whatsapp/wa-settings.tsx', import.meta.url), 'utf8');
  assert.match(settings, /label="Private chats"/);
  assert.match(settings, /save\(\{ private_chats: v \}\)/);
  assert.match(settings, /otherProfiles\(settings\)\.map/);
  assert.match(settings, /withAccessList\(settings, p, "read", v\)/);
  assert.match(settings, /withAccessList\(settings, p, "send", v\)/);
  const page = readFileSync(new URL('../src/pages/whatsapp.tsx', import.meta.url), 'utf8');
  assert.match(page, /withPrivate\(set, chat\.jid, !!v\)/);
  assert.match(page, /Private: the head may not read this chat/);
  assert.match(page, /withAccessChat\(set, profile, "read", chat\.jid, on\)/);
});
