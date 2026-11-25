// Renders the whole app to HTML strings against the mock daemon, at a list of addresses, for the
// profile in RENDER_SEARCH (?mock=still or ?mock=still&ui=classic), and prints them as JSON.
import "./shim";
import { readFileSync } from "node:fs";
import { renderToString } from "react-dom/server";
import App from "@/App";
import { agentPinKey } from "@/lib/agent-pins";
import { useUI } from "@/lib/ui";
import { SidebarContent } from "@/components/sidebar";
import { ShellsList } from "@/components/classic/project-shells";
import { ThreadBoard } from "@/components/matrix/thread-board";
import { Onboarding } from "@/components/onboarding";
import { Dialog } from "@/components/ui/dialog";
import type { MachineRegistry } from "@/lib/machine-registry";
import { TooltipProvider } from "@/components/ui/tooltip";
import { companionApi } from "@/lib/companion";
import type { ThreadCard } from "@/lib/matrix";
import { questionsByThread } from "@/lib/matrix-head";
import { ONBOARD_STEPS } from "@/lib/onboarding-rules";
import { loadWhatsApp } from "@/lib/whatsapp";
import { MockTransport } from "@/lib/mock/transport";
import { ensureFullTimeline, start, useStore } from "@/lib/store";

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

async function main() {
  const t = new MockTransport({ scenario: "still" });
  await start(t);
  await sleep(700); // the snapshot over the event stream, the timeline previews
  const st = useStore.getState();
  const head = st.headId!;
  const by = (f: (a: (typeof st.agents)[string]) => boolean) => Object.values(st.agents).find(f)!;
  const codex = by((a) => a.harness === "codex");
  const shell = by((a) => a.harness === "shell");
  const worker = by((a) => a.level === "worker" && a.harness === "claude");
  const project = Object.values(st.projects)[0];
  // the whole conversations, as the page loads them when it opens (the first read is a short preview)
  for (const a of Object.values(st.agents)) ensureFullTimeline(a.id);
  await sleep(500);
  const routes: Record<string, string> = {
    home: "/",
    "agent.head": `/agents/${head}`,
    "agent.head.conversation": `/agents/${head}/conversation`,
    "agent.head.terminal": `/agents/${head}/terminal`,
    "agent.head.details": `/agents/${head}/details`,
    "agent.worker": `/agents/${worker.id}`,
    // a lead: its conversation has mail from its own workers on this machine
    "agent.lead": `/agents/${project.lead_id}`,
    "agent.codex": `/agents/${codex.id}`,
    "agent.codex.terminal": `/agents/${codex.id}/terminal`,
    "agent.shell": `/agents/${shell.id}`,
    inbox: "/inbox",
    project: `/projects/${project.id}`,
    settings: "/settings",
    "settings.general": "/settings?section=setup",
    "settings.devices": "/settings?section=owner-devices",
    "profile.worker": "/settings/profiles/worker",
    matrix: "/matrix",
    people: "/people",
    clients: "/clients",
    promises: "/promises",
    usage: "/usage",
    recordings: "/recordings",
    chats: "/chats",
    whatsapp: "/whatsapp",
    features: "/features",
    projects: "/projects",
    shells: "/shells",
  };
  const out: Record<string, string> = { profile: st.uiProfile, status: st.status };
  for (const [name, path] of Object.entries(routes)) {
    (globalThis as unknown as { location: { hash: string } }).location.hash = "#" + path;
    try {
      out[name] = renderToString(<App />);
    } catch (e) {
      out[name] = "RENDER ERROR: " + ((e as Error).stack || String(e));
    }
  }
  // Pinning a stopped agent keeps a shortcut visible in both sidebar profiles.
  for (const pinned of [true, false]) {
    useUI.setState({ agentPins: pinned ? [agentPinKey(st.machine, worker.id)] : [] });
    const updated = { ...worker, state: "stopped" as const, desired: "stopped" as const };
    useStore.setState((s) => ({ agents: { ...s.agents, [worker.id]: updated } }));
    out[pinned ? "pins.on" : "pins.off"] = renderToString(<TooltipProvider><SidebarContent /></TooltipProvider>);
  }
  useStore.setState((s) => ({ agents: { ...s.agents, [worker.id]: worker } }));
  // The first-run setup, one screen at a time (a dialog's portal does not render here, so its
  // screens are rendered inside a bare open dialog). No effects run: the phone screen is still
  // reading; WhatsApp and Companion show what the mock daemon says, read here first.
  await loadWhatsApp();
  await companionApi.status(false).catch(() => {});
  for (const step of ONBOARD_STEPS) {
    try {
      out[`setup.${step}`] = renderToString(
        <Dialog open>
          <Onboarding start={step} onClose={() => {}} />
        </Dialog>,
      );
    } catch (e) {
      out[`setup.${step}`] = "RENDER ERROR: " + ((e as Error).stack || String(e));
    }
  }
  // The Matrix loads its threads after the first paint, so its Board view is rendered here by
  // itself, with the threads and the owner's open questions the mock daemon has.
  try {
    const cards = await t.request<ThreadCard[]>("GET", "/api/matrix/mine");
    const now = useStore.getState();
    out["board.view"] = renderToString(
      <TooltipProvider>
        <ThreadBoard cards={cards} questions={questionsByThread(now.approvals, now.headId ?? undefined)} open={null} onOpen={() => {}} now={Date.now()} />
      </TooltipProvider>,
    );
  } catch (e) {
    out["board.view"] = "RENDER ERROR: " + ((e as Error).stack || String(e));
  }
  // A project's "Shells & scripts" list loads after the first paint too: rendered by itself with
  // what the mock daemon's registry has for the billing project, and for a project with nothing.
  try {
    const reg = await t.request<MachineRegistry>("GET", "/api/registry?project=pbl7n2c4ka");
    out["shells.view"] = renderToString(<ShellsList entries={reg.entries} now={Date.now()} onStop={() => {}} onRestart={() => {}} />);
    const safeEntries: MachineRegistry["entries"] = JSON.parse(readFileSync(process.env.TMX_REGISTRY_RENDER_FIXTURE!, "utf8"));
    out["shells.safe"] = renderToString(<ShellsList entries={safeEntries} now={Date.now()} onStop={() => {}} onRestart={() => {}} />);
    const none = await t.request<MachineRegistry>("GET", "/api/registry?project=pdc5v7x9za");
    out["shells.none"] = renderToString(<ShellsList entries={none.entries} now={Date.now()} onStop={() => {}} onRestart={() => {}} />);
  } catch (e) {
    out["shells.view"] = out["shells.none"] = "RENDER ERROR: " + ((e as Error).stack || String(e));
  }
  process.stdout.write(JSON.stringify(out));
  process.exit(0);
}
void main();
