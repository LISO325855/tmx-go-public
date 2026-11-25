// Settings → Fleet rules: where this machine's standing rules come from, which commit is in
// force and whether its signature was checked, the documents (opened in place), and the agents
// that still run on older rules.
import { useEffect, useState } from "react";
import { CheckCircle2, ChevronDown, ChevronRight, CircleOff, Loader2, RefreshCw, TriangleAlert } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Markdown } from "@/components/markdown";
import { Section } from "@/components/page";
import { relativeTime } from "@/lib/format";
import { fleetRulesApi, loadFleetRules, useFleetRules } from "@/lib/fleet-rules";
import { behindLine, docGroups, rulesHeadline, shortCommit, signatureLine, type RulesDoc } from "@/lib/fleet-rules-format";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";

function when(iso: string | undefined): string {
  const ms = iso ? Date.parse(iso) : NaN;
  return Number.isNaN(ms) ? "" : relativeTime(ms);
}

function DocRow({ doc }: { doc: RulesDoc }) {
  const [open, setOpen] = useState(false);
  const [body, setBody] = useState<string | null>(null);
  const [failed, setFailed] = useState("");

  async function toggle() {
    const next = !open;
    setOpen(next);
    if (next && body === null) {
      try {
        setBody((await fleetRulesApi.doc(doc.name)).body ?? "");
      } catch (e) {
        setFailed((e as Error).message);
      }
    }
  }

  const meta = Object.entries(doc.meta ?? {}).filter(([k]) => !["title", "name", "summary"].includes(k));
  return (
    <li className="py-2">
      <button type="button" onClick={() => void toggle()} aria-expanded={open} className="flex w-full min-w-0 items-start gap-2 text-left">
        <span className="mt-0.5 shrink-0 text-muted-foreground">{open ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />}</span>
        <span className="min-w-0 flex-1">
          <span className="text-sm">{doc.title}</span>
          <span className="ml-2 font-mono text-2xs text-subtle-foreground">{doc.name}</span>
          {doc.summary && <span className="block text-xs text-muted-foreground [overflow-wrap:anywhere]">{doc.summary}</span>}
          <span className="block text-2xs text-subtle-foreground">
            last changed {when(doc.last_change?.time)} in {shortCommit(doc.last_change?.commit)}, author as written: {doc.last_change?.author}
          </span>
        </span>
      </button>
      {open && (
        <div className="mt-2 ml-6 min-w-0 rounded-lg border bg-background px-3 py-2">
          {meta.length > 0 && (
            <dl className="mb-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-xs">
              {meta.map(([k, v]) => (
                <div key={k} className="contents">
                  <dt className="text-muted-foreground">{k}</dt>
                  <dd className="min-w-0 [overflow-wrap:anywhere]">{v}</dd>
                </div>
              ))}
            </dl>
          )}
          {failed ? <p className="text-xs text-state-waiting">{failed}</p> : body === null ? <Loader2 className="size-4 animate-spin" /> : <Markdown text={body} className="text-sm" />}
        </div>
      )}
    </li>
  );
}

export function FleetRulesSection() {
  const v = useFleetRules((x) => x.view);
  const status = useStore((x) => x.status);
  const [busy, setBusy] = useState<string | null>(null);

  useEffect(() => {
    if (status === "ready") void loadFleetRules();
  }, [status]);

  if (!v) return null; // a daemon without fleet rules

  async function run(what: string, fn: () => Promise<unknown>, done?: string) {
    setBusy(what);
    try {
      await fn();
      if (done) toast.success(done);
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(null);
    }
  }

  const h = rulesHeadline(v);
  const behind = behindLine(v);
  const icon = h.tone === "ok" ? <CheckCircle2 className="size-4" /> : h.tone === "off" ? <CircleOff className="size-4" /> : <TriangleAlert className="size-4" />;
  return (
    <div id="fleet-rules" className="scroll-mt-6">
      <Section title="Fleet rules">
        <div className="divide-y rounded-xl border bg-card px-4">
          <div className="flex flex-col gap-3 py-3.5 sm:flex-row sm:items-center">
            <div className={cn("flex min-w-0 flex-1 items-start gap-2.5 text-sm", h.tone === "warn" && "text-state-waiting", h.tone === "off" && "text-muted-foreground")}>
              <span className="mt-0.5 shrink-0">{icon}</span>
              <div className="min-w-0">
                <p className="[overflow-wrap:anywhere]">{h.text}</p>
                {v.enabled && (
                  <p className="mt-0.5 text-xs text-muted-foreground [overflow-wrap:anywhere]">
                    From <span className="font-mono">{v.source}</span>, branch <span className="font-mono">{v.branch}</span>
                    {v.fetched_at ? ` · source last reached ${when(v.fetched_at)}` : " · source not reached yet"}
                    {v.pin ? ` · held at ${shortCommit(v.pin)}` : ""}
                  </p>
                )}
              </div>
            </div>
            {v.enabled && (
              <div className="flex shrink-0 flex-wrap items-center gap-1.5">
                {v.pin ? (
                  <Button size="sm" variant="outline" disabled={!!busy} onClick={() => void run("hold", () => fleetRulesApi.hold(""), "This machine follows the branch again.")}>
                    Follow the branch again
                  </Button>
                ) : (
                  v.loaded &&
                  v.head && (
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={!!busy}
                      title="Keep this machine on the commit in force, whatever is pushed to the source"
                      onClick={() => void run("hold", () => fleetRulesApi.hold(v.head!.commit), "This machine is held at this commit.")}
                    >
                      Hold at this commit
                    </Button>
                  )
                )}
                <Button size="sm" variant="outline" disabled={!!busy} onClick={() => void run("refresh", () => fleetRulesApi.refresh())}>
                  {busy === "refresh" ? <Loader2 className="animate-spin" /> : <RefreshCw />}
                  Look now
                </Button>
              </div>
            )}
          </div>
          {v.loaded && v.head && (
            <div className="py-3 text-xs text-muted-foreground">
              <p className="[overflow-wrap:anywhere]">
                Commit <span className="font-mono text-foreground">{shortCommit(v.head.commit)}</span> of {when(v.head.time)}: “{v.head.subject}”, author as written:{" "}
                {v.head.author}.
              </p>
              <p className={cn("mt-0.5", !v.signature && "text-state-waiting")}>{signatureLine(v)}</p>
              {v.signers.length > 0 && (
                <p className="mt-0.5 [overflow-wrap:anywhere]">
                  Pinned keys: {v.signers.map((s) => `${s.id} (${s.fingerprint})`).join(", ")}. Changing them needs your confirmation on this machine.
                </p>
              )}
              {behind && <p className="mt-0.5">{behind}</p>}
            </div>
          )}
          {docGroups(v.docs).map((g) => (
            <div key={g.title} className="py-3">
              <h3 className="text-xs font-medium text-muted-foreground">{g.title}</h3>
              <ul className="divide-y">
                {g.docs.map((d) => (
                  <DocRow key={d.name} doc={d} />
                ))}
              </ul>
            </div>
          ))}
        </div>
      </Section>
    </div>
  );
}
