export function loginPaste(code: string, submit = false): string | null {
  const text = code.trim();
  // eslint-disable-next-line no-control-regex -- rejects pasted control characters on purpose
  if (!text || /[\u0000-\u001f\u007f-\u009f]/.test(text)) return null;
  return "\x1b[200~" + text + "\x1b[201~" + (submit ? "\r" : "");
}

export function isLoginPasteShortcut(event: KeyboardEvent): boolean {
  return !event.altKey && (
    (!event.shiftKey && (event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "v") ||
    (event.shiftKey && !event.ctrlKey && !event.metaKey && event.key === "Insert")
  );
}
