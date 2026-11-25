// The key row under an agent's terminal (classic UI profile; parity B3: Python has "Enter, Esc,
// Ctrl+C, arrows, Tab, 1 2 3, y, n" under the terminal, so a prompt on the agent's screen can
// be answered with a click). Each key is sent with POST /api/agents/{id}/keys, the daemon's own
// key names (internal/runner/term/screen/answer.go Keys: enter, esc, tab, up, down, left, right,
// ctrl+<letter>, or a single character). Owner only, as typing into the terminal is.
//
// Not here: Python's "Approve / Always / Deny". In Go a permission menu on the screen is already
// a card (in Chat and in Your turn) with exactly those answers.

export interface TerminalKey {
  /** what the button shows */
  label: string;
  /** the daemon's key spec */
  keys: string;
  /** the button's tooltip and accessible name */
  title: string;
  /** a gap before this key: the row reads in groups */
  gap?: boolean;
}

export const TERMINAL_KEYS: TerminalKey[] = [
  { label: "Enter", keys: "enter", title: "Enter" },
  { label: "Esc", keys: "esc", title: "Escape" },
  { label: "Ctrl+C", keys: "ctrl+c", title: "Ctrl+C" },
  { label: "↑", keys: "up", title: "Arrow up", gap: true },
  { label: "↓", keys: "down", title: "Arrow down" },
  { label: "←", keys: "left", title: "Arrow left" },
  { label: "→", keys: "right", title: "Arrow right" },
  { label: "Tab", keys: "tab", title: "Tab" },
  { label: "1", keys: "1", title: "1", gap: true },
  { label: "2", keys: "2", title: "2" },
  { label: "3", keys: "3", title: "3" },
  { label: "y", keys: "y", title: "y", gap: true },
  { label: "n", keys: "n", title: "n" },
];

/** A key spec the daemon's parser accepts: one of its names, ctrl+<letter>, or one character. */
export function validKeySpec(spec: string): boolean {
  const named = ["enter", "tab", "esc", "escape", "backspace", "space", "up", "down", "right", "left", "home", "end", "pgup", "pgdn", "delete", "shift+tab"];
  const toks = spec.split(/\s+/).filter(Boolean);
  return toks.length > 0 && toks.every((t) => named.includes(t.toLowerCase()) || /^ctrl\+[a-z]$/i.test(t) || [...t].length === 1);
}

/**
 * The key row and "Paste only" need a live screen: an agent that runs in terminal mode. An
 * agent in chat mode has no screen to press keys on or to paste into.
 */
export function hasLiveScreen(a: { mode: string; state: string; pid?: number }): boolean {
  return a.mode === "terminal" && !["stopped", "exited", "error", "queued"].includes(a.state) && (a.pid ?? 0) > 0;
}
