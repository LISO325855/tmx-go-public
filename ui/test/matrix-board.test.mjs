// The Board view of the Matrix (src/lib/matrix-board.ts; the owner's most repeated wish, 17 Nov:
// "prosthetics for my brain"): one row per thread, grouped Needs you -> Blocked -> In progress
// -> Done today, with a 0-100 progress bar, the blocker and what the owner has to do.
import test from "node:test";
import assert from "node:assert/strict";
import { BOARD_GROUPS, boardGroupOf, boardProgress, boardSections, doneEarlier, lastMoved, progressText, startOfDay } from "../src/lib/matrix-board.ts";

const NOW = new Date(2026, 10, 17, 20, 0, 0).getTime(); // 17 Nov 2026, 20:00 local
const today = (h) => new Date(2026, 10, 17, h, 0, 0).getTime();
const yesterday = new Date(2026, 10, 16, 23, 30, 0).getTime();
const card = (id, extra = {}, last) => ({ thread: { id, title: id, updated_at: today(9), ...extra }, last_event_at: last });
const none = () => 0;

test("four groups, in the order the owner reads them", () => {
  assert.deepEqual(BOARD_GROUPS.map((g) => g.label), ["Needs you", "Blocked", "In progress", "Done today"]);
});

test("a thread with an open question needs you, whatever its status says", () => {
  assert.equal(boardGroupOf(card("a"), 1, NOW), "needs_you");
  assert.equal(boardGroupOf(card("a", { status: "blocked" }), 2, NOW), "needs_you");
  assert.equal(boardGroupOf(card("a", { status: "waiting_on_owner" }), 0, NOW), "needs_you", "and so does one that says it waits for him");
});

test("blocked, in progress, and a paused thread is in progress, not blocked", () => {
  assert.equal(boardGroupOf(card("a", { status: "blocked" }), 0, NOW), "blocked");
  assert.equal(boardGroupOf(card("a"), 0, NOW), "in_progress", "no status = active");
  assert.equal(boardGroupOf(card("a", { status: "active" }), 0, NOW), "in_progress");
  assert.equal(boardGroupOf(card("a", { status: "paused" }), 0, NOW), "in_progress");
});

test("done today stays on the board; done before today and archived threads leave it", () => {
  assert.equal(boardGroupOf(card("a", { status: "done", updated_at: today(8) }), 0, NOW), "done_today");
  assert.equal(boardGroupOf(card("a", { status: "done", updated_at: yesterday }), 0, NOW), null);
  assert.equal(boardGroupOf(card("a", { status: "done", updated_at: yesterday }, today(1)), 0, NOW), "done_today", "its last event counts");
  assert.equal(boardGroupOf(card("a", { archived: true }), 3, NOW), null);
  assert.equal(boardGroupOf(card("a", { status: "done", updated_at: today(8) }), 1, NOW), "done_today", "a done thread is done, even with a stray question");
  assert.equal(startOfDay(NOW), new Date(2026, 10, 17, 0, 0, 0).getTime());
});

test("progress is a whole number from 0 to 100, or nothing when none was reported", () => {
  assert.equal(boardProgress({ progress: 60 }), 60);
  assert.equal(boardProgress({ progress: 0 }), 0, "0% is a report, not nothing");
  assert.equal(boardProgress({ progress: 59.6 }), 60);
  assert.equal(boardProgress({ progress: 140 }), 100);
  for (const p of [undefined, null, -1, NaN, Infinity]) assert.equal(boardProgress({ progress: p }), null, String(p));
  assert.equal(boardProgress({ progress: 20, status: "done" }), 100, "done is done");
  assert.equal(boardProgress({ status: "done" }), 100);
});

test("the words under the bar", () => {
  assert.equal(progressText(60, "tests pass, docs left", "active"), "60% · tests pass, docs left");
  assert.equal(progressText(60, "", "active"), "60%");
  assert.equal(progressText(null, "ignored", undefined), "Not reported");
  assert.equal(progressText(100, "x", "done"), "Done");
});

test("the board: groups in order, empty groups left out, every thread at most once", () => {
  const cards = [
    card("writing"),
    card("stuck", { status: "blocked", blocker: "waiting for the SIM card" }),
    card("asks", { status: "active" }),
    card("shipped", { status: "done", updated_at: today(15) }),
    card("old", { status: "done", updated_at: yesterday }),
    card("gone", { archived: true }),
  ];
  const sections = boardSections(cards, (id) => (id === "asks" ? 1 : 0), NOW);
  assert.deepEqual(sections.map((s) => [s.label, s.rows.map((r) => r.card.thread.id)]), [
    ["Needs you", ["asks"]],
    ["Blocked", ["stuck"]],
    ["In progress", ["writing"]],
    ["Done today", ["shipped"]],
  ]);
  assert.equal(sections[0].rows[0].asks, 1);
  assert.equal(doneEarlier(cards, NOW), 1);
  assert.deepEqual(boardSections([card("a")], none, NOW).map((s) => s.label), ["In progress"], "no empty groups");
  assert.deepEqual(boardSections([], none, NOW), []);
});

test("inside a group: the owner's urgency order first, then the furthest along, then what moved last", () => {
  const cards = [
    card("quiet", { updated_at: today(8) }),
    card("fresh", { updated_at: today(19) }),
    card("half", { progress: 50, updated_at: today(5) }),
    card("nearly", { progress: 90, updated_at: today(4) }),
    card("started", { progress: 0, updated_at: today(3) }),
    card("second", { position: 2, updated_at: today(7) }),
    card("first", { position: 1, progress: 5, updated_at: today(6) }),
  ];
  assert.deepEqual(boardSections(cards, none, NOW)[0].rows.map((r) => r.card.thread.id), ["first", "second", "nearly", "half", "started", "fresh", "quiet"]);
  // done today: simply the most recent first, whatever the urgency order was
  const done = [card("a", { status: "done", position: 1, updated_at: today(9) }), card("b", { status: "done", updated_at: today(18) })];
  assert.deepEqual(boardSections(done, none, NOW)[0].rows.map((r) => r.card.thread.id), ["b", "a"]);
  assert.deepEqual(cards.map((c) => c.thread.id), ["quiet", "fresh", "half", "nearly", "started", "second", "first"], "the caller's list is not reordered");
});

test("when a thread last moved", () => {
  assert.equal(lastMoved(card("a", { updated_at: 5 }, 9)), 9);
  assert.equal(lastMoved(card("a", { updated_at: 5 })), 5);
});

// The screen (components/matrix/thread-board.tsx) and where it sits (pages/matrix.tsx).
test("under classic the Board is the Matrix's default screen; the default dashboard has no Board", async () => {
  const { readFileSync } = await import("node:fs");
  const read = (p) => readFileSync(new URL(`../src/${p}`, import.meta.url), "utf8");
  const page = read("pages/matrix.tsx");
  assert.match(page, /return saved === "board" \|\| saved === "canvas" \|\| saved === "list" \|\| saved === "grid" \? saved : "board";/, "classic starts on the Board, and remembers another choice");
  assert.match(page, /const look: Look = classic \? classicLook : plainLook;/);
  assert.match(page, /\(classic \? \["board", "canvas", "list", "grid"\] : \["canvas", "list", "grid"\]\) as Look\[\]/);
  assert.match(page, /look === "board" \? \(\s*<ThreadBoard cards=\{shown\} questions=\{questions\} open=\{open\} onOpen=\{setOpen\} now=\{boardNow\} onChanged=\{\(\) => void reload\(\)\} \/>/);
  const board = read("components/matrix/thread-board.tsx");
  assert.match(board, /boardSections\(ranked, \(id\) => questions\.get\(id\)\?\.length \?\? 0, now\)/);
  assert.match(board, /<ApprovalActions a=\{q\} \/>/, "the Your-turn card is answered on the row");
  assert.match(board, /s\.group === "needs_you" \? "You: " : s\.group === "blocked" \? "Blocked: " : ""/);
  assert.match(board, /onClick=\{\(\) => onOpen\(t\.id\)\}/, "the title opens the thread");
  assert.doesNotMatch(board, /#[0-9a-fA-F]{3,8}\b/, "theme colours only");
});

// ---- the interactive Board (the owner, 17 Nov 20:58) ----
test("his order: placed threads first in his order, then the rest as the Board shows them", async () => {
  const { boardOrder, boardSections } = await import("../src/lib/matrix-board.ts");
  const now = Date.UTC(2026, 10, 17, 12);
  const card = (id, x = {}) => ({ thread: { id, title: id, created_at: 1, updated_at: now - 1000, ...x } });
  const cards = [card("c"), card("a", { position: 2 }), card("blocked", { status: "blocked" }), card("b", { position: 1 }), card("old-done", { status: "done", updated_at: 5 })];
  const order = boardOrder(cards, boardSections(cards, () => 0, now));
  assert.deepEqual(order, ["b", "a", "blocked", "c", "old-done"], "placed first; then group by group; then what is not on the Board");
  assert.equal(new Set(order).size, cards.length, "every thread once");
});

test("moving a row keeps the other groups' rows where they are", async () => {
  const { movedOrder } = await import("../src/lib/matrix-board.ts");
  const order = ["a", "x", "b", "y", "c"]; // a, b, c are one group; x, y another
  const group = ["a", "b", "c"];
  assert.deepEqual(movedOrder(order, group, "b", "up"), ["b", "x", "a", "y", "c"]);
  assert.deepEqual(movedOrder(order, group, "b", "down"), ["a", "x", "c", "y", "b"]);
  assert.deepEqual(movedOrder(order, group, "c", "a"), ["c", "x", "a", "y", "b"], "dropped on the first row: it takes its place");
  assert.deepEqual(movedOrder(order, group, "a", "c"), ["b", "x", "c", "y", "a"]);
  assert.equal(movedOrder(order, group, "a", "up"), null, "the first row has nowhere to go up");
  assert.equal(movedOrder(order, group, "c", "down"), null);
  assert.equal(movedOrder(order, group, "a", "a"), null);
  assert.equal(movedOrder(order, group, "a", "x"), null, "not onto another group's row");
  assert.equal(movedOrder(order, group, "zz", "up"), null);
  assert.deepEqual(order, ["a", "x", "b", "y", "c"], "the given order is not changed");
});

test("the one-tap actions and what is said after", async () => {
  const { OWNER_ACTS, ownerActSaid } = await import("../src/lib/matrix-board.ts");
  assert.deepEqual(OWNER_ACTS.map((a) => a.act), ["done", "thanks", "park", "not_relevant"]);
  assert.deepEqual(OWNER_ACTS.map((a) => a.label), ["Done", "Thanks", "Park", "Not relevant"]);
  assert.equal(ownerActSaid("park", "billing-lead"), "Parked · billing-lead was told");
  assert.equal(ownerActSaid("note"), "Note saved · on the thread (no agent to tell)");
});

test("the Board's row: acts, a note, a handle; saved through the daemon", async () => {
  const { readFileSync } = await import("node:fs");
  const read = (p) => readFileSync(new URL(`../src/${p}`, import.meta.url), "utf8");
  const board = read("components/matrix/thread-board.tsx");
  assert.match(board, /await matrixApi\.ownerAct\(id, \{ act: what, note \}\)/);
  assert.match(board, /await matrixApi\.setOrder\(next\)/);
  assert.match(board, /movedOrder\(boardOrder\(ranked, sections\), groupIds, id, to\)/);
  assert.match(board, /draggable=\{sortable\}/, "drag on a computer");
  assert.match(board, /aria-label=\{`Move "\$\{t\.title\}" up`\}/, "and a handle that works on a phone");
  assert.match(board, /res\.mail_error/, "says so when the agent could not be told");
  const api = read("lib/matrix.ts");
  assert.match(api, /"POST", `\/api\/matrix\/threads\/\$\{enc\(id\)\}\/owner`, b/);
  assert.match(api, /"PUT", "\/api\/board\/order", \{ ids \}/);
});

test("Park it and Not relevant can be undone from their toast; the agent is told again", async () => {
  const { UNDO_MS, canUndo, undoNote } = await import("../src/lib/matrix-board.ts");
  assert.equal(UNDO_MS, 5000);
  assert.deepEqual(["note", "park", "not_relevant", "done", "thanks"].filter(canUndo), ["park", "not_relevant"]);
  assert.match(undoNote("park"), /^Undo: ignore my "Park it"\. Carry on\.$/);
  assert.match(undoNote("not_relevant"), /Not relevant/);
  const { readFileSync } = await import("node:fs");
  const board = readFileSync(new URL("../src/components/matrix/thread-board.tsx", import.meta.url), "utf8");
  assert.match(board, /canUndo\(what\) \? \{ duration: UNDO_MS, action: \{ label: "Undo", onClick: \(\) => void undo\(id, what, before\) \} \} : undefined/);
  assert.match(board, /await boardApi\.patchThread\(id, \{ archived: false \}\)/, "Not relevant: back on the Board");
  assert.match(board, /await matrixApi\.setState\(id, \{ status: before,/, "Park it: the status it had");
  assert.match(board, /await matrixApi\.ownerAct\(id, \{ act: "note", note: undoNote\(what\) \}\)/);
});
