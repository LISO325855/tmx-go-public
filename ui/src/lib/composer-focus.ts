/** Touch keyboards open only through a direct tap on the text box. */
export function canAutoFocusComposer() {
  return !(typeof navigator !== "undefined" && navigator.maxTouchPoints > 0)
    && !(typeof matchMedia === "function" && matchMedia("(any-pointer: coarse)").matches);
}

export function focusComposer(element: { focus(): void } | null | undefined) {
  if (canAutoFocusComposer()) element?.focus();
}
