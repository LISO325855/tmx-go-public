package term

import (
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
)

// A menu appearing during a paste must not receive Enter as an approval.
func TestNoEnterIntoAMenuThatCameUpAfterTyping(t *testing.T) {
	const working = "Working (esc to interrupt)\r\n────────\r\n❯\r\n────────\r\n? for shortcuts"
	for _, tc := range []struct {
		name   string
		menu   bool
		enter  bool
		report string
	}{
		{"still working", false, true, "submitted"},
		{"a menu came up", true, false, "unconfirmed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, bp := newGuardedTestProcess(t)
			p.done = make(chan struct{})
			close(bp.release)
			go func() { // the fixture announces every write; nobody else listens here
				for {
					select {
					case <-bp.entered:
					case <-p.done:
						return
					}
				}
			}()
			p.state = model.StateWorking
			p.w.scr.Write([]byte(working))
			if err := p.Interject("the owner's words", []string{"m1"}); err != nil {
				t.Fatal(err)
			}
			select {
			case b := <-bp.writes:
				if string(b) != "the owner's words" {
					t.Fatalf("typed %q", b)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("the owner's words were not typed into the working screen")
			}
			if tc.menu {
				p.w.scr.Write([]byte("\x1b[2J\x1b[H" + menuScreen))
			}
			var report string
			entered := false
			deadline := time.After(5 * time.Second)
			for report == "" {
				select {
				case ev := <-p.events:
					if ev.Kind == runner.EvMailDelivery {
						report = ev.Delivery
					}
				case b := <-bp.writes:
					entered = entered || string(b) == "\r"
				case <-deadline:
					t.Fatal("no delivery report")
				}
			}
			if report != tc.report {
				t.Fatalf("delivery %q, want %q", report, tc.report)
			}
			select {
			case b := <-bp.writes:
				entered = entered || string(b) == "\r"
			default:
			}
			if entered != tc.enter {
				t.Fatalf("Enter written: %v, want %v", entered, tc.enter)
			}
			close(p.done)
		})
	}
}
