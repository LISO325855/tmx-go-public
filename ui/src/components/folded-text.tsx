// A long text shown by its first lines with "more" (lib/fold-text.ts). A short text is shown as
// it always was: the same words, no button.
import { useState } from "react";
import { foldText } from "@/lib/fold-text";

export function FoldedText({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  const f = foldText(text);
  if (!f.folded) return <>{text}</>;
  return (
    <>
      {open ? text : f.head}{" "}
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="rounded text-xs font-medium text-brand outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring"
        data-fold-more
      >
        {open ? "less" : "more"}
      </button>
    </>
  );
}
