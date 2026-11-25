// Promises under the classic UI profile (the owner, 17 Nov 18:55: "It's completely replaced by
// matrix… promises is basically useless… It can be removed from that main menu on the side").
// Promises is not a place any more: no sidebar entry, no tab in the Matrix, no form to add one.
// The records are NOT deleted: they stay readable behind a small "Old promises (N)" link at the
// bottom of the Matrix, until he decides. The default dashboard keeps its Promises page.
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const src = (p) => readFileSync(new URL(`../src/${p}`, import.meta.url), "utf8");

test("classic: no Promises entry in the sidebar, nor on the phone's Home", () => {
  assert.match(src("components/sidebar.tsx"), /\{!classic && <NavItem to=\{paths\.promises\(\)\} icon=\{Handshake\} label="Promises"/);
  const rows = src("lib/phone.ts");
  assert.doesNotMatch(rows.slice(rows.indexOf("export const PHONE_HOME_ROWS")), /label: "Promises"/);
});

test("the Matrix is threads only: no Threads | Promises switch, no promise form", () => {
  const page = src("pages/matrix.tsx");
  assert.doesNotMatch(page, /data-matrix-views|MATRIX_VIEWS|PromisesBody|view === "promises"/);
  assert.match(page, /export function MatrixPage\(\) \{/, "no view to choose");
  assert.doesNotMatch(src("lib/matrix.ts"), /MatrixView|MATRIX_VIEWS/);
  assert.doesNotMatch(page, /NewPromise|addPromise/);
});

test("the old records stay reachable: a small link at the bottom of the Matrix, only when there are any", () => {
  const page = src("pages/matrix.tsx");
  assert.match(page, /\{classic && oldPromises > 0 && \(/);
  assert.match(page, /Old promises \(\{oldPromises\}\)/);
  assert.match(page, /href=\{href\(paths\.promises\(\)\)\}/);
  assert.match(page, /if \(!classic\) return;\s*let live = true;\s*peopleApi\.promises\(\)/, "counted from the daemon's records, under classic only");
  assert.ok(page.indexOf("data-old-promises-link") > page.indexOf('<div className="relative flex min-h-0 flex-1">'), "under the threads");
});

test("classic #/promises shows the old records read-only; the default dashboard keeps its page", () => {
  assert.match(src("App.tsx"), /case "promises":[\s\S]{0,300}return classic \? <OldPromisesPage \/> : <PromisesPage \/>;/);
  const people = src("pages/people.tsx");
  const old = people.slice(people.indexOf("export function OldPromisesPage"), people.indexOf("function PromisesContent"));
  assert.match(old, /<Shell title="Old promises">/);
  assert.match(old, /<PromiseList list=\{list\} people=\{people\} onChange=\{\(\) => \{\}\} readOnly \/>/);
  assert.match(old, /<Reminders readOnly \/>/);
  assert.doesNotMatch(old, /NewPromise|addPromise|setPromise|addReminder/, "nothing can be added or changed there");
  // the default page still has everything
  const content = people.slice(people.indexOf("function PromisesContent"));
  assert.match(content, /<NewPromise people=\{people\} onAdded=\{load\} \/>/);
  assert.match(content, /<PromiseList list=\{list\} people=\{people\} onChange=\{load\} \/>\s*<Reminders \/>/);
});

test("read-only means no Done / Drop / Reopen, and no reminder form; a pending reminder can still be cancelled", () => {
  const people = src("pages/people.tsx");
  const list = people.slice(people.indexOf("function PromiseList"), people.indexOf("export function PromisesPage"));
  assert.match(list, /\{readOnly \? \(\s*p\.status !== "open" && <div className="text-muted-foreground">\{p\.status\}<\/div>\s*\) : p\.status === "open" \? \(/, "the buttons give way to the status in words");
  const rem = people.slice(people.indexOf("function Reminders"));
  assert.match(rem, /if \(readOnly && !list\?\.length\) return null;/, "nothing at all when no reminder is pending");
  assert.match(rem, /\{!readOnly && <div className="flex flex-col gap-3 rounded-xl border bg-card p-4">/, "no form to set a new one");
  assert.match(rem, /<Btn onClick=\{\(\) => void cancel\(r\.id\)\}>Cancel<\/Btn>/, "so nothing fires out of sight");
});

test("classic: a person's page lists that person's old promises read-only, and has no form to add one", () => {
  const people = src("pages/people.tsx");
  const person = people.slice(people.indexOf("function PersonPromises"), people.indexOf("function NewPromise"));
  const classicPart = person.slice(person.indexOf("if (classic) {"), person.indexOf("return (\n    <section className=\"flex flex-col gap-3 border-t pt-4\">\n      <h2 className=\"text-[20px] font-semibold\">Promises</h2>"));
  assert.ok(classicPart.length > 0, "a classic branch");
  assert.match(classicPart, /if \(!list\?\.length\) return null;/, "no section when there is none");
  assert.match(classicPart, /<PromiseList list=\{list\} people=\{\[person\]\} onChange=\{\(\) => \{\}\} readOnly \/>/);
  assert.doesNotMatch(classicPart, /NewPromise/);
  // so under classic nothing in the UI creates a promise: the only NewPromise uses are the default dashboard's
  assert.equal(people.split("<NewPromise ").length - 1, 2, "the default person page and the default Promises page");
});

test("no promise record is deleted by any of this", () => {
  for (const f of ["pages/matrix.tsx", "pages/people.tsx", "App.tsx", "lib/people.ts"]) {
    assert.doesNotMatch(src(f), /"DELETE", `\/api\/promises|deletePromise|removePromise/, f);
  }
});
