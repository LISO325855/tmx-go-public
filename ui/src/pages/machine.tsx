// A machine's page (#/machines/<name>): one of the owner's other machines, from this window.
// Its agents as this daemon last read them (the fleet roster), and for each: its screen, a few
// keys to answer a prompt, stop / start / restart, and mail to it - all through this daemon's
// relay, which admits them for the owner only. A new agent can be launched there too. Everything
// shown comes from another machine: plain text only, never HTML.
import { useEffect, useState } from "react";
import { MonitorSmartphone, Plus, RefreshCw } from "lucide-react";
import { EmptyState, PageBody, PageHeader, PageTitle, Section } from "@/components/page";
import { StateDot } from "@/components/state";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { useFleetHosts, watchFleetHosts, type HostSummary } from "@/lib/fleet-hosts";
import { machineDetail, machineTone } from "@/lib/fleet-hosts-format";
import { remote, sendRemoteMail, useRoster, watchRoster } from "@/lib/machines";
import {
  QUICK_KEYS,
  byProject,
  dotState,
  launchProblem,
  launchRequest,
  machineSessions,
  resting,
  sessionRequest,
  sessionsLine,
  type LaunchForm,
  type RosterSession,
  type SessionAction,
} from "@/lib/machines-format";
import { href, paths } from "@/lib/router";
import { toast } from "sonner";

function fail(e: unknown) {
  toast.error(e instanceof Error ? e.message : String(e));
}

export function MachinePage({ host }: { host: string }) {
  const sessions = useRoster((s) => s.sessions);
  const loaded = useRoster((s) => s.loaded);
  const rosterError = useRoster((s) => s.error);
  const row = useFleetHosts((s) => s.hosts.find((h) => h.kind === "peer" && h.name === host));
  const hostsLoaded = useFleetHosts((s) => s.loaded);
  const [launching, setLaunching] = useState(false);
  useEffect(() => watchRoster(), []);
  useEffect(() => watchFleetHosts(), []);

  const agents = machineSessions(sessions, host);
  const tone = row ? machineTone(row) : null;
  const detail = row ? machineDetail(row, Date.now()).slice(1) : [];

  return (
    <>
      <PageHeader
        actions={
          row ? (
            <>
              <Button size="sm" variant="ghost" asChild>
                <a href={href(paths.watchdog(host))}>Watchdog</a>
              </Button>
              {tone === "online" && (
                <Button size="sm" variant="outline" onClick={() => setLaunching((v) => !v)} aria-expanded={launching}>
                  <Plus />
                  New agent there
                </Button>
              )}
            </>
          ) : undefined
        }
      >
        <PageTitle>{host}</PageTitle>
        {row && <span className="truncate text-sm text-muted-foreground">{tone === "online" ? sessionsLine(agents) : detail[0]}</span>}
      </PageHeader>
      <PageBody>
        {hostsLoaded && !row ? (
          <EmptyState icon={MonitorSmartphone} title="No such machine">
            This machine has no other machine called {host} (config.json "peers").
          </EmptyState>
        ) : (
          <div className="space-y-7">
            {row && tone === "online" && detail.length > 1 && (
              <p className="tabular text-xs text-muted-foreground">{detail.slice(1).join(" · ")}</p>
            )}
            {row && tone !== "online" && (
              <div className="rounded-xl border bg-card p-4 text-sm">
                {detail.map((line, i) => (
                  <p key={i} className="break-words text-muted-foreground">
                    {line}
                  </p>
                ))}
                {agents.length > 0 && <p className="mt-2 text-muted-foreground">Below: its agents as last seen. They cannot be reached until it answers again.</p>}
              </div>
            )}
            {rosterError && <p className="text-xs text-state-error">The list could not be refreshed: {rosterError}</p>}
            {launching && row && <LaunchCard row={row} onDone={() => setLaunching(false)} />}
            {loaded && agents.length === 0 ? (
              <EmptyState icon={MonitorSmartphone} title="No agents on this machine">
                {tone === "online" ? "It answers, and lists no agent." : "Nothing was ever read from it."}
              </EmptyState>
            ) : (
              byProject(agents).map((g) => (
                <Section key={g.project} title={g.project || "No project"} right={<span className="tabular text-2xs text-muted-foreground">{g.agents.length}</span>}>
                  <ul className="divide-y rounded-xl border bg-card">
                    {g.agents.map((a) => (
                      <AgentRow key={a.name} a={a} host={host} reachable={tone === "online"} />
                    ))}
                  </ul>
                </Section>
              ))
            )}
          </div>
        )}
      </PageBody>
    </>
  );
}

function AgentRow({ a, host, reachable }: { a: RosterSession; host: string; reachable: boolean }) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState("");
  const stopped = resting(a.state);

  async function act(action: SessionAction) {
    const req = sessionRequest(host, a.name, action);
    if (!req) return fail(new Error(`${a.name} cannot be addressed on another machine.`));
    setBusy(action);
    try {
      await remote(req);
      toast.success(`${a.name}@${host}: ${action} sent`);
    } catch (e) {
      fail(e);
    } finally {
      setBusy("");
    }
  }

  return (
    <li className="px-4 py-3">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1.5">
        <StateDot state={dotState(a.state)} />
        <span className="min-w-0 truncate text-sm font-medium">{a.name}</span>
        {a.tier && <span className="text-2xs text-subtle-foreground">{a.tier}</span>}
        <span className="text-2xs text-muted-foreground">{a.state}</span>
        {(a.queued_messages ?? 0) > 0 && <span className="tabular rounded-sm bg-state-waiting/15 px-1 text-2xs">{a.queued_messages} mail waiting</span>}
        <span className="ml-auto flex shrink-0 items-center gap-1">
          <Button size="xs" variant={open ? "secondary" : "ghost"} aria-expanded={open} onClick={() => setOpen((v) => !v)}>
            {open ? "Close" : "Open"}
          </Button>
          {stopped ? (
            <Button size="xs" variant="ghost" disabled={!reachable || busy !== ""} onClick={() => void act("start")}>
              Start
            </Button>
          ) : (
            <>
              <Button size="xs" variant="ghost" disabled={!reachable || busy !== ""} onClick={() => void act("restart")}>
                Restart
              </Button>
              <Button size="xs" variant="ghost" disabled={!reachable || busy !== ""} onClick={() => void act("stop")}>
                Stop
              </Button>
            </>
          )}
        </span>
      </div>
      {a.role && <p className="mt-1 truncate text-xs text-muted-foreground">{a.role}</p>}
      {open && <AgentPanel a={a} host={host} reachable={reachable} />}
    </li>
  );
}

function AgentPanel({ a, host, reachable }: { a: RosterSession; host: string; reachable: boolean }) {
  const [text, setText] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [mail, setMail] = useState("");
  const [sending, setSending] = useState(false);
  const canScreen = reachable && !resting(a.state);

  async function refresh() {
    const req = sessionRequest(host, a.name, "screen");
    if (!req) return setError(`${a.name} cannot be addressed on another machine.`);
    setLoading(true);
    try {
      const v = await remote<{ text?: string }>(req);
      setText(typeof v.text === "string" ? v.text : "");
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }

  // Read the screen once when the panel opens; after that only on Refresh or a key.
  useEffect(() => {
    if (canScreen) void refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function press(keys: string) {
    const req = sessionRequest(host, a.name, "keys", keys);
    if (!req) return;
    try {
      await remote(req);
      await refresh();
    } catch (e) {
      fail(e);
    }
  }

  async function send() {
    const body = mail.trim();
    if (!body) return;
    setSending(true);
    try {
      await sendRemoteMail(a.name, host, body);
      setMail("");
      toast.success(`Sent to ${a.name}@${host}`);
    } catch (e) {
      fail(e);
    } finally {
      setSending(false);
    }
  }

  return (
    <div className="mt-3 space-y-3">
      {a.cwd && <p className="tabular text-2xs break-all text-subtle-foreground">{a.cwd}</p>}
      {canScreen ? (
        <div>
          <div className="mb-1.5 flex flex-wrap items-center gap-1">
            <Button size="xs" variant="outline" disabled={loading} onClick={() => void refresh()}>
              <RefreshCw />
              Refresh
            </Button>
            <span className="mx-1 text-2xs text-subtle-foreground">Press</span>
            {QUICK_KEYS.map((k) => (
              <Button key={k.keys} size="xs" variant="ghost" onClick={() => void press(k.keys)}>
                {k.label}
              </Button>
            ))}
          </div>
          {error ? (
            <p className="text-xs text-state-error">{error}</p>
          ) : (
            <pre className="max-h-96 overflow-auto rounded-md border bg-muted/40 p-3 font-mono text-2xs leading-snug whitespace-pre">
              {text === null ? "Reading the screen…" : text || "(the screen is empty)"}
            </pre>
          )}
        </div>
      ) : (
        <p className="text-xs text-muted-foreground">{reachable ? "It is not running: there is no screen to show." : "The machine does not answer: no screen."}</p>
      )}
      <div className="flex flex-wrap items-end gap-2">
        <Textarea
          className="min-h-9 flex-1 basis-64"
          rows={1}
          placeholder={`Message to ${a.name}@${host}`}
          aria-label={`Message to ${a.name} on ${host}`}
          value={mail}
          onChange={(e) => setMail(e.target.value)}
        />
        <Button size="sm" disabled={sending || !mail.trim()} onClick={() => void send()}>
          Send
        </Button>
      </div>
    </div>
  );
}

function LaunchCard({ row, onDone }: { row: HostSummary; onDone: () => void }) {
  const goDaemon = row.daemon === "go";
  const [form, setForm] = useState<LaunchForm>({ name: "", cwd: "", role: "", task: "" });
  const [busy, setBusy] = useState(false);
  const problem = launchProblem(form, goDaemon);
  const set = (k: keyof LaunchForm) => (e: { target: { value: string } }) => setForm((f) => ({ ...f, [k]: e.target.value }));

  async function launch() {
    if (problem) return;
    setBusy(true);
    try {
      await remote(launchRequest(row.name, form, goDaemon));
      toast.success(`${form.name.trim()} is starting on ${row.name}`);
      onDone();
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Section title={`New agent on ${row.name}`}>
      <div className="space-y-2.5 rounded-xl border bg-card p-4">
        <div className="grid gap-2.5 sm:grid-cols-2">
          <Input placeholder="Name (e.g. docs-writer)" aria-label="Name" value={form.name} onChange={set("name")} />
          <Input placeholder="Role, in a few words (optional)" aria-label="Role" value={form.role} onChange={set("role")} />
        </div>
        <Input
          placeholder={goDaemon ? "Folder on that machine (optional: its workspace)" : "Folder on that machine"}
          aria-label="Folder on that machine"
          value={form.cwd}
          onChange={set("cwd")}
        />
        <Textarea rows={3} placeholder="What it should do" aria-label="Task" value={form.task} onChange={set("task")} />
        <div className="flex items-center gap-2">
          <Button size="sm" disabled={busy || problem !== null} onClick={() => void launch()}>
            Launch on {row.name}
          </Button>
          <Button size="sm" variant="ghost" onClick={onDone}>
            Cancel
          </Button>
          {problem && form.name !== "" && <span className="text-xs text-muted-foreground">{problem}</span>}
        </div>
      </div>
    </Section>
  );
}
