package term

import (
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
	"strings"
	"testing"
	"time"
)

func TestMailImmediatelySubmitsInEveryNonMenuState(t *testing.T) {
	for _, owner := range []bool{false, true} {
		for _, state := range []model.AgentState{model.StateIdle, model.StateWorking, model.StateStarting, model.StateWaiting} {
			for _, draft := range []string{"", " existing draft\r\nsecond line"} {
				t.Run(string(state)+draft, func(t *testing.T) {
					p, bp := settleTestProcess(t)
					close(bp.release)
					drainOwnerWritesStarted(p, bp)
					p.state = state
					p.w.ready = false
					p.w.humanInputAt = time.Now()
					p.w.busyUntil = time.Now().Add(time.Hour)
					p.w.scr.Write([]byte("\x1b[2J\x1b[H" + claudeBox(draft)))
					if err := p.w.sendMail(mailInput{text: "message", now: owner}); err != nil {
						t.Fatal(err)
					}
					want := "message"
					if draft != "" {
						expectOwnerWrite(t, bp, strings.Repeat("\x1b[F", len(p.w.scr.Lines())+1))
						want = "  message"
					}
					expectOwnerWrite(t, bp, want)
					expectOwnerWrite(t, bp, "\r")
					finishOwnerSubmission(t, p)
				})
			}
		}
	}
}

func TestMixedMailFIFOBackToBackAfterSlowPaste(t *testing.T) {
	p, bp := settleTestProcess(t)
	done := make(chan error, 1)
	go func() { done <- p.SendMail("agent first", []string{"a"}) }()
	select {
	case <-bp.entered:
	case <-time.After(time.Second):
		t.Fatal("first paste not entered")
	}
	if err := p.Interject("owner second", []string{"b"}); err != nil {
		t.Fatal(err)
	}
	if err := p.SendMail("agent third", []string{"c"}); err != nil {
		t.Fatal(err)
	}
	noOwnerWrite(t, bp)
	close(bp.release)
	drainOwnerWritesStarted(p, bp)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"agent first", "\r", "owner second", "\r", "agent third", "\r"} {
		expectOwnerWrite(t, bp, want)
	}
	finishOwnerSubmission(t, p)
	noOwnerWrite(t, bp)
}

func TestMenuIsEscapedBeforeMailCanSelectAnOption(t *testing.T) {
	p, bp := settleTestProcess(t)
	close(bp.release)
	drainOwnerWritesStarted(p, bp)
	p.w.scr.Write([]byte("\x1b[2J\x1b[H" + menuScreen))
	sent := make(chan error, 1)
	go func() { sent <- p.Interject("1 owner message", []string{"owner"}) }()
	expectOwnerWrite(t, bp, "\x1b")
	noOwnerWrite(t, bp)
	// Emulate Claude acknowledging Esc, without a timer or readiness transition.
	workingOwnerScreen(p)
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	expectOwnerWrite(t, bp, "1 owner message")
	expectOwnerWrite(t, bp, "\r")
	finishOwnerSubmission(t, p)
}

func TestMenuThatIgnoresEscapeRetainsMailWithoutSelecting(t *testing.T) {
	p, bp := settleTestProcess(t)
	close(bp.release)
	drainOwnerWritesStarted(p, bp)
	p.w.scr.Write([]byte("\x1b[2J\x1b[H" + menuScreen))
	if err := p.Interject("1 owner message", []string{"owner"}); err != nil {
		t.Fatal(err)
	}
	expectOwnerWrite(t, bp, "\x1b")
	noOwnerWrite(t, bp)
	p.mu.Lock()
	held := p.w.mailMenuHeld && len(p.w.queued) == 1
	p.mu.Unlock()
	if !held {
		t.Fatal("undelivered message not retained")
	}
	workingOwnerScreen(p)
	p.w.evaluate()
	expectOwnerWrite(t, bp, "1 owner message")
	expectOwnerWrite(t, bp, "\r")
	finishOwnerSubmission(t, p)
}

func TestResumedMenuMailRevokesUnpastedReceiptBeforeSubmission(t *testing.T) {
	p, bp := settleTestProcess(t)
	close(bp.release)
	drainOwnerWritesStarted(p, bp)
	if err := p.w.sendMail(mailInput{text: "resumed", ids: []string{"m"}, menuWait: true}); err != nil {
		t.Fatal(err)
	}
	expectOwnerWrite(t, bp, "resumed")
	expectOwnerWrite(t, bp, "\r")
	finishOwnerSubmission(t, p)
	var receipts []string
	for {
		select {
		case ev := <-p.events:
			if ev.Kind == runner.EvMailDelivery {
				receipts = append(receipts, ev.Delivery)
			}
		default:
			if len(receipts) != 2 || receipts[0] != "dispatching" || receipts[1] != "submitted" {
				t.Fatal(receipts)
			}
			return
		}
	}
}
