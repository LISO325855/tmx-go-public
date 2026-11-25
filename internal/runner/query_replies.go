package runner

import (
	"bytes"
	"regexp"
)

// Replies emitted by a terminal viewer while rendering output. The daemon's own
// screen answers these queries; viewer replies and focus reports are not owner keystrokes.
var terminalQueryReply = regexp.MustCompile(`\x1b\[[IO]|\x1b\[\??\d+;\d+R|\x1b\[[?>]?[\d;]*c|\x1b\[\??\d+n|\x1b\[\?[\d;]*\$y|\x1b\[[\d;]+t|\x1b\](?:1[0-9]|4;\d+);rgb:[0-9a-fA-F/]+(?:\x07|\x1b\\)|\x1b\]52;[^;]*;[^\x07\x1b]*(?:\x07|\x1b\\)|\x1bP[01]\$r[^\x1b]*\x1b\\`)

func StripTerminalQueryReplies(b []byte) []byte {
	if !bytes.Contains(b, []byte{0x1b}) {
		return b
	}
	return terminalQueryReply.ReplaceAll(b, nil)
}
