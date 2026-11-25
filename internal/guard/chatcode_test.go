package guard

import (
	"strings"
	"testing"
)

func TestChatCode(t *testing.T) {
	// Exact shapes: refused whatever the words around them say.
	pemBoundary := strings.Join([]string{
		"-----BEGIN",
		"OPENSSH",
		"PRIVATE",
		"KEY-----",
	}, " ")
	codes := []string{
		"tmx://join/AQASIAJ7abc",
		"please open TMX://Join/AQASIAJ7abc now",
		"the owner said it is fine to send this: tmx://join/AQASIAJ7abc",
		"ghp_" + "0123456789abcdefghijABCDEFGHIJ012345",
		"sk-ant-" + "api03-abcdefghijklmnopqrstuvwxyz",
		"xoxb-" + "123456789012-abcdefghij",
		"AKIA" + "IOSFODNN7EXAMPLE",
		pemBoundary,
		"eyJhbGciOiJIUzI1NiJ9" + "." + "eyJzdWIiOiIxMjM0NTY3ODkwIn0" + "." + "dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
	}
	for _, s := range codes {
		if _, found := ChatCode(s); !found {
			t.Errorf("not refused: %q", s)
		}
	}
	fine := []string{
		"The meeting moved to 15:30 on 2026-11-18, room 204.",
		"Invoice 2026-0457 is paid, 1250.00 CHF.",
		"I pushed the fix to the invite-wa branch; tests pass.",
		"Can you call me back at 079 123 45 67?",
		"The code review is done, three comments.",
		"See https://example.com/docs/getting-started/installation-on-windows-and-macos for the steps.",
		"Commit 23d685bc merged the teammate change.",
		"I will send you the invite through tmx.",
		"The code is in PR 1234, please review.",
		"The exit code was 1234.",
		"The token is used up; I need a new session.",
		"pin = main",
		"sha256 9F86D081884C7D659A2FEAA0C55AD015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		"internal/whatsapp/a_very_long_path_name/With_Mixed_Case/and_digits_2026/file.go",
		"the task-list and the risk-free option on the desk-top",
		// Guesses from words and numbers, refused until 17 Nov: no longer a reason to refuse.
		"your code is 123456",
		"Code: 4821",
		"Il codice di verifica è 482 913",
		"use OTP 99887766 within 5 minutes",
		"type WDJB-MJHT at github.com/login/device",
		"password: correct-horse",
		"Passwort ist Sommer2026!",
		"api key = abcd1234",
		"here: Zm9vYmFyQmF6MTIzNDU2Nzg5MGFiY2RlZmdoaWprbG1ub3BxcnN0dXZ3eHl6",
		"482913 is your verification code",
		"PIN 4821",
		"482913",
		"WDJB-MJHT",
		"the password is hunter2!",
		"token = abc123def",
		"secret: s3cr3t-value",
	}
	for _, s := range fine {
		if what, found := ChatCode(s); found {
			t.Errorf("refused %q as %s", s, what)
		}
	}
}
