import { ProjectShells } from "@/components/classic/project-shells";
import { PageBody, PageHeader, PageTitle } from "@/components/page";

/** Reuse the existing machine list; registry management remains a separate feature. */
export function ShellsPage() {
  return <div className="flex min-h-0 flex-1 flex-col">
    <PageHeader><PageTitle>Shells</PageTitle></PageHeader>
    <PageBody><ProjectShells /></PageBody>
  </div>;
}
