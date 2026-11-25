// Resources page (src/lib/toolbox-format.ts): grouping, the one coloured word, what floats to
// the top, and that the page is wired where the owner expects it.
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import {
  countResources,
  countsLine,
  filterResources,
  findResource,
  groupResources,
  holderFull,
  holderShort,
  identityText,
  needsAttention,
  routeText,
  secretText,
  shortDate,
  statusOf,
  verifiedText,
} from "../src/lib/toolbox-format.ts";

const read = (p) => readFileSync(new URL(`../src/${p}`, import.meta.url), "utf8");

const res = (id, kind, state, extra = {}) => ({
  id,
  name: id,
  kind,
  summary: "s",
  identity: { who: "fleet" },
  actions: [],
  rule: { text: "r" },
  state,
  source: "declared",
  ...extra,
});

const list = [
  res("disk", "storage", "not_verified"),
  res("chat", "communication", "works", { health: { level: "good", checked_at: 1 }, source: "live", live: "whatsapp" }),
  res("phone", "communication", "partly", { holder: "phone@box-c", holder_note: "A shell with no model." }),
  res("mail", "communication", "not_verified"),
  res("voice", "communication", "planned"),
  res("sms", "communication", "works", { health: { level: "down", checked_at: 1, reason: "Its holder is stopped." } }),
  res("accounts", "account", "works", { health: { level: "warning", checked_at: 1 }, meter: { label: "Claude", text: "8% left", used_percent: 92 } }),
  res("assistant", "tool", "partly", { machine: "box-b" }),
];

test("grouped by kind in page order; down first, then not verified, then as listed", () => {
  const groups = groupResources(list);
  assert.deepEqual(groups.map((g) => g.label), ["Communication", "Tools", "Accounts", "Storage"]);
  assert.deepEqual(groups[0].resources.map((r) => r.id), ["sms", "mail", "chat", "phone", "voice"]);
  assert.deepEqual(groupResources([]), []);
});

test("one coloured word per line; a failing check wins over the declared state", () => {
  const word = (id) => statusOf(findResource(list, id));
  assert.deepEqual(word("chat"), { word: "Works", tone: "ok" });
  assert.deepEqual(word("sms"), { word: "Down", tone: "down" });
  assert.deepEqual(word("mail"), { word: "Not verified", tone: "unknown" });
  assert.deepEqual(word("voice"), { word: "Planned", tone: "idle" });
  const drive = res("drive", "storage", "not_connected");
  assert.deepEqual(statusOf(drive), { word: "Not connected yet", tone: "idle" });
  assert.equal(needsAttention(drive), false);
  assert.deepEqual(word("phone"), { word: "Partly", tone: "warn" });
  assert.deepEqual(word("accounts"), { word: "Works, with a warning", tone: "warn" });
  // down beats not verified
  assert.equal(statusOf(res("x", "tool", "not_verified", { health: { level: "down", checked_at: 1 } })).word, "Down");
});

test("only down and not verified ask for attention", () => {
  assert.deepEqual(list.filter(needsAttention).map((r) => r.id).sort(), ["disk", "mail", "sms"]);
  const c = countResources(list);
  assert.deepEqual(c, { total: 8, down: 1, notVerified: 2 });
  assert.equal(countsLine(c), "8 resources · 1 down · 2 not verified");
  assert.equal(countsLine({ total: 1, down: 0, notVerified: 0 }), "1 resource");
});

test("who holds it: the holder, else the machine, else the note's first sentence", () => {
  assert.equal(holderShort(findResource(list, "phone")), "phone@box-c");
  assert.equal(holderShort(findResource(list, "assistant")), "box-b");
  assert.equal(holderShort(res("x", "tool", "works", { holder_note: "Each machine. Nothing else." })), "Each machine");
  assert.equal(holderShort(res("x", "tool", "works")), "");
  assert.equal(holderFull(findResource(list, "phone")), "phone@box-c. A shell with no model.");
  assert.equal(holderFull(res("x", "tool", "works")), "");
});

test("routes, dates, where the state comes from", () => {
  assert.equal(routeText({ kind: "tool", tool: "whatsapp_send" }), "tool whatsapp_send");
  assert.equal(routeText({ kind: "mail", to: "phone@box-c" }), "fleet mail to phone@box-c");
  assert.equal(routeText({ kind: "none" }), "no route yet");
  assert.equal(shortDate("2026-11-17"), "17 Nov");
  assert.equal(shortDate("2026-01-05T10:00:00Z"), "5 Jan");
  assert.equal(shortDate("yesterday"), "yesterday");
  assert.equal(verifiedText(findResource(list, "chat")), "Measured by this node.");
  assert.equal(verifiedText(res("x", "tool", "works", { state_note: "Code read.", verified_by: "lead@box-b", verified_at: "2026-11-17" })), "Code read. Checked by lead@box-b on 17 Nov.");
  assert.equal(verifiedText(res("x", "tool", "not_verified")), "Nobody confirmed it.");
  assert.equal(verifiedText(res("x", "tool", "not_verified", { state_note: "From memory" })), "From memory.");
  assert.equal(verifiedText(res("x", "tool", "planned")), "");
});

test("a secret is its name with stored or missing, never a value", () => {
  assert.equal(secretText({ name: "Link token", stored: true }), "Link token: stored");
  assert.equal(secretText({ name: "Link token", stored: false }), "Link token: missing");
  assert.equal(secretText({ name: "Link token" }), "Link token");
});

test("whose face it shows", () => {
  assert.equal(identityText(res("x", "tool", "works", { identity: { who: "none" } })), "");
  assert.equal(identityText(res("x", "tool", "works", { identity: { who: "owner" } })), "The owner");
  assert.equal(identityText(res("x", "tool", "works", { identity: { who: "fleet", label: "The fleet's alias" } })), "The fleet's alias");
});

test("search covers names, holders, rules and the state word", () => {
  const ids = (q) => filterResources(list, q).map((r) => r.id);
  assert.deepEqual(ids(""), list.map((r) => r.id));
  assert.deepEqual(ids("BOX-C"), ["phone"]);
  assert.deepEqual(ids("not verified"), ["disk", "mail"]);
  assert.deepEqual(ids("zzz"), []);
});

test("the page is where the owner expects it: #/resources, directly above Usage", async () => {
  const { parse, paths } = await import("../src/lib/router.ts").catch(() => ({}));
  const router = read("lib/router.ts");
  assert.match(router, /\| \{ name: "resources"; id\?: string \}/);
  assert.match(router, /parts\[0\] === "resources" && parts\.length <= 2/);
  if (parse) {
    assert.deepEqual(parse("/resources"), { name: "resources", id: undefined });
    assert.deepEqual(parse("/resources/fleet-phone"), { name: "resources", id: "fleet-phone" });
    assert.equal(paths.resources("fleet-phone"), "/resources/fleet-phone");
  }
  const side = read("components/sidebar.tsx").split("\n");
  const at = side.findIndex((l) => l.includes("to={paths.resources()}") && l.includes('label="Resources"'));
  assert.ok(at > 0, "the sidebar has a Resources entry");
  assert.ok(side[at + 1].includes("to={paths.usage()}"), "directly above Usage");
  assert.match(read("App.tsx"), /case "resources":\s+return <ResourcesPage id=\{r\.id\} \/>;/);
  // A phone has no sidebar: Home has the row, above Usage too.
  const { PHONE_HOME_ROWS } = await import("../src/lib/phone.ts");
  const labels = PHONE_HOME_ROWS.map((r) => r.label);
  assert.equal(labels.indexOf("Resources") + 1, labels.indexOf("Usage and accounts"));
  assert.equal(PHONE_HOME_ROWS.find((r) => r.label === "Resources").path, "/resources");
});

test("the machine-load settings no longer share the name", () => {
  assert.ok(read("components/resources/resource-settings.tsx").includes('<span id="resources">Machine load</span>'), "label changed, anchor kept");
  const settings = read("pages/settings.tsx");
  assert.ok(settings.includes('["resources", "Machine load"]') && !settings.includes('["resources", "Resources"'));
});

test("the page shows no value of a secret and writes nothing", () => {
  const page = read("pages/resources.tsx");
  assert.ok(!/\b(POST|PUT|DELETE|PATCH)\b/.test(page), "read only");
  assert.ok(page.includes("secretText"), "secrets go through secretText (name + stored/missing)");
  const api = read("lib/toolbox.ts");
  assert.deepEqual(api.match(/request<[^>]+>\("([A-Z]+)"/g), ['request<ResourceList>("GET"']);
});
