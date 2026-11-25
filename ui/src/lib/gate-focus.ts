// Never retain or inspect typed values: only the previous focus target is remembered.
let previous: HTMLElement | null = null;
export function rememberGateFocus() {
  previous = typeof document === "undefined" ? null : document.activeElement as HTMLElement | null;
  if (previous?.closest(".xterm, .xterm-host")) { previous.blur(); previous = null; }
}
export function restoreGateFocus() {
  const target = previous;
  previous = null;
  if (target?.isConnected && !target.closest(".xterm, .xterm-host")) target.focus();
}

/** Extra capture guard alongside Radix's modal focus trap, including programmatic steals. */
export function trapGateFocus(root: HTMLElement) {
  const doc = root.ownerDocument;
  const focus = () => (root.querySelector<HTMLElement>('input[type="password"]:not(:disabled), button:not(:disabled), [tabindex="0"]') ?? root).focus();
  const outside = (event: Event) => {
    if (root.contains(event.target as Node)) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    focus();
  };
  doc.addEventListener("focusin", outside, true);
  doc.addEventListener("keydown", outside, true);
  focus();
  return () => {
    doc.removeEventListener("focusin", outside, true);
    doc.removeEventListener("keydown", outside, true);
  };
}

export function guardTerminalForGate(
  terminal: { options: { disableStdin?: boolean }; blur(): void },
  gate: { getState(): { confirming: unknown }; subscribe(fn: () => void): () => void },
) {
  const update = () => {
    const blocked = !!gate.getState().confirming;
    terminal.options.disableStdin = blocked;
    if (blocked) terminal.blur();
  };
  update();
  return gate.subscribe(update);
}
