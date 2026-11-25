package guard

import "regexp"

// ChatCode identifies credential shapes for display redaction only. It is not a send policy.
var (
	joinLinkRx = regexp.MustCompile(`(?i)tmx://join/`)
	// Well-known credential shapes.
	tokenRx = regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9_\-]{20,}|xox[abprs]-[A-Za-z0-9\-]{10,}|` +
		`AKIA[0-9A-Z]{16}|tskey-[A-Za-z0-9\-]{10,}|AIza[0-9A-Za-z_\-]{30,}|eyJ[A-Za-z0-9_\-]{8,}\.eyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,})`)
	privateKeyRx = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)
)

// ChatCode reports a known credential shape for display redaction, never the value itself.
func ChatCode(text string) (what string, found bool) {
	switch {
	case joinLinkRx.MatchString(text):
		return "a tmx join invite link", true
	case privateKeyRx.MatchString(text):
		return "a private key", true
	case tokenRx.MatchString(text):
		return "an access token", true
	}
	return "", false
}
