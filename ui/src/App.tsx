import { PhoneNotificationBanner, PhoneNotificationsProvider } from "@/components/phone-notifications";
import { useEffect } from "react";
import { MachinesBar } from "@/components/machines-bar";
import { MessagePinsStrip } from "@/components/message-pins";
import { PhoneBottomBar } from "@/components/phone/bottom-bar";
import { AgentsPage, ProjectsPage } from "@/pages/phone-lists";
import { PhoneHome } from "@/pages/phone-home";
import { usePhoneClassic } from "@/hooks/use-phone-classic";
import { Inbox, KeyRound, Menu, Search, Smartphone } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetDescription, SheetTitle } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { Toaster } from "@/components/ui/sonner";
import { TooltipProvider } from "@/components/ui/tooltip";
import { approvalHeadline } from "@/components/approval";
import { InviteDialog } from "@/components/chat/invite-dialog";
import { JoinDialog } from "@/components/chat/join-dialog";
import { OnboardingDialog } from "@/components/onboarding";
import { offerOnboarding } from "@/lib/onboarding";
import { CommandPalette } from "@/components/command-palette";
import { DaemonUnavailable, useDisconnected } from "@/components/connection";
import { NewAgentHost } from "@/components/new-agent-dialog";
import { NewProjectDialog } from "@/components/new-project-dialog";
import { HandOffDialog } from "@/components/companion/hand-off-dialog";
import { OwnerConfirmDialog } from "@/components/owner-confirm";
import { EmptyState } from "@/components/page";
import { ShortcutsDialog } from "@/components/shortcuts-dialog";
import { Sidebar, SidebarContent } from "@/components/sidebar";
import { classicNow, useClassic } from "@/hooks/use-classic";
import { useGlobalHotkeys } from "@/hooks/use-hotkeys";
import { useMedia } from "@/hooks/use-media";
import { setClassicText } from "@/lib/text-size";
import { loadSafety } from "@/lib/owner-gate";
import { loadUpdate } from "@/lib/update";
import { reloadGate } from "@/lib/reload-guard";
import { PendingUIUpdate } from "@/components/pending-ui-update";
import { WindowUpdateBanner } from "@/components/update-banner";
import { href, navigate, paths, useRoute } from "@/lib/router";
import { onApprovalCreated, pendingApprovals, useStore } from "@/lib/store";
import { summarizeTool } from "@/lib/tools";
import { ui, useUI } from "@/lib/ui";
import { AgentPage } from "@/pages/agent";
import { ChatPage } from "@/pages/chat";
import { ClassicProjectsPage } from "@/pages/classic-projects";
import { ChatsPage } from "@/pages/chats";
import { CompanionPage } from "@/pages/companion";
import { InboxPage } from "@/pages/inbox";
import { OverviewPage } from "@/pages/overview";
import { OldPromisesPage, PeopleBookPage, PromisesPage } from "@/pages/people";
import { ProjectPage } from "@/pages/project";
import { ProfilePage } from "@/pages/profile";
import { RecordingsPage } from "@/pages/recordings";
import { RecordingBar } from "@/components/recorder/recording-bar";
import { DictationBar } from "@/components/dictation-bar";
import { startDictationOutbox } from "@/lib/dictation-outbox";
import { RestartBanner } from "@/components/restart-banner";
import { StartRecordingDialog } from "@/components/recorder/start-dialog";
import { CameraTile } from "@/components/recorder/camera-preview";
import { SettingsPage } from "@/pages/settings";
import { ShellsPage } from "@/pages/shells";
import { MatrixPage } from "@/pages/matrix";
import { WhatsAppPage } from "@/pages/whatsapp";
import { MachinePage } from "@/pages/machine";
import { ResourcesPage } from "@/pages/resources";
import { TeamPage } from "@/pages/team";
import { UsagePage } from "@/pages/usage";
import { WatchdogPage } from "@/pages/watchdog";
import { FeaturesPage } from "@/pages/features";
import { waSendShownAt } from "@/lib/whatsapp";
import { PairDevice } from "@/components/device-pairing";
import { ConnectPhoneButton, ConnectPhoneDialog } from "@/components/connect-phone";
import { ScanPair } from "@/components/scan-pair";
import { PhoneLock } from "@/components/phone-lock";
import { remotePage, takePairCode } from "@/lib/phone-connect";
import { BrandMark } from "@/components/brand-mark";
import { approvalToastId, shouldToastApproval, staleApprovalToasts } from "@/lib/approval-toasts";

function Routes() {
  const r = useRoute();
  const classic = useClassic();
  const desktop = useMedia("(min-width: 768px)");
  const phone = usePhoneClassic(); // classic, below md: Python's mobile app
  const headId = useStore((s) => s.headId);
  // Classic, on a computer: Python's Projects page, a card per project (a phone keeps its list below).
  if (r.name === "projects" && classic && desktop) return <ClassicProjectsPage />;
  switch (r.name) {
    case "overview":
      // Classic, on a phone: Python's mobile Home (pages/phone-home.tsx).
      if (phone) return <PhoneHome />;
      // Classic, on a computer: the start page is the Architect's conversation, as in Python
      // (which has no Home on a computer). A phone keeps Home.
      if (classic && desktop && headId) return <AgentPage key={headId} id={headId} />;
      return <OverviewPage />;
    case "inbox":
      return <InboxPage />;
    case "shells":
      return <ShellsPage />;
    case "matrix":
      return <MatrixPage />;
    case "settings":
      return <SettingsPage />;
    case "profile":
      return <ProfilePage key={r.slug} slug={r.slug} />;
    case "agent":
      return <AgentPage key={r.id} id={r.id} tab={r.tab} />;
    case "project":
      return <ProjectPage key={r.id} id={r.id} />;
    case "agents":
      return <AgentsPage />;
    case "projects":
      return <ProjectsPage />;
    case "chats":
      return <ChatsPage />;
    case "chat":
      return <ChatPage key={r.id} id={r.id} view={r.view} />;
    case "machine":
      return <MachinePage key={r.host} host={r.host} />;
    case "watchdog":
      return <WatchdogPage machine={r.machine} />;
    case "team":
      return <TeamPage />;
    case "recordings":
      return <RecordingsPage id={r.id} />;
    case "whatsapp":
      return <WhatsAppPage jid={r.jid} />;
    case "companion":
      return <CompanionPage />;
    case "features":
      return <FeaturesPage />;
    case "resources":
      return <ResourcesPage id={r.id} />;
    case "usage":
      return <UsagePage />;
    case "people":
      return <PeopleBookPage key="people" page="people" />;
    case "clients":
      return <PeopleBookPage key="clients" page="clients" />;
    case "promises":
      // Classic: Promises is not a place any more (the owner, 17 Nov 18:55: the Matrix replaced it); the
      // address shows the old records, read-only. The default dashboard keeps its page.
      return classic ? <OldPromisesPage /> : <PromisesPage />;
    default:
      return (
        <div className="flex flex-1 items-center justify-center p-6">
          <EmptyState icon={Search} title="Nothing here" className="w-full max-w-md border-none bg-transparent">
            <a className="text-brand hover:underline" href={href(paths.overview())}>
              Back to overview
            </a>
          </EmptyState>
        </div>
      );
  }
}

function MobileBar() {
  const machine = useStore((s) => s.machine);
  const pending = pendingApprovals(useStore((s) => s.approvals)).length;
  return (
    <div className="flex h-12 shrink-0 items-center gap-1 border-b bg-sidebar px-2 md:hidden">
      <Button variant="ghost" size="icon-sm" aria-label="Open navigation" onClick={() => ui.openMobileNav(true)}>
        <Menu className="size-4" />
      </Button>
      <BrandMark className="ml-0.5" />
      <span className="truncate text-[0.8125rem] font-semibold">{machine || "tmx"}</span>
      <div className="ml-auto flex items-center gap-0.5">
        <Button variant="ghost" size="icon-sm" aria-label="Search" onClick={() => ui.openPalette(true)}>
          <Search className="size-4" />
        </Button>
        <ConnectPhoneButton />
        <Button variant="ghost" size="sm" aria-label="Needs you" asChild className="gap-1.5 px-2">
          <a href={href(paths.inbox())}>
            <Inbox className="size-4" />
            {pending > 0 && <span className="tabular rounded-full bg-state-waiting px-1.5 text-2xs font-semibold text-black/80">{pending}</span>}
          </a>
        </Button>
      </div>
    </div>
  );
}

function MobileNav() {
  const open = useUI((s) => s.mobileNav);
  return (
    <Sheet open={open} onOpenChange={(v) => ui.openMobileNav(v)}>
      <SheetContent side="left" showCloseButton={false} className="w-[18.125rem] gap-0 bg-sidebar p-0 sm:max-w-[18.125rem]" aria-describedby={undefined}>
        <SheetTitle className="sr-only">Navigation</SheetTitle>
        <SheetDescription className="sr-only">Agents and projects on this machine</SheetDescription>
        <SidebarContent onNavigate={() => ui.openMobileNav(false)} />
      </SheetContent>
    </Sheet>
  );
}

// Closing a toast (its x, "Dismiss all", or its timeout) only hides it: the card stays in Needs
// you. A toast also goes away by itself once its card is no longer waiting.
function useApprovalToasts() {
  useEffect(() => {
    // The cards whose toast is showing, each with the function that (re)draws its toast: with
    // or without "Dismiss all". A toast drawn alone is redrawn with it when a second one comes.
    const open = new Map<string, (stacked: boolean) => void>();
    const closed = (id: string) => () => void open.delete(id);
    const dismissAll = () => [...open.keys()].forEach((id) => toast.dismiss(approvalToastId(id)));
    const offCreated = onApprovalCreated((a) => {
      // Classic: no toasts; as in Python the count on "Your turn" is the signal (architect, 17 Nov).
      if (classicNow()) return;
      if (!shouldToastApproval(a)) return;
      const st = useStore.getState();
      const agent = st.agents[a.agent_id];
      const h = window.location.hash;
      if (h.startsWith("#/inbox") || (agent && h.startsWith("#" + paths.agent(agent.id))) || waSendShownAt(a, h)) return;
      const s = summarizeTool(a.tool, a.input, agent?.cwd);
      const show = (stacked: boolean) =>
        toast(`${agent?.name ?? "An agent"} ${approvalHeadline(a)}`, {
          id: approvalToastId(a.id),
          description: a.kind === "question" || a.kind === "whatsapp" ? a.question : a.kind === "brake" ? a.description : [s.label, s.detail].filter(Boolean).join(" · "),
          action: { label: "Review", onClick: () => navigate(paths.inbox() + "?id=" + a.id) },
          cancel: stacked ? { label: "Dismiss all", onClick: dismissAll } : undefined,
          duration: 8000,
          onDismiss: closed(a.id),
          onAutoClose: closed(a.id),
        });
      if (open.size === 1) for (const redraw of open.values()) redraw(true); // the one that was alone
      open.set(a.id, show);
      show(open.size > 1);
    });
    const offStore = useStore.subscribe((st) => {
      for (const id of staleApprovalToasts(open.keys(), st.approvals)) {
        open.delete(id);
        toast.dismiss(approvalToastId(id));
      }
    });
    return () => {
      offCreated();
      offStore();
    };
  }, []);
}

function Boot() {
  return (
    <div className="flex h-full">
      <div className="hidden w-[16.25rem] shrink-0 flex-col gap-3 border-r bg-sidebar p-3 pt-[3.75rem] md:flex">
        <Skeleton className="h-16 rounded-lg" />
        <Skeleton className="h-6 w-3/4" />
        <Skeleton className="h-6 w-2/3" />
        <Skeleton className="mt-4 h-5 w-1/2" />
        <Skeleton className="h-5 w-2/3" />
        <Skeleton className="h-5 w-3/5" />
      </div>
      <div className="flex-1 p-8 pt-[4.5rem]">
        <div className="mx-auto max-w-[65rem] space-y-6">
          <Skeleton className="h-40 rounded-xl" />
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
            <Skeleton className="h-32 rounded-xl" />
            <Skeleton className="h-32 rounded-xl" />
            <Skeleton className="h-32 rounded-xl" />
          </div>
        </div>
      </div>
    </div>
  );
}

// A phone's camera opened https://…/#pair=<code> (Connect a phone): the code leaves the address
// bar before anything else reads the hash. A browser that is already paired just opens tmx.
const scannedCode = takePairCode();

function Unauthorized() {
  const msg = useStore((s) => s.statusMessage);
  const remote = remotePage(window.location.hostname, useStore((s) => !!s.transport?.desktop));
  return (
    <div className="drag-region flex h-full items-center justify-center p-6">
      {scannedCode ? <ScanPair code={scannedCode} /> : window.location.protocol === "https:" ? <PairDevice /> : remote ? (
        <EmptyState icon={Smartphone} title="Connect this phone" className="w-full max-w-md">
          On your computer, open tmx, click Connect a phone and point this phone's camera at the code.
        </EmptyState>
      ) : (
        <EmptyState icon={KeyRound} title="This page needs your owner token" className="w-full max-w-md">
          In a terminal on this computer, run <code className="rounded border bg-muted px-1 font-mono text-xs">tmx open --launch</code>: it
          opens tmx here, signed in. (<code className="rounded border bg-muted px-1 font-mono text-xs">tmx open</code> prints the link
          instead.) The token stays in this tab only.
          {msg && <p className="mt-3 text-xs text-subtle-foreground">{msg}</p>}
        </EmptyState>
      )}
    </div>
  );
}

// Dialogs can contain local form state that a version-triggered reload would discard.
function useDialogReloadHold() {
  useEffect(() => {
    let release: (() => void) | undefined;
    function update() {
      if (document.querySelector('[role="dialog"][data-state="open"]')) {
        release ??= reloadGate.hold();
      } else {
        release?.();
        release = undefined;
      }
    }
    const observer = new MutationObserver(update);
    observer.observe(document.body, { subtree: true, childList: true, attributes: true, attributeFilter: ["data-state"] });
    update();
    return () => {
      observer.disconnect();
      release?.();
    };
  }, []);
}

export default function App() {
  const status = useStore((s) => s.status);
  const conn = useStore((s) => s.conn);
  const transport = useStore((s) => s.transport);
  const disconnected = useDisconnected();
  const unavailable = status === "offline" || (status === "ready" && disconnected);
  useEffect(startDictationOutbox, []);
  useGlobalHotkeys(!unavailable);
  useApprovalToasts();
  useDialogReloadHold();
  useEffect(() => {
    if (status === "ready") {
      void loadSafety(); // the gate's state decides how Allow looks
      void loadUpdate(); // the sidebar shows when a release is on its way
      void offerOnboarding(useStore.getState().transport); // the first-run setup, once, in the computer's own window
    }
  }, [status]);
  const desktopMac = !!transport?.desktop && transport.platform === "darwin";
  // Classic: Python's text size (the root at 100%, where Normal is otherwise 125%).
  const classic = useClassic();
  useEffect(() => {
    if (status === "ready") setClassicText(classic);
  }, [classic, status]);
  // Classic on a phone is Python's mobile app: no top bar and no menu, a bottom bar instead
  // (components/phone/bottom-bar.tsx). With the profile off a phone is as it was.
  const phone = usePhoneClassic();

  return (
    <PhoneNotificationsProvider><TooltipProvider>
      {status === "booting" || (status === "ready" && conn === "connecting") ? (
        <Boot />
      ) : status === "locked" ? (
        <div className="h-full" /> // a locked phone: the lock screen below covers the page
      ) : status === "unauthorized" ? (
        <Unauthorized />
      ) : status === "offline" ? (
        <Boot />
      ) : (
        <div className="flex h-full min-h-0 overflow-hidden">
          <Sidebar desktopMac={desktopMac} />
          <main className="flex min-w-0 flex-1 flex-col" data-phone-classic={phone ? "" : undefined}>
            <RecordingBar />
            <DictationBar />
            <RestartBanner />
            <WindowUpdateBanner />
            <PendingUIUpdate />
            <PhoneNotificationBanner />
            {!phone && <MobileBar />}
            {!phone && <MachinesBar />}
            <MessagePinsStrip />
            <Routes />
            <PhoneBottomBar />
          </main>
          <MobileNav />
          <CommandPalette />
          <ShortcutsDialog />
          <NewProjectDialog />
          <ConnectPhoneDialog />
          <NewAgentHost />
          <HandOffDialog />
          <InviteDialog />
          <JoinDialog />
          <OnboardingDialog />
          <OwnerConfirmDialog />
          <StartRecordingDialog />
          <CameraTile />
        </div>
      )}
      <PhoneLock />
      <Toaster position="bottom-right" closeButton />
      {unavailable && <DaemonUnavailable />}
    </TooltipProvider></PhoneNotificationsProvider>
  );
}
