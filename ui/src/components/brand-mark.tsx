// The Living cell mark of the Tiamatrix dashboards: the exact SVG file (src/assets/living-cell.svg),
// inlined so that its fill="currentColor" follows the text colour of the place it sits in.
import mark from "@/assets/living-cell.svg?raw";
import { cn } from "@/lib/utils";

export function BrandMark({ className }: { className?: string }) {
  return <span role="img" aria-label="Tiamatrix" data-brand-mark className={cn("inline-flex size-4 shrink-0 [&>svg]:size-full", className)} dangerouslySetInnerHTML={{ __html: mark }} />;
}
