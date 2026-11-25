// Folding long text to its first lines (thread events have no length cap any more, 17 Nov 2026:
// a long one shows its beginning and "more"). Pure: no imports.

export const FOLD_LINES = 4;
export const FOLD_CHARS = 320;

export interface Folded {
  /** what is shown while folded: the whole text when it is short */
  head: string;
  /** true when there is more than the head */
  folded: boolean;
}

/** The first `lines` lines of a text, at most `chars` characters, cut at a word where it can be.
 *  A text only a little longer than the limit is left whole: "more" for a few words is noise. */
export function foldText(text: string, lines = FOLD_LINES, chars = FOLD_CHARS): Folded {
  const all = text ?? "";
  const rows = all.split("\n");
  if (rows.length <= lines && all.length <= chars + chars / 4) return { head: all, folded: false };
  let head = rows.slice(0, lines).join("\n");
  if (head.length > chars) {
    head = head.slice(0, chars);
    const space = Math.max(head.lastIndexOf(" "), head.lastIndexOf("\n"));
    if (space > chars / 2) head = head.slice(0, space);
  }
  head = head.replace(/\s+$/, "");
  if (head.length >= all.replace(/\s+$/, "").length) return { head: all, folded: false };
  return { head: head + "…", folded: true };
}
