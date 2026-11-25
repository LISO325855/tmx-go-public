// "Shells & scripts" on Projects, a project, and Shells (lib/machine-registry.ts):
// every shell, tool and long-running script the project's agents started on this machine, what was
// left running first and flagged, each with who started it and since when, and the buttons the
// daemon allows (Stop, Restart). Loading and unavailable states keep the card discoverable.
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { relativeTime } from "@/lib/format";
import { canAct, isOrphan, kindWord, listedProcesses, registryMissing, registrySummary, stateWord, type MachineProcess } from "@/lib/machine-registry";
import { href, paths } from "@/lib/router";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";

const REFRESH_MS = 30_000;

/** The list itself: no loading of its own, so it can be rendered and checked without a daemon. */
export function ShellsList({
  entries,
  now,
  busy,
  onStop,
  onRestart,
}: {
  entries: MachineProcess[];
  now: number;
  /** the entry an action is running on */
  busy?: string | null;
  onStop: (e: MachineProcess) => void;
  onRestart: (e: MachineProcess) => void;
}) {
  const agents = useStore((s) => s.agents);
  // Stop is asked twice, on the row: it ends a running program.
  const [asking, setAsking] = useState<string | null>(null);
  const rows = listedProcesses(entries);

  return (
    <section className="rounded-xl border bg-card px-4 py-3.5" data-project-card-section="shells">
      <h2 className="mb-1 text-2xs font-semibold tracking-[0.12em] text-subtle-foreground uppercase">Shells &amp; scripts</h2>
      <p className="mb-2.5 text-xs text-muted-foreground" data-shells-summary>
        {registrySummary(entries)}
      </p>
      {rows.length > 0 && (
        <ul className="divide-y">
          {rows.map((e) => {
            const orphan = isOrphan(e);
            const known = e.agent_id ? agents[e.agent_id] : undefined;
            const working = busy === e.id;
            return (
              <li key={e.id} className={cn("flex flex-wrap items-center gap-x-3 gap-y-1.5 py-2", orphan && "-mx-2 rounded-md border-l-4 border-l-state-error px-2")} data-shell-row={e.id} data-orphan={orphan || undefined}>
                <div className="min-w-0 flex-1 basis-56">
                  <p className="text-sm [overflow-wrap:anywhere]">
                    <span className="font-medium">{e.what || e.command || kindWord(e.kind)}</span>
                    {e.command && e.command !== e.what && <span className="ml-2 font-mono text-xs text-muted-foreground">{e.command}</span>}
                  </p>
                  <p className="mt-0.5 flex flex-wrap gap-x-1.5 text-xs text-muted-foreground">
                    <span>{kindWord(e.kind)}</span>
                    <span aria-hidden>·</span>
                    <span className={cn(orphan && "font-semibold text-state-error")}>{stateWord(e)}</span>
                    {(e.agent || known) && (
                      <>
                        <span aria-hidden>·</span>
                        {known ? (
                          <a href={href(paths.agent(known.id))} className="hover:text-foreground hover:underline">
                            {known.name}
                          </a>
                        ) : (
                          <span>{e.agent}</span>
                        )}
                      </>
                    )}
                    {e.since > 0 && (
                      <>
                        <span aria-hidden>·</span>
                        <span>since {relativeTime(e.since, now)}</span>
                      </>
                    )}
                    {!!e.restarts && (
                      <>
                        <span aria-hidden>·</span>
                        <span>restarted {e.restarts}×</span>
                      </>
                    )}
                  </p>
                  {/* why it is flagged, in the daemon's words */}
                  {e.why && <p className="mt-0.5 text-xs [overflow-wrap:anywhere]">{e.why}</p>}
                </div>
                <div className="flex shrink-0 items-center gap-1.5">
                  {asking === e.id ? (
                    <>
                      <span className="text-xs text-muted-foreground">Stop it?</span>
                      <Button
                        size="sm"
                        variant="destructive"
                        disabled={working}
                        onClick={() => {
                          setAsking(null);
                          onStop(e);
                        }}
                      >
                        Yes, stop
                      </Button>
                      <Button size="sm" variant="outline" onClick={() => setAsking(null)}>
                        No
                      </Button>
                    </>
                  ) : (
                    <>
                      {canAct(e, "restart") && (
                        <Button size="sm" variant="outline" disabled={working} onClick={() => onRestart(e)}>
                          Restart
                        </Button>
                      )}
                      {canAct(e, "stop") && (
                        <Button size="sm" variant="outline" disabled={working} onClick={() => setAsking(e.id)}>
                          Stop
                        </Button>
                      )}
                    </>
                  )}
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}

/** The project list, or all entries when no project is selected, kept fresh. */
export function ProjectShells({ projectId }: { projectId?: string }) {
  const [entries, setEntries] = useState<MachineProcess[] | null>(null);
  const [missing, setMissing] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [now, setNow] = useState(() => Date.now());

  const load = useCallback(async () => {
    try {
      const reg = await api.registry(projectId);
      setEntries(reg.entries ?? []);
      setMissing(false);
      setError("");
    } catch (err) {
      if (registryMissing(err)) setMissing(true);
      else setError((err as Error).message || "Could not load shells and scripts.");
      // Show the failure with a retry; the next scheduled refresh also tries again.
    }
    setNow(Date.now());
  }, [projectId]);

  useEffect(() => {
    void load();
    const timer = setInterval(() => void load(), REFRESH_MS);
    return () => clearInterval(timer);
  }, [load]);

  const act = async (e: MachineProcess, what: "stop" | "restart") => {
    setBusy(e.id);
    try {
      if (what === "stop") await api.registryStop(e.id);
      else await api.registryRestart(e.id);
      toast.success(what === "stop" ? `Stopped: ${e.what || e.command}` : `Restarted: ${e.what || e.command}`);
    } catch (err) {
      toast.error((err as Error).message || `Could not ${what} it.`);
    }
    setBusy(null);
    await load();
  };

  if (missing || error || entries === null) return (
    <section className="rounded-xl border bg-card px-4 py-3.5" data-project-card-section="shells">
      <h2 className="mb-1 text-2xs font-semibold tracking-[0.12em] text-subtle-foreground uppercase">Shells &amp; scripts</h2>
      <p className="text-xs text-muted-foreground" role="status">{missing ? "Shells and scripts are unavailable on this machine." : error || "Loading shells and scripts..."}</p>
      {(missing || error) && <Button size="sm" variant="outline" className="mt-2" onClick={() => void load()}>Retry</Button>}
    </section>
  );
  return <ShellsList entries={entries} now={now} busy={busy} onStop={(e) => void act(e, "stop")} onRestart={(e) => void act(e, "restart")} />;
}
