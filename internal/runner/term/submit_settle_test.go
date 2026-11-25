package term

import (
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

func workingOwnerScreen(p *process) {
	p.w.scr.Write([]byte("\x1b[2J\x1b[HWorking (esc to interrupt)\r\n" + claudeBox("")))
}

// Wait for the real delayed Enter transaction, including its cleanup. This
// exercises the watcher, not a fake runner whose Send skips the terminal gates.
func finishOwnerSubmission(t *testing.T, p *process) {
	t.Helper()
	within(t, time.Second, "submission completion", func() {
		p.w.inputMu.Lock()
		p.w.inputMu.Unlock()
	})
}

func expectOwnerWrite(t *testing.T, bp *blockingPty, want string) {
	t.Helper()
	select {
	case b := <-bp.writes:
		if string(b) != want {
			t.Fatalf("write %q, want %q", b, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("missing write %q", want)
	}
}

func noOwnerWrite(t *testing.T, bp *blockingPty) {
	t.Helper()
	select {
	case b := <-bp.writes:
		t.Fatalf("premature write %q", b)
	default:
	}
}

func expireOwnerSettle(p *process) {
	p.mu.Lock()
	p.w.busyUntil = time.Now().Add(-time.Second)
	p.mu.Unlock()
}

func settleTestProcess(t *testing.T) (*process, *blockingPty) {
	t.Helper()
	p, bp := newGuardedTestProcess(t)
	p.done = make(chan struct{})
	p.state = model.StateWorking
	p.w.info.SubmitDelay = time.Millisecond
	workingOwnerScreen(p)
	t.Cleanup(func() {
		close(p.done)
		finishOwnerSubmission(t, p)
	})
	return p, bp
}

func drainOwnerWritesStarted(p *process, bp *blockingPty) {
	go func() {
		for {
			select {
			case <-bp.entered:
			case <-p.done:
				return
			}
		}
	}()
}
