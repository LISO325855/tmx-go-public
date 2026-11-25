// pnpm render-check: renders the whole app to HTML in Node, against the mock daemon, at every
// main address, once for the default dashboard and once for the classic UI profile, and checks
// what each one shows (go-ui-desktop-f for tmx-lead, 17 Nov 2026).
//
// Why: browsers never run on the owner's PCs and the private repo runs no CI, so until a build is
// installed nobody sees the screens. This is not a browser: it runs the render phase only (no
// effects, no CSS, no layout, no clicks). It does catch a screen that throws while rendering, a
// hook used wrongly, and wrong content for a profile: which tabs, which words, what is hidden.
// The real look stays for ui/e2e (Linux CI) and for a person in front of the installed build.
//
// How: scripts/render/vite.config.ts bundles scripts/render/entry.tsx for Node (shim.ts is the
// minimal "browser"); the bundle prints one JSON object of address -> HTML per profile.
import { spawnSync } from "node:child_process";
import { readFileSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { build } from "vite";

const ui = path.resolve(import.meta.dirname, "..");
const out = path.join(ui, "scripts/render/out");
const fixtureDir = mkdtempSync(path.join(tmpdir(), "tmx-registry-render-"));
process.env.TMX_REGISTRY_RENDER_FIXTURE = path.join(fixtureDir, "registry.json");
process.on("exit", () => rmSync(fixtureDir, { recursive: true, force: true }));
const fixture = spawnSync("go", ["test", "./internal/fleet", "-run", "^TestRegistrySafeDisplayFixture$", "-count=1"], {
  cwd: path.dirname(ui), env: process.env, encoding: "utf8", windowsHide: true,
});
if (fixture.status !== 0) throw new Error(`registry fixture failed: ${fixture.stdout}\n${fixture.stderr}`);

await build({ configFile: path.join(ui, "scripts/render/vite.config.ts") });

function render(search, width) {
  const r = spawnSync(process.execPath, [path.join(out, "entry.js")], { env: { ...process.env, RENDER_SEARCH: search, RENDER_WIDTH: String(width) }, encoding: "utf8", maxBuffer: 256 * 1024 * 1024 });
  if (r.status !== 0) throw new Error(`rendering ${search} failed (exit ${r.status}):\n${r.stderr.slice(0, 4000)}`);
  return JSON.parse(r.stdout);
}

// a computer window below the side-by-side width (one view at a time), and a wide one for classic
const d = render("?mock=still", 1100);
const c = render("?mock=still&ui=classic", 1100);
const w = render("?mock=still&ui=classic", 1440);
const dw = render("?mock=still", 1440);

// ---- reading the HTML ------------------------------------------------------------------------
const unescape = (s) =>
  s.replace(/&#x([0-9a-f]+);/gi, (_, n) => String.fromCodePoint(parseInt(n, 16))).replace(/&#(\d+);/g, (_, n) => String.fromCodePoint(Number(n))).replace(/&quot;/g, '"').replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&amp;/g, "&");
const text = (h) =>
  unescape(
    h
      .replace(/<(script|style)[^>]*>[\s\S]*?<\/\1>/g, " ")
      .replaceAll("<!-- -->", "") // React's text separators are not spaces
      .replace(/<[^>]+>/g, " "),
  )
    .replace(/\s+/g, " ")
    .trim();
const tabs = (h) => [...h.matchAll(/<button[^>]*role="tab"[^>]*>([\s\S]*?)<\/button>/g)].map((m) => text(m[1]));
const selectedTab = (h) => {
  const m = [...h.matchAll(/<button([^>]*role="tab"[^>]*)>([\s\S]*?)<\/button>/g)].find((x) => x[1].includes('aria-selected="true"'));
  return m ? text(m[2]) : null;
};
const mainPart = (h) => (h.indexOf("<main") >= 0 ? h.slice(h.indexOf("<main")) : h);
const sidePart = (h) => (h.indexOf("<aside") >= 0 ? h.slice(h.indexOf("<aside"), h.indexOf("</aside>")) : "");
const h1 = (h) => {
  const m = /<h1[^>]*>[\s\S]*?<\/h1>/.exec(mainPart(h));
  return m ? text(m[0]) : "";
};
const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);

let count = 0;
const fails = [];
function check(what, ok, detail = "") {
  count++;
  if (!ok) fails.push(`${what}${detail ? ` | ${String(detail).slice(0, 300)}` : ""}`);
}

// ---- every address renders, in both profiles ---------------------------------------------------
for (const [name, r] of [
  ["default", d],
  ["classic", c],
]) {
  for (const [k, v] of Object.entries(r)) check(`${name}: ${k} renders`, !v.startsWith("RENDER ERROR"), v);
  check(`${name}: the app is ready`, r.status === "ready", r.status);
  const safeRow = text(r["shells.safe"] ?? "");
  check(`${name}: real registry row hides the secret parameter tail`, safeRow.includes("worker --token [hidden]"));
  check(`${name}: real registry row never renders sentinel or private executable path`, !safeRow.includes("registry-test-sentinel") && !safeRow.includes("/private/bin"));
}
check("default: no UI profile", d.profile === "", d.profile);
check("classic: the daemon says classic", c.profile === "classic", c.profile);

// ---- the default dashboard is as it was --------------------------------------------------------
{
  const h = d["agent.head"];
  check("default: tabs", same(tabs(h), ["Conversation", "Claude Code", "Details"]), tabs(h));
  check("default: opens on Conversation (the head is in chat mode)", selectedTab(h) === "Conversation", selectedTab(h));
  check("default: Pin buttons on messages", h.includes('aria-label="Pin message"'));
  check("default: the key hint under the message box", text(h).includes("Enter to send"));
  check("default: the model badge in the header", [...mainPart(h).matchAll(/<header\b[^>]*>[\s\S]*?<\/header>/g)].some(([header]) => text(header).includes("Opus")));
  check("default: no classic header line", !h.includes("data-agent-line"));
  check("default: Codex's tab is named Codex", tabs(d["agent.codex"]).includes("Codex"), tabs(d["agent.codex"]));
  check("default: a shell's tabs", tabs(d["agent.shell"])[0] === "Shell", tabs(d["agent.shell"]));
  const s = text(sidePart(d.home));
  for (const w of ["Home", "Needs you", "New project", "Show all", "Features", "Matrix", "Recordings"]) check(`default sidebar: ${w}`, s.includes(w));
  check("default sidebar: no Workspace", !s.includes("Workspace"));
  check("default sidebar: no search box", !d.home.includes("data-agent-search"));
  check("default sidebar: one-line agent rows", !d.home.includes("data-agent-row"));
  check("default: the start page is Home", text(mainPart(d.home)).includes("Pick up a conversation"));
  check("default: the cards page is Needs you", h1(d.inbox) === "Needs you", h1(d.inbox));
  check("default: cards grouped by agent", !d.inbox.includes("data-card-project"));
  check("default: no Not applicable", !text(d.inbox).includes("Not applicable"));
  const det = text(mainPart(d["agent.head.details"]));
  check("default details: no mode button", !det.includes("Run in chat mode") && !det.includes("Open Claude Code's screen"));
}

// ---- classic: the agent view ---------------------------------------------------------------
{
  const h = c["agent.head"];
  check("classic: tabs", same(tabs(h), ["Chat", "Terminal", "Details"]), tabs(h));
  check("classic: opens on Chat though the head runs in its own screen", selectedTab(h) === "Chat", selectedTab(h));
  check("classic: the message box under Chat", h.includes('aria-label="Message '));
  check("classic: no Pin buttons", !h.includes('aria-label="Pin message"') && !h.includes('aria-label="Unpin message"'));
  check("classic: no pin strip", !h.includes('aria-label="Pinned messages"'));
  check("classic: no key hint", !text(h).includes("Enter to send"));
  check("classic: no notice about the screen above the message box", !text(h).includes("own screen. What it does there"));
  check("classic: the one header line", h.includes("data-agent-line"));
  check("classic: no model badge in the header", !text(mainPart(h).slice(0, 5000)).includes("Opus"));
  check("classic: no mail badge", !h.includes("data-mail-receipts"));
  check("classic: no Hand to Companion on the agent", !text(mainPart(h)).includes("Hand to Companion"));

  const t = c["agent.head.terminal"];
  const tt = text(mainPart(t));
  check("classic Terminal: selected", selectedTab(t) === "Terminal", selectedTab(t));
  check("classic Terminal: the message box is under it too", t.includes('aria-label="Message '));
  for (const gone of ["Back to chat", "Don't switch", "Got it", "Stay here", "opens when this turn ends", "same conversation"]) check(`classic Terminal: no "${gone}"`, !tt.includes(gone));
  check("classic Terminal: an agent in its screen is not told it runs in chat mode", !tt.includes("runs in chat mode"));
  // Python's key row under the terminal, and "Paste only" beside Send: only with a live screen
  check("classic Terminal: the key row", t.includes("data-terminal-keys") && ["Enter", "Esc", "Ctrl+C", "Tab"].every((k) => tt.includes(k)), tt);
  check("classic Terminal: Paste only", t.includes("data-paste-only"));
  check("classic Chat of an agent in its screen: Paste only, no key row", h.includes("data-paste-only") && !h.includes("data-terminal-keys"));
  check("classic, an agent in chat mode: no key row and no Paste only", !c["agent.codex.terminal"].includes("data-terminal-keys") && !c["agent.codex.terminal"].includes("data-paste-only"));
  check("default: no key row and no Paste only", !d["agent.head.terminal"].includes("data-terminal-keys") && !d["agent.head"].includes("data-paste-only") && !d["agent.shell"].includes("data-terminal-keys"));
  check("classic shell: the key row under its terminal", c["agent.shell"].includes("data-terminal-keys"));

  const x = c["agent.codex.terminal"];
  const xt = text(mainPart(x));
  check("classic, an agent in chat mode: tabs", same(tabs(c["agent.codex"]), ["Chat", "Terminal", "Details"]), tabs(c["agent.codex"]));
  check("classic, an agent in chat mode: opens on Chat", selectedTab(c["agent.codex"]) === "Chat");
  check("classic, an agent in chat mode: Terminal says so in one line", xt.includes("is in chat mode."), xt);
  check("classic, an agent in chat mode: the explicit button", xt.includes("Open Codex's screen"), xt);
  check("classic, an agent in chat mode: the message box", x.includes('aria-label="Message '));

  const sh = c["agent.shell"];
  check("classic shell: tabs", same(tabs(sh), ["Terminal", "Log", "Details"]), tabs(sh));
  check("classic shell: opens on Terminal", selectedTab(sh) === "Terminal", selectedTab(sh));
  check("classic shell: no message box under a shell", !sh.includes('aria-label="Message '));

  const det = text(mainPart(c["agent.head.details"]));
  check("classic details: the mode", det.includes("its own screen (read by tmx)"), det);
  check("classic details: the deliberate way back", det.includes("Run in chat mode"));
}

// ---- classic: the start page, the sidebar, Your turn ---------------------------------------------
{
  check("classic: the start page is the Architect's conversation", same(tabs(c.home), ["Chat", "Terminal", "Details"]), tabs(c.home));
  check("classic: the start page is not Home", !text(mainPart(c.home)).includes("Pick up a conversation"));
  const side = sidePart(c.home);
  const s = text(side);
  for (const w of ["Workspace", "Your turn", "Architect", "Matrix", "Recordings", "Usage and accounts"]) check(`classic sidebar: ${w}`, s.includes(w), s);
  for (const w of ["Needs you", "Show all", "Features", "New project"]) check(`classic sidebar: no ${w}`, !s.includes(w), s);
  check("classic sidebar: the search box", c.home.includes('placeholder="Search agents, projects, machines"'));
  const rows = [...side.matchAll(/data-agent-row="([^"]+)"/g)];
  check("classic sidebar: two-line agent rows", rows.length >= 5, rows.length);
  const words = new Set([...side.matchAll(/data-agent-state="true"[^>]*>[\s\S]*?<\/span>([a-z ]+)<\/span>/g)].map((m) => m[1]));
  const known = ["needs you", "asks you", "working", "starting", "idle", "queued", "paused", "failed", "stopped", "ended", "shelved"];
  check("classic sidebar: the state in words", words.size > 0 && [...words].every((w) => known.includes(w)), [...words]);
  check("classic sidebar: + Agent", c.home.includes('aria-label="New agent"'));

  // People: one entry, the page has Python's two tabs
  check("classic sidebar: one People entry, no Clients", s.includes("People") && !/\bClients\b/.test(s.replace("Clients & partners", "")), s);
  check("default sidebar: People and Clients", /\bClients\b/.test(text(sidePart(d.home))));
  for (const page of ["people", "clients"]) {
    const pt = text(mainPart(c[page]));
    check(`classic ${page}: the tabs Team | Clients & partners`, c[page].includes("data-people-tabs") && pt.includes("Team") && pt.includes("Clients & partners"), pt);
    check(`classic ${page}: titled People`, h1(c[page]) === "People", h1(c[page]));
    check(`default ${page}: no tabs`, !d[page].includes("data-people-tabs"));
  }
  check("default clients: titled Clients", h1(d.clients) === "Clients", h1(d.clients));

  // Mail between agents is folded in a classic conversation; the default dashboard shows it inline
  const folded = Object.keys(c).filter((k) => k.startsWith("agent.") && c[k].includes("data-agent-traffic"));
  const inline = Object.keys(d).filter((k) => k.startsWith("agent.") && text(mainPart(d[k])).includes("Message from"));
  check("default: agent mail inline somewhere in the mock", inline.length > 0, inline);
  check("classic: where there is agent mail it is folded, and none is inline", folded.length > 0 && Object.keys(c).every((k) => !k.startsWith("agent.") || !text(mainPart(c[k])).includes("Message from")), folded);
  check("default: nothing is folded", Object.keys(d).every((k) => !d[k].includes("data-agent-traffic")));
  // each agent message is its own closed row, like a tool call (the owner, 17 Nov 18:30): a button that says it is closed, none open
  const msgRows = folded.map((k) => (c[k].match(/data-agent-message="closed"/g) ?? []).length);
  check("classic: every agent message is a row of its own, closed", folded.length > 0 && msgRows.every((n) => n > 0) && folded.every((k) => !c[k].includes('data-agent-message="open"')), msgRows);
  check("classic: a closed agent message is a button that can be opened", folded.every((k) => /data-agent-message="closed"><button[^>]*aria-expanded="false"/.test(c[k])));
  // the version number has its own line under Settings in the classic sidebar
  check("classic sidebar: the version on its own line", c.home.includes("data-sidebar-version") && /flex-col[^"]*" data-sidebar-footer/.test(c.home));

  // The conversation, easy to skim (the owner, 17 Nov 19:26): replies plain, tool runs as one soft
  // closed card "N steps", agent mail with a coloured rule and a sender chip, his own messages
  // clearly his. On a computer, side by side, and on a phone; the default dashboard keeps its rows.
  const phone = render("?mock=still&ui=classic", 390);
  check("phone home: Shells is reachable", phone.home.includes('href="#/shells"'));
  check("phone: real registry row hides sentinel", text(phone["shells.safe"]).includes("[hidden]") && !phone["shells.safe"].includes("registry-test-sentinel"));
  for (const [name, r] of [
    ["classic", c],
    ["classic wide", w],
    ["classic phone", phone],
  ]) {
    for (const [k, v] of Object.entries(r)) if (name === "classic phone") check(`${name}: ${k} renders`, !v.startsWith("RENDER ERROR"), v);
    for (const [name, r] of [["default", d], ["classic", c]]) {
  check(`${name}: a pinned stopped agent stays visible`, r["pins.on"].includes('aria-label="Pinned agents"') && r["pins.on"].includes('href="#/agents/'));
  check(`${name}: unpin removes the shortcut section`, !r["pins.off"].includes('aria-label="Pinned agents"'));
}

const pages = Object.keys(r).filter((k) => k.startsWith("agent.") && !k.endsWith(".details") && !k.endsWith(".terminal"));
    const all = pages.map((k) => r[k]).join("\n");
    const cards = [...all.matchAll(/data-chat="steps" data-tool-run="(open|closed)"/g)].map((m) => m[1]);
    check(`${name}: tool calls are soft cards, every one closed`, cards.length > 0 && cards.every((s) => s === "closed"), cards.length);
    check(`${name}: a card says "N steps"`, /data-chat="steps"[\s\S]{0,900}?>\d+ steps?</.test(all));
    check(`${name}: no single tool rows outside a card`, !/aria-expanded="false"[^>]*class="group\/tool/.test(all.replace(/data-agent-message="closed">[\s\S]*?<\/button>/g, "")), "a tool row is showing though its card is closed");
    check(`${name}: replies are plain text in the reading line`, (all.match(/data-chat="reply"/g) ?? []).length >= 2);
    check(`${name}: his own messages are marked as his`, (all.match(/data-chat="mine"/g) ?? []).length >= 1);
    check(`${name}: agent mail has its own look: a coloured rule and a sender chip`, all.includes('data-chat="mail"') && all.includes('data-mail-chip="from"'));
    check(`${name}: mail the agent sent shows who it went to`, /data-mail-chip="to"[^>]*>to [a-z0-9-]+</.test(all), "no outgoing mail row");
    const head = text(mainPart(r["agent.head"]));
    check(`${name}: what the Architect sent to the owner reads as a reply`, head.includes("billing-portal is staffed"), head.slice(0, 200));
  }
  {
    // Mail from an agent's OWN workers on the same machine (the owner, 17 Nov 19:50, on his phone:
    // "a message from matrix-keeper-g, worker" in his chat with the Architect). The daemon stamps
    // it with the worker's id as sender (internal/fleet/mail.go), like any agent mail, so it is a
    // closed row with a sender chip: on a phone too, and never the old "Message from" box.
    for (const [name, r] of [
      ["classic phone", phone],
      ["classic", c],
    ]) {
      const lead = r["agent.lead"];
      const lt = text(mainPart(lead));
      const chips = [...lead.matchAll(/data-mail-chip="from"[^>]*>([^<]+)</g)].map((m) => m[1]);
      check(`${name}, local worker mail: a closed row with the worker's name on its chip`, chips.some((n) => /worker/.test(n)) && lead.includes('data-agent-message="closed"'), chips);
      check(`${name}, local worker mail: no "Message from" box and no open mail`, !lt.includes("Message from") && !lead.includes('data-agent-message="open"'), lt.slice(0, 200));
    }
    check("default, local worker mail: the box as before", text(mainPart(d["agent.lead"])).includes("Message from"));
  }
  {
    const dh = d["agent.head"];
    check("default: tool calls are single rows as before, no cards", !dh.includes('data-chat="steps"') && /class="group\/tool/.test(dh) && !dh.includes('data-chat="mine"') && !dh.includes("data-mail-chip"));
    // both themes: every colour the new looks use is a token with a light and a dark value
    const css = readFileSync(path.join(ui, "src/index.css"), "utf8");
    const light = css.slice(css.indexOf(":root {"), css.indexOf(".dark {"));
    const dark = css.slice(css.indexOf(".dark {"));
    for (const token of ["--bubble", "--brand", "--brand-soft", "--muted", "--border", "--card", "--state-error", "--muted-foreground", "--subtle-foreground"]) {
      check(`both themes define ${token}`, new RegExp(`${token}:`).test(light) && new RegExp(`${token}:`).test(dark), token);
    }
  }

  // A wide markdown table in a reply (the owner, 17 Nov 18:54: its first column broke words in the
  // middle). The render shows the table is there, cell by cell and unbroken; how it is laid out
  // is CSS, so the rules themselves are read from the stylesheet.
  for (const [name, r] of [
    ["default", d],
    ["classic", c],
    ["classic wide", w],
  ]) {
    const h = r["agent.head"];
    const table = /<div class="prose-tmx[^"]*">\s*<table>[\s\S]*?<\/table>/.exec(h)?.[0] ?? "";
    const cells = [...table.matchAll(/<t[hd][^>]*>([\s\S]*?)<\/t[hd]>/g)].map((m) => text(m[1]));
    check(`${name}: the wide table is in the conversation, inside the prose`, table.length > 0 && cells.length === 15, cells.length);
    check(`${name}: its first column's words are whole`, ["Python tab", "Workspace", "Recordings", "Your turn", "Promises"].every((x) => cells.includes(x)), cells);
  }
  {
    const css = readFileSync(path.join(ui, "src/index.css"), "utf8").replace(/\r\n/g, "\n");
    const ruleOf = (selector) => {
      const at = css.indexOf(selector + " {");
      return at < 0 ? "" : css.slice(at, css.indexOf("\n}", at));
    };
    const cell = ruleOf(".prose-tmx th,\n.prose-tmx td");
    const tbl = ruleOf(".prose-tmx table");
    check("tables: a cell breaks between words only", /overflow-wrap: normal;/.test(cell) && /word-break: normal;/.test(cell), cell);
    check("tables: a table that does not fit scrolls sideways", /display: block;/.test(tbl) && /overflow-x: auto;/.test(tbl), tbl);
  }

  // A project's shared page (status, progress, outputs) sits on the project screen. The content
  // comes from the daemon after the first paint, so only its place can be checked here.
  {
    const pt = text(mainPart(c.project));
    check("classic project: where it stands, with who coordinates and how many are active", c.project.includes("data-project-status") && pt.includes("Where it stands") && c.project.includes("data-project-meta") && pt.includes("coordinator") && /\d+ active · \d+ shelved/.test(pt), pt);
    check("classic project: files and locations", c.project.includes('data-project-card-section="folders"') && pt.includes("Files and locations"));
    check("classic project: a place for the repository", c.project.includes('data-project-card-section="repo"') && pt.includes("Repository"));
    const team = [...c.project.matchAll(/data-team-member="([^"]+)"/g)];
    const heads = (c.project.slice(c.project.indexOf('data-project-card-section="team"')).match(/data-agent-face=/g) ?? []).length;
    check("classic project: the team, each member with its little head and its state in words", team.length >= 2 && heads === team.length && /Team · \d+/.test(pt), `${team.length} members, ${heads} heads`);
    check("classic project: the team replaces the plain agent list", !c.project.includes(">Agents<"));
    check("default project: no shared page, its four numbers and its agent list stay", !d.project.includes("data-project-status") && text(mainPart(d.project)).includes("Turns") && d.project.includes(">Agents<") && !d.project.includes("data-agent-face"));
  }

  // The Projects page: a card per project, like Python's, with the row of little agent heads.
  // (Status, progress bar and output on a card come from the daemon after the first paint.)
  for (const [name, r] of [
    ["classic", c],
    ["classic wide", w],
  ]) {
    const h = r.projects;
    const cards = [...h.matchAll(/data-project-card="([^"]+)"/g)];
    const pt = text(mainPart(h));
    check(`${name} #/projects: a card per project, under "Active projects"`, h.includes("data-classic-projects") && cards.length >= 3 && /Active projects · \d+/.test(pt), cards.length);
    check(`${name} #/projects: positive counts are shown and zero counts are hidden`, /[1-9]\d* agent\(s\)/.test(pt) && !/\b0 (agent\(s\)|working)/.test(pt), pt.slice(0, 300));
    check(`${name} #/projects: card rows have even heights`, h.includes("auto-rows-fr"));
    check(`${name} #/projects: every card has its progress bar`, (h.match(/data-card-progress/g) ?? []).length === cards.length);
    check(`${name} #/projects: a coordinator is named`, /coordinator [a-z0-9-]+/.test(pt), pt.slice(0, 300));
    const faces = (mainPart(h).match(/data-agent-face=/g) ?? []).length;
    check(`${name} #/projects: the rows of little agent heads`, (h.match(/data-card-faces/g) ?? []).length >= 2 && faces >= 6, faces);
    check(`${name} #/projects: the heads are drawn faces, not letters`, /<span[^>]*data-agent-face="[^"]+"[^>]*><svg viewBox="0 0 24 24"/.test(mainPart(h)));
    check(`${name} sidebar: Projects is an entry`, /href="#\/projects"/.test(sidePart(r.home)));
  }
  check("classic sidebar: agent rows show the little heads", (sidePart(c.home).match(/data-agent-face=/g) ?? []).length >= 5);
  {
    // The projects list (the owner, 17 Nov 19:03): collapsed by default, a project opens only while one of
    // its agents is busy. In the mock, billing-portal and docs-site have busy agents; ingest-pipeline's
    // only agent is paused, so it is one line.
    const side = sidePart(c.home);
    const open = [...side.matchAll(/aria-label="(Collapse|Expand) ([^"]+)"/g)].map((m) => `${m[1]} ${m[2]}`);
    check("classic sidebar: a project with busy agents is open, a quiet one is one line", open.includes("Collapse billing-portal") && open.includes("Expand ingest-pipeline"), open);
    check("classic sidebar: the collapse all / expand active toggle", side.includes('data-projects-mode="active"') && text(side).includes("collapse all"), text(side).slice(0, 200));
    const dopen = [...sidePart(d.home).matchAll(/aria-label="(Collapse|Expand) ([^"]+)"/g)].map((m) => `${m[1]} ${m[2]}`);
    check("default sidebar: every active project is open as before, and there is no toggle", dopen.includes("Collapse ingest-pipeline") && !sidePart(d.home).includes("data-projects-mode"), dopen);
  }
  check("default #/projects: its own list, no cards with heads", !d.projects.includes("data-classic-projects") && !d.projects.includes("data-agent-face"));
  check("default sidebar: no little heads, no Projects entry", !sidePart(d.home).includes("data-agent-face") && !/href="#\/projects"/.test(sidePart(d.home)));

  // The Board view of the Matrix (the owner's "prosthetics for my brain"): one row per thread, grouped
  // Needs you -> Blocked -> In progress -> Done today, a bar per row, what it waits for in words.
  {
    const b = c["board.view"];
    const bt = text(b);
    const groups = [...b.matchAll(/data-board-group="([a-z_]+)"/g)].map((m) => m[1]);
    check("board: the groups in the order he reads them", same(groups, ["needs_you", "blocked", "in_progress", "done_today"]), groups);
    const rows = [...b.matchAll(/data-board-row="([^"]+)"/g)].map((m) => m[1]);
    check("board: one row per thread, each once", rows.length >= 5 && new Set(rows).size === rows.length, rows);
    check("board: every row has a progress bar", (b.match(/role="progressbar"/g) ?? []).length === rows.length);
    check("board: the totals on top", b.includes("data-board-totals") && /Needs you \d+/.test(bt) && /Done today \d+/.test(bt), bt.slice(0, 160));
    const needs = b.slice(b.indexOf('data-board-group="needs_you"'), b.indexOf('data-board-group="blocked"'));
    check("board, Needs you: what the owner has to do, in words", text(needs).includes("You: Choose the search"), text(needs).slice(0, 300));
    check("board, Needs you: a thread with an open question is there, with its Your-turn card on the row", needs.includes("data-board-ask="), text(needs).slice(0, 300));
    const blocked = b.slice(b.indexOf('data-board-group="blocked"'), b.indexOf('data-board-group="in_progress"'));
    check("board, Blocked: what stops it", text(blocked).includes("Blocked: Waits for the sample files"), text(blocked));
    check("board: a row says how far it is and where it stands", bt.includes("55%") && bt.includes("Webhooks and the plan picker are done"), bt.slice(0, 400));
    check("board: a thread that reported nothing says so", bt.includes("Not reported"));
    check("board: who owns it and when it last moved", (b.match(/data-board-who/g) ?? []).length === rows.length && /updated \d+[smhd]? ?(m|h|s|d)? ?ago|updated just now/.test(bt), bt.slice(0, 500));
    check("board: done today is at the end, at 100%", text(b.slice(b.indexOf('data-board-group="done_today"'))).includes("100%"));
    // interactive (the owner 20:58): on every open row the one-tap actions, a note field and a handle
    const openRows = rows.length - (b.slice(b.indexOf('data-board-group="done_today"')).match(/data-board-row=/g) ?? []).length;
    check("board: a note field on every row", (b.match(/data-board-note/g) ?? []).length === rows.length);
    check("board: Done, Thanks, Park it, Not relevant on an open row", ["done", "thanks", "park", "not_relevant"].every((a) => (b.match(new RegExp(`data-board-act="${a}"`, "g")) ?? []).length >= 1) && (b.match(/data-board-act="not_relevant"/g) ?? []).length === openRows, openRows);
    check("board: a finished thread offers Thanks only", !/data-board-act="(park|done|not_relevant)"/.test(b.slice(b.indexOf('data-board-group="done_today"'))) && b.slice(b.indexOf('data-board-group="done_today"')).includes('data-board-act="thanks"'));
    check("board: a move handle on every open row, up and down", (b.match(/data-board-handle/g) ?? []).length === openRows && (b.match(/aria-label="Move [^"]+ up"/g) ?? []).length === openRows);
    check("board: open rows can be dragged, finished ones not", (b.match(/draggable="true"/g) ?? []).length === openRows);
    check("classic phone #/matrix: the Board is the view in force there too", /role="radio"[^>]*aria-checked="true"[^>]*>board</.test(phone.matrix));
    // the Matrix page: under classic it opens on the Board; the default dashboard has no Board
    check("classic #/matrix: the Board is offered first and is the view in force", /role="radio"[^>]*aria-checked="true"[^>]*>board</.test(c.matrix), text(mainPart(c.matrix)).slice(0, 200));
    check("default #/matrix: no Board", !/>board</.test(mainPart(d.matrix)) && /role="radio"[^>]*>canvas</.test(d.matrix));
  }

  // The attach button: on a phone under classic Photos first and "File…" second (Python's mobile
  // app); on a computer, and with classic off, the one button as before.
  {
    const withButton = Object.keys(c).filter((k) => typeof c[k] === "string" && c[k].includes('aria-label="Attach files"'));
    check("attach: classic on a computer keeps the one button", withButton.length > 0 && !Object.values(c).some((h) => typeof h === "string" && h.includes("data-attach-photo")), withButton);
    check("attach: the default dashboard keeps the one button", !Object.values(d).some((h) => typeof h === "string" && h.includes("data-attach-photo")));
    const onPhone = withButton.filter((k) => typeof phone[k] === "string" && phone[k].includes("data-attach-photo"));
    check("attach, classic phone: a photo button wherever files can be attached", onPhone.length > 0, withButton);
    for (const k of onPhone) {
      const h = phone[k];
      check(`attach, classic phone ${k}: Photos (image/*) first, then File…`, h.indexOf("data-attach-photo") < h.indexOf("data-attach-file") && /accept="image\/\*"/.test(h) && !h.includes('aria-label="Attach files"'));
    }
  }

  // "Shells & scripts" inside a project (the machine registry): what was left running first and
  // flagged, who started it, and only the buttons the daemon allows.
  {
    const s = c["shells.view"];
    const st = text(s);
    const rows = [...s.matchAll(/data-shell-row="([^"]+)"/g)].map((m) => m[1]);
    check("shells: one row per entry", rows.length === 5 && new Set(rows).size === 5, rows);
    check("shells: what was left running comes first, flagged", rows[0] === "pid:7012:1794890000" && /data-shell-row="pid:7012:1794890000"[^>]*data-orphan/.test(s) && st.includes("Left running"), rows);
    check("shells: why it is flagged, in words", st.includes("Its agent (ui-worker-2) is gone; the process still runs."));
    check("shells: what ended is last", rows[rows.length - 1] === "tool:t1a2b3c4", rows);
    check("shells: the count on top", st.includes("3 running · 1 left running · 1 ended"), st.slice(0, 120));
    check("shells: who started it and since when", st.includes("dev-shell") && /since \d+/.test(st), st.slice(0, 300));
    const row = (id) => s.slice(s.indexOf(`data-shell-row="${id}"`), s.indexOf("</li>", s.indexOf(`data-shell-row="${id}"`)));
    check("shells: a shell has no buttons here", !/<button/.test(row("shell:sh5r7t9v3w")));
    check("shells: a tool can be stopped and restarted", />Restart</.test(row("tool:t7k2m9x4")) && />Stop</.test(row("tool:t7k2m9x4")));
    check("shells: a seen script can only be stopped", !/>Restart</.test(row("pid:6344:1794900000")) && />Stop</.test(row("pid:6344:1794900000")));
    check("shells: the executable's name only, never its arguments", !/--|\.env|token/i.test(st), st);
    check("shells: a project with nothing running says so", text(c["shells.none"]).includes("Nothing is running for this project.") && !c["shells.none"].includes("data-shell-row"));
    check("default project page: Shells & scripts is visible", d.project.includes('data-project-card-section="shells"'));
  }

  // Promises is not a place under classic (the owner, 17 Nov 18:55: the Matrix replaced it): no
  // sidebar entry, the Matrix is threads only, and the address shows the old records read-only.
  // (The "Old promises (N)" link under the Matrix appears once the count has come from the
  // daemon, after the first paint, so it cannot be seen in this render.)
  for (const [name, r] of [
    ["classic", c],
    ["classic wide", w],
  ]) {
    check(`${name} sidebar: no Promises entry`, !/\bPromises\b/.test(text(sidePart(r.home))), text(sidePart(r.home)));
    check(`${name} #/matrix: threads only, no Promises tab and no promise form`, h1(r.matrix) === "Matrix" && !r.matrix.includes("data-matrix-views") && !r.matrix.includes("data-matrix-promises") && !text(mainPart(r.matrix)).includes("Add promise"));
    const pt = text(mainPart(r.promises));
    check(`${name} #/promises: the old records, read-only`, h1(r.promises) === "Old promises" && r.promises.includes("data-old-promises") && pt.includes("Back to the Matrix"), h1(r.promises));
    check(`${name} #/promises: nothing to add or change`, !pt.includes("Add promise") && !pt.includes("What was promised") && !pt.includes("Set reminder"), pt);
  }
  check("default sidebar: Promises stays", /\bPromises\b/.test(text(sidePart(d.home))));
  check("default #/promises: its own page, with its form", h1(d.promises) === "Promises" && !d.promises.includes("data-old-promises"), h1(d.promises));
  check("default #/matrix: as it was", h1(d.matrix) === "Matrix" && !d.matrix.includes("data-old-promises-link"));

  const ib = c.inbox;
  const ibt = text(mainPart(ib));
  check("classic: the cards page is Your turn", h1(ib) === "Your turn", h1(ib));
  check("classic: cards by project", ib.includes("data-card-project"));
  check("classic: project chips", ib.includes("data-project-chips") && ibt.includes("All projects"));
  check("classic: each card names its agent", ib.includes("data-card-agent"));
  check("classic: no key hints", !ibt.slice(0, 400).includes("to move"));
  check("classic: Not applicable / Dismiss on a question", ibt.includes("Not applicable / Dismiss"));
  check("both: the option row carries data-card-choices", ib.includes("data-card-choices") && d.inbox.includes("data-card-choices"));
}

// ---- a profile's editor: the Claude Code MCP servers it adds (tmx-lead, 17 Nov 2026) ------------
{
  for (const [name, r] of [["default", d], ["classic", c]]) {
    const t = text(mainPart(r["profile.worker"]));
    check(`${name}: the profile editor opens on Worker`, h1(r["profile.worker"]).includes("Worker"), h1(r["profile.worker"]));
    check(`${name}: the profile editor has the Claude Code extensions section`, t.includes("Claude Code extensions") && t.includes("MCP servers"));
    check(`${name}: it says the tmx tools always stay`, t.includes("the tmx tools always stay"));
    check(`${name}: installing is offered, and says it asks first`, t.includes("Install plugin") && t.includes("Asks you to confirm first"));
    check(`${name}: no Claude in Chrome switch on the fleet line`, !t.includes("Claude in Chrome"));
  }
}

// ---- classic on a wide screen: Chat and Terminal side by side ---------------------------------
{
  for (const [k, v] of Object.entries(w)) check(`classic wide: ${k} renders`, !v.startsWith("RENDER ERROR"), v);
  for (const [k, v] of Object.entries(dw)) check(`default wide: ${k} renders`, !v.startsWith("RENDER ERROR"), v);
  const h = w["agent.head"];
  check("classic wide: side by side", h.includes("data-agent-split") && h.includes('data-pane="chat"') && h.includes('data-pane="terminal"'));
  check("classic wide: chat pane can shrink for scrolling", /class="[^"]*min-h-0[^"]*" data-pane="chat"/.test(h));
  check("classic wide: pane buttons instead of tabs", h.includes("data-agent-panes") && tabs(h).length === 0, tabs(h));
  check("classic wide: one message box", h.split('aria-label="Message ').length - 1 === 1, h.split('aria-label="Message ').length - 1);
  check("classic wide: the message box comes after both panes", h.indexOf('aria-label="Message ') > h.indexOf('data-pane="terminal"'));
  check("classic wide: Focus view is offered", text(mainPart(h)).includes("Focus view"));
  check("classic wide: the key row sits in the terminal pane", h.includes("data-terminal-keys"));
  check("classic wide: Details is still a view of its own", !w["agent.head.details"].includes("data-agent-split") && text(mainPart(w["agent.head.details"])).includes("Run in chat mode"));
  check("classic wide: chat-mode terminal is a short note, no empty pane", w["agent.codex"].includes("data-agent-split") && !w["agent.codex"].includes('data-pane="terminal"') && text(mainPart(w["agent.codex"])).includes("Open Codex's screen"));
  check("classic wide: a shell keeps its tabs", !w["agent.shell"].includes("data-agent-split") && same(tabs(w["agent.shell"]), ["Terminal", "Log", "Details"]), tabs(w["agent.shell"]));
  check("classic wide: the start page is the Architect, side by side", w.home.includes("data-agent-split"));
  check("classic below the split width: no split, no toggle", !c["agent.head"].includes("data-agent-split") && !c["agent.head"].includes("data-split-toggle"));
  check("default wide: tabs as ever, no split", same(tabs(dw["agent.head"]), ["Conversation", "Claude Code", "Details"]) && !dw["agent.head"].includes("data-agent-split") && !dw["agent.head"].includes("data-split-toggle"), tabs(dw["agent.head"]));
}

// Shells stays discoverable before the registry request completes, on every device.
for (const [name, r] of [["default", d], ["classic", c], ["classic wide", w]]) {
  check(`${name}: Shells navigation`, sidePart(r.home).includes('href="#/shells"'));
  check(`${name}: shells route renders a visible card`, h1(r.shells) === "Shells" && r.shells.includes('data-project-card-section="shells"'));
  check(`${name}: Projects includes shells card before fetching`, r.projects.includes('data-project-card-section="shells"'));
  check(`${name}: individual project includes shells card before fetching`, r.project.includes('data-project-card-section="shells"'));
}

// ---- the first-run setup (components/onboarding.tsx), the same in both profiles -------------------
for (const [name, r] of [
  ["default", d],
  ["classic", c],
]) {
  const t = (step) => text(r[`setup.${step}`] ?? "");
  check(`${name}: Settings can open the setup again`, text(mainPart(r["settings.general"])).includes("Run setup again"));
  check(`${name}: Settings uses the page width`, r.settings.includes("max-w-none") && !r.settings.includes("max-w-[51.25rem]"));
  check(`${name}: Owner devices uses plain words`, text(r["settings.devices"]).includes("Create connection link") && !text(r["settings.devices"]).includes("Normal issuance"));
  check(`${name}: Phone notifications has a next step`, text(r["settings.general"]).includes("Connect a phone in Owner devices"));
  if (name === "classic") {
    check("classic: Settings opens on first tab", selectedTab(r.settings) === "Agents", selectedTab(r.settings));
    check("classic: Settings deep link opens General", selectedTab(r["settings.devices"]) === "General");
  }
  check(`${name} setup: Welcome has its two buttons`, ["Welcome to tmx", "Get started", "Set up later"].every((s) => t("welcome").includes(s)), t("welcome"));
  check(`${name} setup: WhatsApp says it is connected (the mock is linked)`, t("whatsapp").includes("WhatsApp is connected") && t("whatsapp").includes("Continue"), t("whatsapp"));
  check(`${name} setup: the phone step renders`, t("phone").includes("Use tmx on your phone"), t("phone"));
  check(`${name} setup: Companion says it is connected, both links ticked`, t("companion").includes("Companion is connected") && (t("companion").match(/\(done\)/g) ?? []).length === 2, t("companion"));
  check(`${name} setup: Done lists the three and how to come back`, ["You're all set", "WhatsApp", "Your phone", "Companion", "Setup opens this again", "Start using tmx"].every((s) => t("done").includes(s)), t("done"));
  check(`${name} setup: every step can be closed`, ["welcome", "whatsapp", "phone", "companion", "done"].every((s) => (r[`setup.${s}`] ?? "").includes('aria-label="Close setup"')));
  check(`${name} setup: no plain-HTTP address on any screen`, ["welcome", "whatsapp", "phone", "companion", "done"].every((s) => !t(s).includes("http://"))); // the words, not the icons' SVG namespace
}

const pages = Object.keys(d).length - 2;
if (fails.length) {
  console.error(`render-check: ${fails.length} of ${count} checks FAILED (${pages} addresses, 2 profiles)`);
  for (const f of fails) console.error("  ✖ " + f);
  process.exit(1);
}
console.log(`render-check: ${count} checks passed (${pages} addresses, default and classic)`);
