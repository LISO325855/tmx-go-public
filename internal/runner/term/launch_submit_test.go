package term

import (
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

func TestClaudeLaunchPromptWaitsForComposerThenSubmitsOnce(t *testing.T) {
	p, pty := settleTestProcess(t)
	p.state = model.StateStarting
	p.w.ready = false
	close(pty.release)
	drainOwnerWritesStarted(p, pty)
	p.w.scr.Write([]byte("\x1b[2J\x1b[HStarting Claude"))
	if err := p.SendLaunchPrompt("first line\nsecond line"); err != nil {
		t.Fatal(err)
	}
	p.w.evaluate()
	noOwnerWrite(t, pty)
	if p.state != model.StateStarting {
		t.Fatalf("launch without a composer reported %s", p.state)
	}

	p.w.scr.Write([]byte("\x1b[2J\x1b[H\x1b[?2004h" + claudeBox("")))
	p.w.idleSince = time.Now().Add(-time.Second)
	p.w.evaluate()
	expectOwnerWrite(t, pty, "\x1b[200~first line\rsecond line\x1b[201~")
	expectOwnerWrite(t, pty, "\r")
	finishOwnerSubmission(t, p)
	p.w.pumpMail()
	noOwnerWrite(t, pty)
}
