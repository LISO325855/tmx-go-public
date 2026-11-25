// The daemon emulator owns harness queries. xterm emits these replies through
// onData while rendering; forwarding them or focus/blur reports would look like owner typing.
export function stripTerminalQueryReplies(input: string): string {
  // eslint-disable-next-line no-control-regex -- matches terminal escape replies (ESC/BEL) on purpose
  return input.replace(/\x1b\[[IO]|\x1b\[\??\d+;\d+R|\x1b\[[?>]?[\d;]*c|\x1b\[\??\d+n|\x1b\[\?[\d;]*\$y|\x1b\[[\d;]+t|\x1b\](?:1[0-9]|4;\d+);rgb:[0-9a-fA-F/]+(?:\x07|\x1b\\)|\x1b\]52;[^;]*;[^\x07\x1b]*(?:\x07|\x1b\\)|\x1bP[01]\$r[^\x1b]*\x1b\\/g, "");
}
