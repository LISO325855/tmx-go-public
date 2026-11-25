package term

import (
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// Claude Code's input box after a turn, as on hosta-go on 17 Nov: the prompt sign, then a
// placeholder drawn as a hint. box is what follows the prompt sign, with its escape codes.
func claudeBox(box string) string {
	return "✻ Cooked for 26s\r\n────────\r\n❯" + box + "\x1b[0m\r\n────────\r\n  ⏵⏵ bypass permissions on (shift+tab to cycle)"
}

// A box that holds only the harness's placeholder is an empty box; what a person typed is a
// draft, also when a placeholder-coloured word stands next to it.
func TestClaudePlaceholderIsAnEmptyBox(t *testing.T) {
	for _, tc := range []struct {
		name, box string
		empty     bool
	}{
		{"nothing", "", true},
		{"faint placeholder", "  \x1b[2mClaude Code", true},
		{"bright black placeholder", " \x1b[90mIdle probe session", true},
		{"256-colour grey placeholder", " \x1b[38;5;244mIdle probe session", true},
		{"truecolor grey placeholder", " \x1b[38;2;153;153;153mIdle probe session", true},
		{"cursor block before a placeholder", " \x1b[7m \x1b[27m\x1b[2mClaude Code", true},
		{"cursor block on the placeholder's first letter", " \x1b[7mC\x1b[27m\x1b[2mlaude Code", true},
		{"a typed draft", " human draft", false},
		{"a one-letter draft under the cursor block", " \x1b[7mx\x1b[27m", false},
		{"the cursor block alone", " \x1b[7m \x1b[27m", true},
		{"a typed draft in white", " \x1b[38;2;255;255;255mhuman draft", false},
		{"a typed draft in black (light theme)", " \x1b[38;2;0;0;0mhuman draft", false},
		{"a typed draft in a colour", " \x1b[34m/compact", false},
		{"a draft with a hint after it", " fix the\x1b[2m tab to accept", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := newGuardedTestProcess(t)
			p.w.scr.Write([]byte(claudeBox(tc.box)))
			if got := p.w.boxEmpty(); got != tc.empty {
				all, typed := p.w.scr.LinesTyped()
				t.Fatalf("empty = %v, want %v\nrows  %q\ntyped %q", got, tc.empty, all, typed)
			}
		})
	}
}

// The whole way: a harness that worked and now shows its placeholder is back at its input box
// (the turn ends, the state is idle), and mail that waited is typed in.
func TestTurnEndsAndMailFlowsWithAPlaceholderInTheBox(t *testing.T) {
	p, bp := newGuardedTestProcess(t)
	p.done = make(chan struct{})
	close(bp.release)
	p.state = model.StateWorking
	p.w.turnInput = true
	p.w.scr.Write([]byte(claudeBox("  \x1b[2mClaude Code")))
	p.w.idleSince = time.Now().Add(-time.Hour)
	p.w.evaluate()
	p.mu.Lock()
	state := p.state
	p.mu.Unlock()
	if state != model.StateIdle {
		t.Fatalf("state %s after the turn, with the placeholder in the box", state)
	}
	if err := p.w.send("queued mail"); err != nil {
		t.Fatal(err)
	}
	p.w.idleSince = time.Now().Add(-time.Hour)
	p.w.evaluate()
	select {
	case b := <-bp.writes:
		if string(b) != "queued mail" {
			t.Fatalf("paste %q", b)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("mail was not typed in; %d still queued", len(p.w.queued))
	}
	close(p.done)
}
