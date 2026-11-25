import test from "node:test";
import assert from "node:assert/strict";
import {
  connectedSummary,
  fullestLimit,
  harnessMark,
  isLink,
  limitTone,
  normalizeAccounts,
  resetLabel,
  toneWords,
  untilWords,
  usedPct,
  fableNeedsYou,
  freeResetRows,
} from "../src/lib/accounts-format.ts";

test("unverified reset needs owner attention even after usage drops", () => {
  assert.equal(fableNeedsYou({ harness: "claude", connected: true, limits: [], reset: { state: "needs_you" } }), true);
  assert.equal(fableNeedsYou({ harness: "claude", connected: true, limits: [], reset: { state: "succeeded" } }), false);
});

const acc = (over) => ({
  harness: "claude",
  label: "Claude",
  installed: true,
  connected: true,
  account: null,
  limits: [],
  limits_note: "",
  checked_at: 0,
  refreshing: false,
  error: "",
  login: { state: "idle", message: "" },
  install: "",
  ...over,
});

test("Fable needs you starts at exactly 95%, on connected Claude accounts", () => {
  const a = acc({ limits: [{ id: "week_fable", label: "Current week (Fable)", used_percent: 94.9 }] });
  assert.equal(fableNeedsYou(a), false);
  a.limits[0].used_percent = 95;
  assert.equal(fableNeedsYou(a), true);
  assert.equal(fableNeedsYou({ ...a, connected: false }), false);
  assert.equal(fableNeedsYou({ ...a, harness: "codex" }), false);
  assert.equal(fableNeedsYou(acc({ limits: [{ id: "week", label: "Current week", used_percent: 100 }] })), false);
});

test("free resets keep types and expiry separate; unknown is not none", () => {
  const now = Date.UTC(2026, 10, 18);
  const a = acc({ free_resets: { known: true, checked_at: now, grants: [
    { type: "fable", remaining: 2, expires_at: now + 86400000, usable_now: true },
    { type: "weekly", remaining: 1, expires_at: now + 172800000, usable_now: false },
    { type: "weekly", remaining: 5, expires_at: now - 1, usable_now: true },
  ] } });
  const rows = freeResetRows(a, now);
  assert.match(rows[0].detail, /^2 free remaining · expires /);
  assert.match(rows[1].detail, /^1 free remaining · expires .*not usable yet$/);
  a.free_resets.grants = [];
  assert.equal(freeResetRows(a, now)[0].detail, "None offered (confirmed by an offer read)");
  assert.match(freeResetRows(a, now + 16 * 60000)[0].detail, /fresh usage check/);
  a.free_resets.known = false;
  assert.notEqual(freeResetRows(a, now)[0].detail, "None offered");
});

test("quota tones: green under 70, amber from 70, red from 90, with words for each", () => {
  assert.equal(limitTone(0), "ok");
  assert.equal(limitTone(69.4), "ok");
  assert.equal(limitTone(70), "warn");
  assert.equal(limitTone(89), "warn");
  assert.equal(limitTone(90), "hot");
  assert.equal(limitTone(140), "hot");
  assert.equal(toneWords(50), "");
  assert.equal(toneWords(72), "Getting close");
  assert.equal(toneWords(91), "Almost used up");
  assert.equal(toneWords(100), "Used up");
  assert.equal(usedPct(-3), 0);
  assert.equal(usedPct(100.4), 100);
  assert.equal(usedPct(Number.NaN), 0);
});

test("resets: a countdown from resets_at, the harness's words when it is 0", () => {
  const now = 1_000_000_000_000;
  assert.equal(resetLabel({ resets_at: now + 3 * 3600_000 + 12 * 60_000, resets_text: "" }, now), "Resets in 3h 12m");
  assert.equal(resetLabel({ resets_at: now + 47 * 60_000, resets_text: "ignored" }, now), "Resets in 47m");
  assert.equal(resetLabel({ resets_at: 0, resets_text: "Nov 3 at 6am" }, now), "Resets Nov 3 at 6am");
  assert.equal(resetLabel({ resets_at: 0, resets_text: "" }, now), "");
  assert.equal(resetLabel({ resets_at: now - 1, resets_text: "" }, now), "Resets any moment");
  assert.equal(untilWords(30_000), "under a minute");
  assert.equal(untilWords(2 * 86400_000 + 5 * 3600_000), "2d 5h");
  assert.equal(untilWords(2 * 3600_000), "2h");
});

test("the summary counts connected accounts and says when nothing is installed", () => {
  assert.equal(connectedSummary([acc({}), acc({ harness: "codex" }), acc({ harness: "antigravity", connected: false })]), "2 of 3 AI accounts connected");
  assert.equal(connectedSummary([acc({ installed: false, connected: false })]), "No AI tools installed yet");
  assert.equal(connectedSummary([]), "No AI tools found");
  assert.equal(connectedSummary([acc({})]), "1 of 1 AI account connected");
});

test("the fullest limit counts connected accounts only", () => {
  const l = (id, p) => ({ id, label: id, used_percent: p, resets_at: 0, resets_text: "" });
  const list = [
    acc({ limits: [l("session", 8), l("week", 54)] }),
    acc({ harness: "codex", limits: [l("5h", 91)] }),
    acc({ harness: "antigravity", connected: false, limits: [l("stale", 99)] }),
  ];
  assert.equal(fullestLimit(list)?.limit.id, "5h");
  assert.equal(fullestLimit([]), null);
});

test("marks, links, and partial payloads", () => {
  assert.equal(harnessMark("claude"), "Cl");
  assert.equal(harnessMark("antigravity"), "Ag");
  assert.equal(harnessMark("opencode", "opencode"), "Op");
  assert.ok(isLink("https://antigravity.google/download"));
  assert.ok(!isLink("curl -fsSL https://claude.ai/install.sh | bash"));
  const p = normalizeAccounts({ accounts: [{ harness: "codex", label: "Codex", installed: true, connected: false, checked_at: 0, refreshing: false, limits: null }] });
  assert.deepEqual(p.accounts[0].limits, []);
  assert.deepEqual(p.accounts[0].login, { state: "idle", message: "" });
  assert.equal(p.updated_at, 0);
});

test('every account card has Remove; a default account is hidden and can be shown again', async () => {
  const { removeExplanation, normalizeAccounts } = await import('../src/lib/accounts-format.ts');
  assert.match(removeExplanation({ name: 'default', label: 'Antigravity' }), /own sign-in on this machine stays/);
  assert.match(removeExplanation({ name: '', label: 'Claude' }), /Show again/);
  assert.match(removeExplanation({ name: 'work', label: 'Claude' }), /sign out first/);
  assert.deepEqual(normalizeAccounts({ accounts: [] }).hidden, [], 'no hidden list from an older daemon');
  assert.deepEqual(normalizeAccounts({ accounts: [], hidden: [{ harness: 'antigravity', label: 'Antigravity' }] }).hidden, [{ harness: 'antigravity', label: 'Antigravity' }]);
  const { readFileSync } = await import('node:fs');
  const card = readFileSync(new URL('../src/components/accounts/account-card.tsx', import.meta.url), 'utf8');
  assert.doesNotMatch(card, /a\.name !== DEFAULT_ACCOUNT && \(\s*<Button[^>]*onClick=\{\(\) => setAskRemove\(true\)\}/, 'Remove is not limited to named accounts');
  assert.match(card, /onClick=\{\(\) => setAskRemove\(true\)\}/);
  const page = readFileSync(new URL('../src/pages/usage.tsx', import.meta.url), 'utf8');
  assert.match(page, /accountsApi\.add\(h\.harness, DEFAULT_ACCOUNT\)/, '"Show again" adds the default account back');
});


test("account normalization preserves the sign-out ownership explanation", () => {
  const reason = "This node does not own this account";
  const payload = normalizeAccounts({ accounts: [acc({ sign_out_reason: reason })] });
  assert.equal(payload.accounts[0].sign_out_reason, reason);
});
