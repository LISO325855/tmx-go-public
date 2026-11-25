package screen

// A terminal's control strings (OSC: the window title, links, colours; DCS, APC, PM, SOS) end
// with BEL or ESC \. The emulator's parser also ends one at the byte 0x9c, the one-byte C1
// String Terminator, and 0x9c is a continuation byte of ordinary UTF-8 characters. Claude Code
// sets the window title "✳ <session>" when it becomes idle; "✳" is e2 9c b3, so the title
// ended in the middle of that character and the rest of it, " <session>", was printed at the
// cursor: in the input box. The box then read "❯  Claude Code", which looks like a person's
// draft, so the agent was never seen back at its input box: no end of turn, state working for
// ever, no mail (hosta-go, 17 Nov; raw bytes read by hosta-go-move-f).
//
// stringFilter replaces every byte >= 0x80 inside a control string with '?' before the
// emulator sees it. The screen needs no title; what a control string does (a colour query, a
// link) is spelled in ASCII.
type stringFilter struct {
	state byte
	osc   bool // the open string is an OSC: BEL ends it too
}

const (
	sfText   byte = iota // ordinary output
	sfEsc                // after ESC
	sfString             // inside a control string
	sfStrEsc             // inside a control string, after ESC
)

// filter returns b with the bytes of control strings made safe. It keeps its place between
// calls, so a string cut by the pty's reads is handled as one. b is not changed.
func (f *stringFilter) filter(b []byte) []byte {
	var out []byte // nil until a byte has to change
	for i, c := range b {
		switch f.state {
		case sfText:
			if c == 0x1b {
				f.state = sfEsc
			}
		case sfEsc:
			f.afterEsc(c)
		case sfString:
			switch {
			case c == 0x1b:
				f.state = sfStrEsc
			case c == 0x07 && f.osc, c == 0x18, c == 0x1a: // BEL; CAN and SUB abort
				f.state = sfText
			case c >= 0x80:
				if out == nil {
					out = append([]byte(nil), b...)
				}
				out[i] = '?'
			}
		case sfStrEsc:
			if c == '\\' {
				f.state = sfText // ESC \ : the string's end
			} else {
				f.afterEsc(c) // the ESC ended the string and begins something new
			}
		}
	}
	if out == nil {
		return b
	}
	return out
}

func (f *stringFilter) afterEsc(c byte) {
	switch c {
	case ']':
		f.state, f.osc = sfString, true
	case 'P', '_', '^', 'X':
		f.state, f.osc = sfString, false
	case 0x1b:
		f.state = sfEsc
	default:
		f.state = sfText
	}
}
