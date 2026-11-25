package term

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/runner"
)

type receiptPty struct {
	*blockingPty
	mu        sync.Mutex
	calls     int
	completed int
	shortAt   int
	blockAt   int
}

func (p *receiptPty) Write(b []byte) (int, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	if call == p.blockAt {
		p.entered <- struct{}{}
		<-p.release
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.completed++
	if call == p.shortAt {
		return 0, nil
	}
	p.writes <- append([]byte(nil), b...)
	return len(b), nil
}

func TestMailReceiptFollowsActualTerminalWrites(t *testing.T) {
	for _, draft := range []bool{false, true} {
		total := 2 // paste, Enter
		if draft {
			total++
		} // non-destructive End navigation before the paste
		for fail := 0; fail <= total; fail++ {
			t.Run(fmt.Sprintf("draft=%v/short-write=%d", draft, fail), func(t *testing.T) {
				blocked := total
				if fail > 0 {
					blocked = fail
				}
				pt := &receiptPty{blockingPty: newBlockingPty(), shortAt: fail, blockAt: blocked}
				p := newTestProcess(t, pt)
				p.done = make(chan struct{})
				p.w.info.SubmitDelay = time.Millisecond
				text := ""
				if draft {
					text = " human draft"
				}
				p.w.scr.Write([]byte(claudeBox(text)))
				var release sync.Once
				unblock := func() { release.Do(func() { close(pt.release) }) }
				t.Cleanup(func() { unblock(); close(p.done); finishOwnerSubmission(t, p) })
				sent := make(chan error, 1)
				go func() { sent <- p.SendMail("message", []string{"mail-one"}) }()
				select {
				case <-pt.entered:
				case <-time.After(time.Second):
					t.Fatal("terminal write did not start")
				}
				// StateWorking (or dispatching) is activity, not confirmation of delivery.
				// Hold the actual PTY write open: a final receipt here would be premature.
			drain:
				for {
					select {
					case ev := <-p.events:
						if ev.Kind == runner.EvMailDelivery && ev.Delivery != "dispatching" {
							t.Fatalf("final receipt before write completed: %+v", ev)
						}
					default:
						break drain
					}
				}
				unblock()
				if err := <-sent; err != nil {
					t.Fatal(err)
				}
				timeout := time.After(time.Second)
				for {
					select {
					case ev := <-p.events:
						if ev.Kind != runner.EvMailDelivery || ev.Delivery == "dispatching" {
							continue
						}
						want := "submitted"
						count := total
						if fail > 0 {
							want = "unconfirmed"
							count = fail
						}
						if ev.Delivery != want || len(ev.MessageIDs) != 1 || ev.MessageIDs[0] != "mail-one" {
							t.Fatalf("receipt %+v", ev)
						}
						pt.mu.Lock()
						started, completed := pt.calls, pt.completed
						pt.mu.Unlock()
						if started != count || completed != count {
							t.Fatalf("receipt with writes started=%d completed=%d, want %d", started, completed, count)
						}
						// Check the successful writes too: navigation, paste, then Enter.
						expected := []string{"message", "\r"}
						if draft {
							expected = []string{strings.Repeat("\x1b[F", len(p.w.scr.Lines())+1), "  message", "\r"}
						}
						if fail > 0 {
							expected = expected[:fail-1]
						}
						for _, want := range expected {
							select {
							case got := <-pt.writes:
								if string(got) != want {
									t.Fatalf("write %q, want %q", got, want)
								}
							default:
								t.Fatalf("missing successful write %q", want)
							}
						}
						select {
						case got := <-pt.writes:
							t.Fatalf("unexpected write %q", got)
						default:
						}
						finishOwnerSubmission(t, p)
						return
					case <-timeout:
						t.Fatal("no terminal receipt")
					}
				}
			})
		}
	}
}
