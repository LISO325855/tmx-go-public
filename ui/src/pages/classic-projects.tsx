// The Projects page of the classic UI profile on a computer (the owner, 17 Nov 19:01; tmx Python's
// "Projects" tab): a card per project, "Active projects · N" first, then "On the shelf · N"
// (the paused and archived ones). A phone keeps its own list (pages/phone-lists.tsx).
import { ProjectShells } from "@/components/classic/project-shells";
import { useMemo } from "react";
import { FolderPlus, Plus } from "lucide-react";
import { ProjectCard } from "@/components/classic/project-card";
import { EmptyState, PageBody, PageHeader, PageTitle } from "@/components/page";
import { Button } from "@/components/ui/button";
import { plural } from "@/lib/format";
import { splitProjects } from "@/lib/project-page";
import { useStore } from "@/lib/store";
import { ui } from "@/lib/ui";

export function ClassicProjectsPage() {
  const projects = useStore((s) => s.projects);
  const { active, shelf } = useMemo(() => splitProjects(Object.values(projects)), [projects]);
  const total = active.length + shelf.length;
  return (
    <div className="flex min-h-0 flex-1 flex-col" data-classic-projects>
      <PageHeader
        actions={
          <Button variant="outline" size="sm" onClick={() => ui.openNewAgent(true)}>
            <Plus />
            Agent
          </Button>
        }
      >
        <PageTitle>Projects</PageTitle>
        <span className="tabular truncate text-xs text-muted-foreground">{plural(total, "project")}</span>
      </PageHeader>
      <PageBody width="max-w-[80rem]" className="space-y-7">
        <ProjectShells />
        {total === 0 ? (
          <EmptyState icon={FolderPlus} title="No projects yet" className="border-none bg-transparent">
            The Architect sets up projects and their leads. Tell it what to build.
          </EmptyState>
        ) : (
          <>
            {active.length > 0 && (
              <section aria-label="Active projects">
                <h2 className="mb-3 text-2xs font-semibold tracking-[0.12em] text-subtle-foreground uppercase">Active projects · {active.length}</h2>
                <div className="grid auto-rows-fr gap-4 [grid-template-columns:repeat(auto-fill,minmax(min(100%,22.5rem),1fr))]">
                  {active.map((p) => (
                    <ProjectCard key={p.id} project={p} />
                  ))}
                </div>
              </section>
            )}
            {shelf.length > 0 && (
              <section aria-label="On the shelf">
                <h2 className="mb-3 text-2xs font-semibold tracking-[0.12em] text-subtle-foreground uppercase">On the shelf · {shelf.length}</h2>
                <div className="grid auto-rows-fr gap-4 [grid-template-columns:repeat(auto-fill,minmax(min(100%,22.5rem),1fr))]">
                  {shelf.map((p) => (
                    <ProjectCard key={p.id} project={p} />
                  ))}
                </div>
              </section>
            )}
          </>
        )}
      </PageBody>
    </div>
  );
}
