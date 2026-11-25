import { usePath } from "@/lib/router";
import { useClassic } from "@/hooks/use-classic";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useEffect, useState } from "react";
import { ProcessList } from "@/components/process-list";
import { ExternalLink, Laptop, Moon, Sun, WandSparkles } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectSeparator, SelectTrigger, SelectValue } from "@/components/ui/select";
import { CopyButton } from "@/components/copy-button";
import { DiscoverSection } from "@/components/discover-section";
import { PageBody, PageHeader, PageTitle, Section } from "@/components/page";
import { P2PSection } from "@/components/p2p-settings";
import { ProfilesSection } from "@/components/profiles-section";
import { FleetRulesSection } from "@/components/fleet-rules-settings";
import { SafetySection } from "@/components/safety-settings";
import { PhoneNotifications } from "@/components/phone-notifications";
import { OwnerDevices } from "@/components/device-pairing";
import { CompanionSection } from "@/components/companion/settings";
import { UpdateSection } from "@/components/update-settings";
import { VoiceSection } from "@/components/voice-settings";
import { RecordingSettingsSection } from "@/components/recording-settings";
import { WhatsAppSection } from "@/components/whatsapp/wa-settings";
import { ResourceSettings } from "@/components/resources/resource-settings";
import { Truncate } from "@/components/truncate";
import { displayPath, modelName } from "@/lib/format";
import { api } from "@/lib/api";
import { useStore } from "@/lib/store";
import { isComputerWindow } from "@/lib/onboarding-rules";
import { ui } from "@/lib/ui";
import { setTheme, useTheme, type ThemePref } from "@/lib/theme";
import { applyTextSize, savedTextSize, TEXT_SIZES, type TextSize } from "@/lib/text-size";
import { cn } from "@/lib/utils";
import { KNOWN_MODELS } from "@/lib/models";
import { chatApi } from "@/lib/chats";
import { toast } from "sonner";

// the model choices live in lib/models.ts (Fable 5.2 added, the owner #11); re-exported for the profile editor
export { KNOWN_MODELS };

export const MODES: { value: string; label: string; hint: string }[] = [
  { value: "auto", label: "Auto", hint: "Claude's classifier decides; anything it escalates comes to Needs you." },
  { value: "default", label: "Ask", hint: "Every tool that needs permission comes to Needs you." },
  { value: "acceptEdits", label: "Accept edits", hint: "File edits are allowed; commands still ask." },
  { value: "plan", label: "Plan only", hint: "The agent plans and reads but changes nothing." },
  { value: "dontAsk", label: "Don't ask", hint: "Anything not explicitly allowed is refused without asking you." },
  { value: "bypassPermissions", label: "Bypass permissions", hint: "Nothing asks. The agent can run any command." },
];

export function Row({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-2 py-3 sm:flex-row sm:items-start sm:gap-6">
      <div className="min-w-0 sm:w-56 sm:shrink-0 sm:pt-1.5">
        <div className="text-sm">{label}</div>
        {hint && <div className="mt-0.5 text-xs text-muted-foreground">{hint}</div>}
      </div>
      <div className="min-w-0 flex-1">{children}</div>
    </div>
  );
}

export function ModelPicker({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const known = KNOWN_MODELS.includes(value);
  const [custom, setCustom] = useState(!known);
  return (
    <div className="flex flex-col gap-2 sm:flex-row">
      <Select
        value={custom ? "__custom" : value}
        onValueChange={(v) => {
          if (v === "__custom") setCustom(true);
          else {
            setCustom(false);
            onChange(v);
          }
        }}
      >
        <SelectTrigger className="w-full sm:w-72">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {KNOWN_MODELS.map((m) => (
            <SelectItem key={m} value={m}>
              <span>{modelName(m)}</span>
              <span className="font-mono text-2xs text-muted-foreground">{m}</span>
            </SelectItem>
          ))}
          <SelectSeparator />
          <SelectItem value="__custom">Other model ID…</SelectItem>
        </SelectContent>
      </Select>
      {custom && (
        <Input value={value} onChange={(e) => onChange(e.target.value.trim())} placeholder="claude-…" className="font-mono text-[0.7812rem] sm:w-64" spellCheck={false} autoFocus />
      )}
    </div>
  );
}

function ThemePicker() {
  const { pref } = useTheme();
  const opts: { v: ThemePref; label: string; icon: React.ComponentType<{ className?: string }> }[] = [
    { v: "system", label: "System", icon: Laptop },
    { v: "light", label: "Light", icon: Sun },
    { v: "dark", label: "Dark", icon: Moon },
  ];
  return (
    <div role="radiogroup" aria-label="Theme" className="inline-flex rounded-lg border bg-muted/40 p-0.5">
      {opts.map((o) => (
        <button
          key={o.v}
          role="radio"
          aria-checked={pref === o.v}
          onClick={() => setTheme(o.v)}
          className={cn(
            "flex h-7 items-center gap-1.5 rounded-md px-3 text-[0.7812rem] text-muted-foreground outline-none transition-colors duration-150 hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring",
            pref === o.v && "bg-background font-medium text-foreground shadow-sm dark:bg-accent",
          )}
        >
          <o.icon className="size-3.5" />
          {o.label}
        </button>
      ))}
    </div>
  );
}

/** Text size, per device (lib/text-size.ts): the whole UI scales with it. */
function TextSizePicker() {
  const [size, setSize] = useState<TextSize>(savedTextSize);
  return (
    <div role="radiogroup" aria-label="Text size" className="inline-flex rounded-lg border bg-muted/40 p-0.5">
      {TEXT_SIZES.map((o) => (
        <button
          key={o.id}
          role="radio"
          aria-checked={size === o.id}
          onClick={() => {
            applyTextSize(o.id);
            setSize(o.id);
          }}
          className={cn(
            "flex h-7 items-center rounded-md px-3 text-[0.7812rem] text-muted-foreground outline-none transition-colors duration-150 hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring",
            size === o.id && "bg-background font-medium text-foreground shadow-sm dark:bg-accent",
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

/** The name other people see in invites and chats (PUT /api/p2p/name). */
function ChatNameRow() {
  const [saved, setSaved] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [off, setOff] = useState(false);
  useEffect(() => {
    chatApi.p2p().then(
      (s) => {
        setOff(!s.enabled);
        setSaved(s.name ?? "");
        setName(s.name ?? "");
      },
      () => setOff(true),
    );
  }, []);
  if (off) return null;
  const clean = name.trim().replace(/\s+/g, " ");
  const dirty = saved !== null && clean !== saved && clean.length > 0;
  async function save() {
    setBusy(true);
    try {
      const r = await chatApi.setName(clean);
      setSaved(r.name);
      setName(r.name);
      toast.success("Saved. New invites and messages use this name.");
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Row label="Name in chats" hint="What other people see in your invites and messages. People who already have you as a contact keep the name they gave you.">
      <form
        className="flex max-w-sm items-center gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          if (dirty) void save();
        }}
      >
        <Input value={name} maxLength={60} onChange={(e) => setName(e.target.value)} disabled={saved === null || busy} aria-label="Name in chats" />
        <Button type="submit" size="sm" variant={dirty ? "default" : "outline"} disabled={!dirty || busy}>
          Save
        </Button>
      </form>
    </Row>
  );
}

function HeadHarnessSection() {
  const classic = useClassic();
  const [value, setValue] = useState<string>("claude");
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    api.getConfig().then((c) => {
      setValue(c.head_harness || "claude");
      setLoaded(true);
    }).catch(() => {});
  }, []);
  if (!loaded) return null;
  async function onChange(v: string) {
    const prev = value;
    setValue(v);
    setBusy(true);
    try {
      await api.putConfig({ head_harness: v }); // owner gate: may ask for a confirmation first
      toast.success("Saved. Restart the daemon (tmx down && tmx up) for this to take effect.");
    } catch (e) {
      setValue(prev); // not saved (refused, or the confirmation was cancelled)
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Section title={classic ? "Architect" : "Head agent"}>
      <div className="rounded-xl border bg-card px-4 py-1">
        <Row label={classic ? "Architect harness" : "Head harness"} hint={classic ? "The harness for the Architect. Chat-capable harnesses only. Takes effect after a daemon restart." : "The harness for the head agent (the top of the tree). Chat-capable harnesses only. Takes effect after a daemon restart."}>
          <Select value={value} onValueChange={onChange} disabled={busy}>
            <SelectTrigger className="w-full sm:w-72">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="claude">Claude Code</SelectItem>
              <SelectItem value="codex">Codex</SelectItem>
            </SelectContent>
          </Select>
        </Row>
      </div>
    </Section>
  );
}

const SETTINGS_SECTIONS = [
  ["phone-notifications", "Phone notifications"], ["system-tools", "System tools"],
  ["project-tools", "Project tools"], ["profiles", "Profiles"], ["fleet-rules", "Fleet rules"], ["head-agent", "Head agent"],
  ["safety", "Safety"], ["owner-devices", "Owner devices"], ["companion", "Companion"],
  ["peer-to-peer", "Peer-to-peer"], ["resources", "Machine load"], ["harnesses", "Harnesses"],
  ["running-outside-tmx", "Running outside tmx"], ["updates", "Updates"], ["voice", "Voice"],
  ["recordings", "Recordings"], ["whatsapp", "WhatsApp"], ["setup", "Setup"], ["appearance", "Appearance"],
  ["this-machine", "This machine"],
];

const CLASSIC_TABS = [
  ["agents", "Agents", ["profiles", "fleet-rules", "head-agent", "harnesses", "running-outside-tmx"]],
  ["safety", "Safety", ["safety"]],
  ["resources", "Machine load", ["resources"]],
  ["integrations", "Integrations", ["companion", "peer-to-peer", "whatsapp"]],
  ["voice", "Voice & recordings", ["voice", "recordings"]],
  ["updates", "Updates", ["updates"]],
  ["general", "General", ["phone-notifications", "owner-devices", "setup", "appearance", "this-machine"]],
] as const;

export function SettingsPage() {
  const classic = useClassic();
  const path = usePath();
  const [section, setSection] = useState(() => new URLSearchParams(window.location.hash.split("?")[1] ?? "").get("section") || "phone-notifications");
  const [tab, setTab] = useState(() => CLASSIC_TABS.find(([, , sections]) => (sections as readonly string[]).includes(new URLSearchParams(window.location.hash.split("?")[1] ?? "").get("section") ?? ""))?.[0] ?? "agents");
  const show = (group: string) => !classic || tab === group;
  const workspace = useStore((s) => s.workspace);
  const machine = useStore((s) => s.machine);
  const version = useStore((s) => s.version);
  const transport = useStore((s) => s.transport);

  useEffect(() => {
    // #/settings?section=companion scrolls to that section.
    const section = new URLSearchParams(window.location.hash.split("?")[1] ?? "").get("section");
    if (classic) setTab(CLASSIC_TABS.find(([, , sections]) => (sections as readonly string[]).includes(section ?? ""))?.[0] ?? "agents");
    const timer = section ? setTimeout(() => document.getElementById(section)?.scrollIntoView({ block: "start" }), 50) : undefined;
    return () => clearTimeout(timer);
  }, [path, classic]);

  const browserURL = transport ? `${transport.baseURL}/#token=${encodeURIComponent(transport.token)}` : "";
  const via = transport?.kind === "mock" ? "Mock data" : transport?.kind === "bridge" ? "Desktop bridge" : transport?.desktop ? "Direct (desktop)" : "Direct";

  const content = (
      <PageBody width="max-w-none" className="space-y-10">
        {show("general") && <PhoneNotifications />}
        {!classic && <ProcessList />}
        {!classic && <ProcessList projectTools />}
        {show("agents") && <ProfilesSection />}
        {show("agents") && <FleetRulesSection />}

        {show("agents") && <HeadHarnessSection />}

        {show("safety") && <SafetySection />}
        {show("general") && <OwnerDevices />}

        {show("integrations") && <div id="companion" className="scroll-mt-6">
          <CompanionSection />
        </div>}

        {show("integrations") && <P2PSection />}

        {show("resources") && <ResourceSettings />}
        {show("agents") && <DiscoverSection />}

        {show("updates") && <UpdateSection />}

        {show("voice") && <VoiceSection />}

        {show("voice") && <RecordingSettingsSection />}
        {show("integrations") && <WhatsAppSection />}

        {show("general") && isComputerWindow(transport) && <Section title="Setup">
          <div className="rounded-xl border bg-card px-4 py-1">
            <Row label="Set up tmx" hint="Connect WhatsApp, your phone and Companion, one easy step at a time. Anything you skipped is still here.">
              <Button variant="outline" size="sm" onClick={() => ui.openOnboarding(true)}>
                <WandSparkles />
                Run setup again
              </Button>
            </Row>
          </div>
        </Section>}

        {show("general") && <Section title="Appearance">
          <div className="rounded-xl border bg-card px-4 py-1">
            <Row label="Theme" hint="System follows your OS setting.">
              <ThemePicker />
            </Row>
            <Row label="Text size" hint="For this device. Every page grows with it; no zooming needed.">
              <TextSizePicker />
            </Row>
          </div>
        </Section>}

        {show("general") && <Section title="This machine">
          <div className="divide-y rounded-xl border bg-card px-4">
            <Row label="Machine">
              <p className="pt-1.5 text-sm">{machine}</p>
            </Row>
            <ChatNameRow />
            <Row label="Workspace" hint="New project folders go here.">
              <div className="flex min-w-0 items-center gap-1 pt-1">
                <Truncate className="font-mono text-[0.7812rem]" title={workspace}>
                  {displayPath(workspace)}
                </Truncate>
                <CopyButton text={workspace} label="Copy path" />
              </div>
            </Row>
            <Row label="Open in a browser" hint="The same UI, served by the daemon. The link carries your owner token: keep it to yourself.">
              <div className="flex flex-wrap items-center gap-1.5">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => transport?.openExternal(browserURL)}
                  disabled={!transport || transport.kind === "mock"}
                >
                  <ExternalLink />
                  Open in browser
                </Button>
                <CopyButton text={browserURL} label="Copy link" />
                <span className="font-mono text-2xs text-subtle-foreground">{transport?.baseURL}</span>
              </div>
            </Row>
            <Row label="Connection">
              <p className="pt-1.5 text-sm">{via}</p>
            </Row>
            <Row label="Version">
              <p className="pt-1.5 font-mono text-[0.7812rem]">{version}</p>
            </Row>
          </div>
        </Section>}
      </PageBody>
  );

  return (
    <Tabs value={tab} onValueChange={(v) => setTab(v as typeof tab)} className={cn("flex min-h-0 min-w-0 flex-1 flex-col gap-0", !classic && "settings-page")}>
      <PageHeader>
        <PageTitle>Settings</PageTitle>
      </PageHeader>
      {classic && <div className="shrink-0 overflow-x-auto border-b px-4">
        <TabsList variant="line" aria-label="Settings sections">
          {CLASSIC_TABS.map(([id, label]) => <TabsTrigger key={id} value={id} className="min-h-[34px] px-3">{label}</TabsTrigger>)}
        </TabsList>
      </div>}
      {!classic && <nav aria-label="Settings sections" className="shrink-0 border-b px-4 py-2 md:hidden">
        <label htmlFor="settings-section" className="sr-only">Settings section</label>
        <select id="settings-section" value={section} className="min-h-11 w-full min-w-0 rounded-md border bg-background px-3 text-[18px]" onChange={e => {
          setSection(e.target.value);
          document.getElementById(e.target.value)?.scrollIntoView({ block: "start" });
        }}>
          {SETTINGS_SECTIONS.filter(([id]) => id !== "setup" || isComputerWindow(transport)).map(([id, label]) => <option key={id} value={id}>{label}</option>)}
        </select>
      </nav>}
      {classic ? <TabsContent value={tab} className="mt-0 flex min-h-0 flex-1 flex-col">{content}</TabsContent> : content}
    </Tabs>
  );
}
