// A project's card on the Projects page (classic UI profile; the owner, 17 Nov 19:01: "like
// Python's"; tmx Python static/index.html activeCard / shelfCard): the name, what it is, the
// latest status note, the progress bar, "N agents · N working · coordinator X", the row of
// little agent heads, and the first final output.
import { useEffect, useMemo } from "react";
import { AgentFace } from "@/components/classic/agent-face";
import { stateWords } from "@/lib/classic-agents";
import { cardFaces, fileName, needsProgress, projectCounts, statusLines } from "@/lib/project-page";
import { followProjectPages, loadProjectPage, useProjectPages } from "@/lib/project-pages";
import { href, paths } from "@/lib/router";
import { agentsOfProject, useStore } from "@/lib/store";
import type { Project } from "@/lib/types";
import { cn } from "@/lib/utils";

export function ProjectCard({ project }: { project: Project }) {
  const agents = useStore((s) => s.agents);
  const machine = useStore((s) => s.machine);
  const page = useProjectPages((s) => s.pages[project.id]);
  useEffect(() => {
    followProjectPages();
    void loadProjectPage(project.id);
  }, [project.id]);

  const members = useMemo(() => agentsOfProject(agents, project.id), [agents, project.id]);
  const counts = projectCounts(members);
  const lead = project.lead_id ? agents[project.lead_id] : undefined;
  const faces = cardFaces(members, project.lead_id);
  const status = statusLines(page?.section?.status);
  const progress = needsProgress(page?.section?.needs);
  const out = (page?.artifacts?.final_output ?? [])[0];
  const active = project.status === "active";

  return (
    <a
      href={href(paths.project(project.id))}
      data-project-card={project.id}
      className={cn(
        "flex h-full min-w-0 flex-col gap-3 rounded-[14px] border bg-card p-5 outline-none transition-colors duration-150 [overflow-wrap:anywhere] hover:border-border-strong focus-visible:ring-2 focus-visible:ring-ring",
        !active && "bg-transparent",
      )}
    >
      <h3 className="flex items-start gap-3 text-[1.0625rem] leading-snug font-semibold tracking-tight">
        <span className="min-w-0 flex-1">{project.name}</span>
        {active && (
          <span aria-hidden className="shrink-0 text-base leading-none text-muted-foreground">
            ↗
          </span>
        )}
        {!active && <span className="shrink-0 rounded-full border px-2 py-px text-2xs font-normal text-muted-foreground">{project.status}</span>}
      </h3>
      {project.brief && <p className="line-clamp-3 text-[0.8125rem] leading-relaxed whitespace-pre-line text-muted-foreground">{project.brief}</p>}
      {status.length > 0 && (
        <p className="rounded-r-lg border-l-2 border-border-strong bg-muted/40 px-3 py-2 text-[0.8125rem] leading-relaxed" data-card-status>
          {!active && <b className="font-medium">Where it stood: </b>}
          {status.join(" ")}
        </p>
      )}
      {/* as on Python's card: the bar is always there; with no checklist it is empty and says so */}
      <div data-card-progress>
        <div className="h-1.5 overflow-hidden rounded-full bg-muted" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={progress.percent} aria-label="Progress">
          <div className="h-full rounded-full bg-brand" style={{ width: `${progress.percent}%` }} />
        </div>
        <p className="mt-1.5 text-xs">
          {progress.total > 0 ? (
            <span className="tabular text-muted-foreground">
              <b className="font-semibold text-foreground">{progress.percent}%</b> · {progress.done} of {progress.total} steps done
            </span>
          ) : (
            <b className="font-semibold">Not reported</b>
          )}
        </p>
      </div>
      {(counts.agents > 0 || lead) && <p className="flex flex-wrap gap-x-3 gap-y-0.5 text-xs text-muted-foreground" data-card-counts>
        {counts.agents > 0 && <span>
          <b className="font-semibold text-foreground">{counts.agents}</b> agent(s)
        </span>}
        {counts.working > 0 && <span>
          <b className="font-semibold text-foreground">{counts.working}</b> working
        </span>}
        {lead && (
          <span>
            coordinator <b className="font-semibold text-foreground">{lead.name}</b>
          </span>
        )}
      </p>}
      {faces.shown.length > 0 && (
        <div className="flex flex-wrap items-center gap-2.5" data-card-faces>
          {faces.shown.map((m) => (
            <AgentFace key={m.id} name={m.name} size={34} className="shadow-[0_2px_4px_rgb(0_0_0/0.12)]" title={`${m.name}${machine ? "@" + machine : ""} · ${stateWords(m)}`} />
          ))}
          {faces.more > 0 && <span className="ml-1 text-2xs text-muted-foreground">+{faces.more}</span>}
        </div>
      )}
      {out && (
        <p className="truncate text-xs text-muted-foreground" data-card-output>
          <span aria-hidden>📦 </span>
          {fileName(out.path) || out.path}
          {out.what ? ` · ${out.what}` : ""}
        </p>
      )}
    </a>
  );
}
