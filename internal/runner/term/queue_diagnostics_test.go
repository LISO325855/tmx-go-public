package term

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/runner/term/screen"
)

func TestOwnerQueueDiagnosticsThrottleAndRelease(t *testing.T) {
	p, bp := settleTestProcess(t)
	close(bp.release)
	drainOwnerWritesStarted(p, bp)
	p.w.scr.Write([]byte(claudeBox("")))
	var out bytes.Buffer
	old := log.Writer()
	log.SetOutput(&out)
	defer log.SetOutput(old)
	now := time.Now()
	p.w.queued = []mailInput{{text: "private owner words", ids: []string{"message-id"}, now: true, queuedAt: now}}
	d := screen.Detection{State: screen.Idle}
	p.w.logHeldLocked(now.Add(9*time.Second), d, false)
	if out.Len() != 0 {
		t.Fatal("logged before threshold")
	}
	p.w.logHeldLocked(now.Add(11*time.Second), d, false)
	p.w.logHeldLocked(now.Add(20*time.Second), d, false)
	if strings.Count(out.String(), "queue held") != 1 {
		t.Fatal(out.String())
	}
	p.w.logHeldLocked(now.Add(41*time.Second), d, false)
	if strings.Count(out.String(), "queue held") != 2 {
		t.Fatal(out.String())
	}
	if !strings.Contains(out.String(), "draft_or_placeholder=true") || !strings.Contains(out.String(), "messages=[message-id]") || strings.Contains(out.String(), "private owner words") {
		t.Fatal(out.String())
	}
	input := p.w.queued[0]
	p.w.queued = nil
	p.w.typeMail(input)
	finishOwnerSubmission(t, p)
	if !strings.Contains(out.String(), "queue released") {
		t.Fatal(out.String())
	}
}
