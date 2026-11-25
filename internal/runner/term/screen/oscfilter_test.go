package screen

import (
	"strings"
	"testing"
)

// The end of Claude Code's output when a turn ends, as read on hosta-go on 17 Nov
// (box-probe-h): the empty input box on row 30, then the idle window title "✳ Idle probe
// session". "✳" is e2 9c b3; 0x9c must not end the title.
const idleTitleStream = "\x1b[m\x1b[27;1H\x1b[K\x1b[30;1H❯\xc2\xa0\x1b[38;2;153;153;153m\x1b[32;51H← for agents\x1b[K\x1b[30;3H" +
	"\x1b]0;\xe2\x9c\xb3 Idle probe session\x07\x1b[?25h\x1b[?25l\x1b[m\x1b[32;48H\x1b[K\x1b[30;3H\x1b[?25h"

func TestWindowTitleDoesNotLeakOntoTheScreen(t *testing.T) {
	for _, chunk := range []int{0, 1, 7} { // whole, byte by byte, in pieces: the pty cuts anywhere
		s := New(120, 32, nil)
		b := []byte(idleTitleStream)
		if chunk == 0 {
			s.Write(b)
		}
		for i := 0; chunk > 0 && i < len(b); i += chunk {
			s.Write(b[i:min(i+chunk, len(b))])
		}
		lines := s.Lines()
		if got := strings.TrimSpace(lines[29]); got != "❯" { // Claude Code draws a no-break space after the sign
			t.Errorf("chunk %d: the input box row reads %q, want the prompt sign alone", chunk, got)
		}
		if strings.Contains(strings.Join(lines, "\n"), "Idle probe session") {
			t.Errorf("chunk %d: the window title is on the screen: %q", chunk, lines)
		}
		s.Close()
	}
}

func TestStringFilter(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"text is left alone", "caf\xc3\xa9 ✳ ok", "caf\xc3\xa9 ✳ ok"},
		{"a title ended by BEL", "\x1b]0;\xe2\x9c\xb3 T\x07✳", "\x1b]0;??? T\x07✳"},
		{"a title ended by ESC backslash", "\x1b]2;\xe2\x9c\xb3\x1b\\✳", "\x1b]2;???\x1b\\✳"},
		{"an ASCII string", "\x1b]11;?\x07x", "\x1b]11;?\x07x"},
		{"a DCS is not ended by BEL", "\x1bPq\x07\xe2\x9c\xb3\x1b\\✳", "\x1bPq\x07???\x1b\\✳"},
		{"ESC inside a string begins something new", "\x1b]0;a\x1b[1m\xe2\x9c\xb3", "\x1b]0;a\x1b[1m\xe2\x9c\xb3"},
		{"CAN aborts", "\x1b]0;a\x18\xe2\x9c\xb3", "\x1b]0;a\x18\xe2\x9c\xb3"},
		{"other escapes", "\x1b[31m\xe2\x9c\xb3\x1b(B✳", "\x1b[31m\xe2\x9c\xb3\x1b(B✳"},
	} {
		var f stringFilter
		if got := string(f.filter([]byte(tc.in))); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
		var g stringFilter // byte by byte
		var parts []byte
		for i := range len(tc.in) {
			parts = append(parts, g.filter([]byte{tc.in[i]})...)
		}
		if string(parts) != tc.want {
			t.Errorf("%s, byte by byte: %q, want %q", tc.name, parts, tc.want)
		}
	}
	in := []byte("\x1b]0;\xe2\x9c\xb3\x07")
	var f stringFilter
	f.filter(in)
	if string(in) != "\x1b]0;\xe2\x9c\xb3\x07" {
		t.Error("the input was changed")
	}
}
