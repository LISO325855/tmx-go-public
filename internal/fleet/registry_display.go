package fleet

import (
	"path"
	"regexp"
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/guard"
)

// This is a display projection only. Restart continues to use the original brief.
// Match quoted values as a whole, including unterminated quotes (fail closed).
const displayValue = `(?:"(?:\\.|\x60.|[^"\\\x60\r\n])*(?:"|[\\\x60]?$)|'(?:\\.|[^'\\\r\n])*(?:'|\\?$)|(?:\\.|\x60.|[^\s;&|"'])+)+`

var (
	displayFlag     = regexp.MustCompile(`(?i)(?:--?|/)[a-z0-9_-]*?(?:password|passwd|pass|token|secret|apikey|key|credential|auth|pwd|code)|-p\b`)
	displayEnv      = regexp.MustCompile(`(?i)([a-z_][a-z0-9_]*(?:TOKEN|PASSWORD|PASSWD|SECRET|KEY|CREDENTIAL)[a-z0-9_]*|TOKEN|PASSWORD|PASSWD|SECRET|KEY|CREDENTIAL)=(` + displayValue + `)`)
	displayUserinfo = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^\s/]*@`)
	displayWords    = regexp.MustCompile(`(?:"[^"\r\n]*(?:"|$)|'[^'\r\n]*(?:'|$)|[^\s"']+)+`)
)

func safeShellDisplay(command string) string {
	if what, found := guard.ChatCode(command); found && what == "a private key" {
		return "[redacted]"
	}
	// Fail closed at the first secret-like parameter. Do not parse its value:
	// separators, shell quoting and even missing separators are all ambiguous.
	// Stop at the sensitive name fragment so an attached value cannot survive.
	if match := displayFlag.FindStringIndex(command); match != nil {
		command = command[:match[1]] + " [hidden]"
	}
	command = displayEnv.ReplaceAllString(command, "$1=[redacted]")
	command = displayUserinfo.ReplaceAllString(command, "$1[redacted]@")
	words := displayWords.FindAllString(command, -1)
	for i, word := range words {
		// Reuse the exact-shape credential detector, including invite/private-key
		// shapes. Redact the entire argument, never only a credential prefix.
		if _, found := guard.ChatCode(word); found {
			words[i] = "[redacted]"
		}
	}
	if len(words) > 0 {
		words[0] = path.Base(strings.ReplaceAll(strings.Trim(words[0], `"'`), `\`, "/"))
	}
	return strings.Join(words, " ")
}
