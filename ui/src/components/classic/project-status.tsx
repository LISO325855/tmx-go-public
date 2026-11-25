// A project's shared page on the project screen (classic UI profile; parity H2; the owner, 17 Nov
// 19:01: "like Python's"; tmx Python static/index.html openProject): where it stands and who
// coordinates it, the progress with its checklist, the final outputs to grab (what it is, who
// made it, where it is, download), links, the folders, and the team with each member's own
// line. Read-only: the agents publish it (docs/project-sections.md), the owner reads it.
import { useEffect, useMemo, useState } from "react";
import { Download, ExternalLink } from "lucide-react";
import { toast } from "sonner";
import { AgentFace } from "@/components/classic/agent-face";
import { CopyButton } from "@/components/copy-button";
import { Button } from "@/components/ui/button";
import { canDownload, downloadAgentFile } from "@/lib/agent-files";
import { agentSecondLine, stateWords } from "@/lib/classic-agents";
import { relativeTime } from "@/lib/format";
import { harnessLabel } from "@/lib/harness";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/api";
import { downloadAgent, fileName, needsProgress, projectCounts, projectFolders, repoName, repoWebAddress, sectionEmpty, statusLines, webLink, type ProjectArtifact } from "@/lib/project-page";
import { followProjectPages, loadProjectPage, useProjectPages } from "@/lib/project-pages";
import { href, paths } from "@/lib/router";
import { agentsOfProject, useStore } from "@/lib/store";
import type { Agent, Project } from "@/lib/types";
import { cn } from "@/lib/utils";

/** Python's project-page card: a small caps heading over its content; wide = the whole row. */
function Card({ title, wide, children, name }: { title: string; wide?: boolean; children: React.ReactNode; name: string }) {
  return (
    <section className={cn("min-w-0 rounded-xl border bg-card px-4 py-3.5", wide && "col-span-full")} data-project-card-section={name}>
      <h2 className="mb-2.5 text-2xs font-semibold tracking-[0.12em] text-subtle-foreground uppercase">{title}</h2>
      {children}
    </section>
  );
}

/** A path or address an agent wrote: a plain web address is a link; anything else is text to copy. */
function Where({ url }: { url: string }) {
  const web = webLink(url);
  if (web) {
    return (
      <a href={web} target="_blank" rel="noopener noreferrer" className="text-brand hover:underline [overflow-wrap:anywhere]">
        {url}
      </a>
    );
  }
  return <span className="font-mono text-[0.7188rem] text-foreground/80 [overflow-wrap:anywhere]">{url}</span>;
}

function OutputRow({ o, by, from }: { o: ProjectArtifact; by?: Agent; from: string | null }) {
  const [busy, setBusy] = useState(false);
  const web = webLink(o.path);
  const name = fileName(o.path) || o.path;
  async function download() {
    if (!from || busy) return;
    setBusy(true);
    try {
      await downloadAgentFile(from, o.path, name);
    } catch (e) {
      toast.error((e as Error).message || "The download failed.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <li className="flex flex-wrap items-center gap-x-3 gap-y-1.5 py-2.5 first:pt-0 last:pb-0" data-project-output>
      <span aria-hidden className="w-6 shrink-0 text-center text-lg">
        {web ? "🔗" : "📦"}
      </span>
      <span className="min-w-[12rem] flex-1">
        <span className="block text-sm font-medium [overflow-wrap:anywhere]">{name}</span>
        {o.what && <span className="block text-xs text-muted-foreground [overflow-wrap:anywhere]">{o.what}</span>}
        <span className="block text-xs text-subtle-foreground">
          {by ? (
            <a href={href(paths.agent(by.id))} className="hover:text-foreground hover:underline">
              {by.name}
            </a>
          ) : (
            "the project"
          )}
          {" · "}
          <Where url={o.path} />
        </span>
      </span>
      <span className="flex shrink-0 items-center gap-1">
        {web ? (
          <Button size="xs" variant="outline" asChild>
            <a href={web} target="_blank" rel="noopener noreferrer">
              <ExternalLink /> Open link
            </a>
          </Button>
        ) : (
          from &&
          canDownload() && (
            <Button size="xs" variant="outline" disabled={busy} onClick={() => void download()}>
              <Download /> Download
            </Button>
          )
        )}
        {!web && <CopyButton text={o.path} label="Copy path" />}
      </span>
    </li>
  );
}

/**
 * The project's repository (the owner, 17 Nov 19:01; internal/fleet/project_repo.go): one address,
 * set by the owner here. The project's agents read it with their project tools and are told it
 * when they start ("clone it, or fetch and pull"). The daemon itself never touches the forge.
 */
function Repository({ project, repo }: { project: Project; repo: string }) {
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(repo);
  const [busy, setBusy] = useState(false);
  const web = repoWebAddress(repo);

  async function save(next: string) {
    if (busy) return;
    setBusy(true);
    try {
      await api.setProjectRepo(project.id, next.trim());
      await loadProjectPage(project.id);
      setEditing(false);
      toast(next.trim() ? "Repository saved. Agents are told it when they start." : "Repository removed.");
    } catch (e) {
      toast.error((e as Error).message || "The repository was not saved.");
    } finally {
      setBusy(false);
    }
  }

  if (editing) {
    return (
      <form
        className="flex flex-wrap items-center gap-1.5"
        onSubmit={(e) => {
          e.preventDefault();
          void save(value);
        }}
      >
        <Input
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder="https://github.com/owner/repo"
          aria-label="Repository address"
          autoFocus
          autoComplete="off"
          spellCheck={false}
          className="h-7 min-w-[14rem] flex-1 font-mono text-[0.7812rem]"
          disabled={busy}
        />
        <Button type="submit" size="xs" disabled={busy || value.trim() === repo}>
          Save
        </Button>
        <Button type="button" size="xs" variant="ghost" disabled={busy} onClick={() => setEditing(false)}>
          Cancel
        </Button>
        <p className="w-full text-2xs text-subtle-foreground">https://host/owner/repo or git@host:owner/repo. No token in the address. Leave it empty to remove it.</p>
      </form>
    );
  }
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1.5">
      {repo ? (
        <span className="min-w-0 flex-1">
          {web ? (
            <a href={web} target="_blank" rel="noopener noreferrer" className="block text-sm font-medium text-brand hover:underline [overflow-wrap:anywhere]">
              {repoName(repo)}
            </a>
          ) : (
            <span className="block text-sm font-medium [overflow-wrap:anywhere]">{repoName(repo)}</span>
          )}
          <span className="block font-mono text-[0.7188rem] text-subtle-foreground [overflow-wrap:anywhere]">{repo}</span>
        </span>
      ) : (
        <span className="min-w-0 flex-1 text-sm text-muted-foreground">No repository set. With one, the project's agents are told where the code lives.</span>
      )}
      {repo && <CopyButton text={repo} label="Copy address" />}
      <Button
        size="xs"
        variant="outline"
        onClick={() => {
          setValue(repo);
          setEditing(true);
        }}
      >
        {repo ? "Change" : "Set repository"}
      </Button>
    </div>
  );
}

export function ProjectSharedPage({ project }: { project: Project }) {
  const agents = useStore((s) => s.agents);
  const machine = useStore((s) => s.machine);
  const page = useProjectPages((s) => s.pages[project.id]);
  useEffect(() => {
    followProjectPages();
    void loadProjectPage(project.id);
  }, [project.id]);

  const sec = page?.section ?? null;
  const art = page?.artifacts ?? null;
  const members = useMemo(() => agentsOfProject(agents, project.id).sort((a, b) => Number(b.id === project.lead_id) - Number(a.id === project.lead_id) || a.name.localeCompare(b.name)), [agents, project.id, project.lead_id]);
  const lead = project.lead_id ? agents[project.lead_id] : undefined;
  const counts = projectCounts(members);
  const lines = statusLines(sec?.status);
  const progress = needsProgress(sec?.needs);
  const links = sec?.links ?? [];
  const outputs = art?.final_output ?? [];
  const parts = new Map((art?.contributions ?? []).map((p) => [p.agent_id, p]));
  const folders = projectFolders(project.folder, members);
  const known = (id: string) => !!agents[id];

  return (
    <div className="space-y-4" data-project-status>
      <a href={href(paths.projects())} className="inline-flex h-8 items-center rounded-lg border bg-card px-3 text-sm text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring" data-all-projects>
        ← All projects
      </a>
      {/* where it stands: status, progress, who coordinates, where it runs */}
      <section className="rounded-xl border bg-card px-4 py-3.5" data-project-card-section="status">
        <h2 className="mb-2 text-2xs font-semibold tracking-[0.12em] text-subtle-foreground uppercase">Where it stands</h2>
        {page?.failed && !sec ? (
          <p className="text-sm text-muted-foreground">{page.failed}</p>
        ) : !page ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : lines.length > 0 ? (
          <div className="space-y-1 text-[0.875rem] leading-relaxed">
            {lines.map((l, i) => (
              <p key={i}>
                {i === 0 && <b className="font-medium">Status: </b>}
                {l}
              </p>
            ))}
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">{sectionEmpty(sec, art) ? "Nothing published yet. The project's agents write its status, checklist and outputs here." : "No status line yet."}</p>
        )}

        {progress.total > 0 && (
          <div className="mt-3" data-project-progress>
            <div className="flex items-center gap-2.5">
              <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-muted" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={progress.percent} aria-label="Progress">
                <div className="h-full rounded-full bg-brand transition-[width] duration-300" style={{ width: `${progress.percent}%` }} />
              </div>
            </div>
            <p className="tabular mt-1.5 text-xs text-muted-foreground">
              <b className="font-semibold text-foreground">{progress.percent}%</b> · {progress.done} of {progress.total} steps done
            </p>
            {/* as in Python: the checklist is closed until asked for */}
            <details className="mt-2" data-project-checklist>
              <summary className="cursor-pointer text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring">
                Checklist ({progress.done}/{progress.total})
              </summary>
            <ul className="mt-2.5 space-y-1.5">
              {(sec?.needs ?? []).map((n, i) => (
                <li key={i} className="flex items-start gap-2 text-sm">
                  <span aria-hidden className={cn("mt-0.5 flex size-4 shrink-0 items-center justify-center rounded-[4px] border text-[0.625rem] font-bold", n.done ? "border-brand bg-brand text-white" : "border-border-strong")}>
                    {n.done ? "✓" : ""}
                  </span>
                  <span className={cn("min-w-0 [overflow-wrap:anywhere]", n.done && "text-muted-foreground")}>
                    <span className="sr-only">{n.done ? "Done: " : "Open: "}</span>
                    {n.text}
                  </span>
                </li>
              ))}
            </ul>
            </details>
          </div>
        )}

        <p className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground" data-project-meta>
          <span>
            {counts.agents} active · {counts.shelved} shelved
          </span>
          {lead ? (
            <span>
              coordinator{" "}
              <a href={href(paths.agent(lead.id))} className="font-semibold text-foreground hover:underline">
                {lead.name}
              </a>
            </span>
          ) : (
            <span>no coordinator yet</span>
          )}
          {machine && <span>on {machine}</span>}
          {sec && sec.updated_at > 0 && (
            <span>
              updated {relativeTime(sec.updated_at)}
              {sec.updated_by ? ` by ${sec.updated_by}` : ""}
            </span>
          )}
        </p>
        {lead && (
          <p className="mt-3">
            <Button size="sm" variant="outline" asChild>
              <a href={href(paths.agent(lead.id))}>Open coordinator</a>
            </Button>
          </p>
        )}
      </section>

      <div className="grid gap-4 [grid-template-columns:repeat(auto-fill,minmax(min(100%,22.5rem),1fr))]">
        <Card title="Repository" name="repo">{page ? <Repository project={project} repo={sec?.repo ?? ""} /> : <p className="text-sm text-muted-foreground">Loading…</p>}</Card>

        {outputs.length > 0 && (
          <Card title="Final outputs" name="outputs" wide={outputs.length > 2}>
            <ul className="divide-y">
              {outputs.map((o, i) => (
                <OutputRow key={i} o={o} by={o.agent_id ? agents[o.agent_id] : undefined} from={downloadAgent(o, project.lead_id, known)} />
              ))}
            </ul>
          </Card>
        )}

        {links.length > 0 && (
          <Card title="Links" name="links">
            <ul className="space-y-1.5 text-sm">
              {links.map((l, i) => (
                <li key={i} className="min-w-0">
                  {l.title && l.title !== l.url && <span className="mr-1.5">{l.title}</span>}
                  <Where url={l.url} />
                </li>
              ))}
            </ul>
          </Card>
        )}

        {folders.length > 0 && (
          <Card title="Files and locations" name="folders">
            <ul className="space-y-2">
              {folders.map((f) => (
                <li key={f.path} className="flex min-w-0 items-start gap-1">
                  <span className="min-w-0 flex-1">
                    <span className="block font-mono text-[0.7188rem] text-foreground/85 [overflow-wrap:anywhere]">{f.path}</span>
                    <span className="block text-2xs text-subtle-foreground">{[machine, f.who.length ? f.who.join(", ") : "the project's folder"].filter(Boolean).join(" · ")}</span>
                  </span>
                  <CopyButton text={f.path} label="Copy path" className="-my-0.5" />
                </li>
              ))}
            </ul>
          </Card>
        )}

        <Card title={`Team · ${members.length}`} name="team" wide>
          {members.length === 0 ? (
            <p className="text-sm text-muted-foreground">No agents in this project.</p>
          ) : (
            <ul className="divide-y">
              {members.map((m) => {
                const pt = parts.get(m.id);
                return (
                  <li key={m.id} className="flex flex-wrap items-start gap-x-3 gap-y-1.5 py-2.5 first:pt-0 last:pb-0" data-team-member={m.id}>
                    <AgentFace name={m.name} size={30} />
                    <span className="w-44 min-w-0 shrink-0">
                      <a href={href(paths.agent(m.id))} className="block truncate text-sm font-medium hover:underline">
                        {m.name}
                      </a>
                      <span className="block truncate text-2xs text-subtle-foreground">{[harnessLabel(m.harness), machine].filter(Boolean).join(" · ")}</span>
                    </span>
                    <span className="w-20 shrink-0 rounded-full border px-2 py-px text-center text-2xs text-muted-foreground">{stateWords(m)}</span>
                    <span className="min-w-[12rem] flex-1 text-sm">
                      {pt?.status.trim() ? <span className="[overflow-wrap:anywhere]">{pt.status}</span> : <span className="text-muted-foreground">{agentSecondLine(m, "")}</span>}
                      {(pt?.final_output ?? []).map((o, i) => (
                        <span key={i} className="mt-0.5 block text-xs text-muted-foreground [overflow-wrap:anywhere]">
                          <span aria-hidden>📦 </span>
                          {fileName(o.path) || o.path}
                          {o.what ? ` · ${o.what}` : ""}
                        </span>
                      ))}
                      {pt && pt.updated_at > 0 && <span className="block text-2xs text-subtle-foreground">updated {relativeTime(pt.updated_at)}</span>}
                    </span>
                    <Button size="xs" variant="outline" asChild className="shrink-0">
                      <a href={href(paths.agent(m.id))}>Open</a>
                    </Button>
                  </li>
                );
              })}
            </ul>
          )}
        </Card>
      </div>
    </div>
  );
}
