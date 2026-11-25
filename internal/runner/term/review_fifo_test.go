package term

import (
	"github.com/blackcode-ch/tmx-go/internal/model"
	"testing"
	"time"
)

func TestOwnerFIFOWhileOlderPasteWaitsForInputLock(t *testing.T) {
	p, bp := newGuardedTestProcess(t)
	close(bp.release)
	p.done = make(chan struct{})
	defer close(p.done)
	p.state = model.StateWorking
	p.w.scr.Write([]byte("Thinking (esc to interrupt)\r\n" + claudeBox("")))
	p.w.inputMu.Lock()
	done := make(chan struct{})
	go func() { defer close(done); _ = p.w.sendMail(mailInput{text: "older", now: true}) }()
	deadline := time.Now().Add(time.Second)
	for {
		p.mu.Lock()
		selected := p.w.mailTyping
		p.mu.Unlock()
		if selected {
			break
		}
		if time.Now().After(deadline) {
			p.w.inputMu.Unlock()
			t.Fatal("older never selected")
		}
		time.Sleep(time.Millisecond)
	}
	if err := p.w.sendMail(mailInput{text: "newer", now: true}); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	if len(p.w.queued) != 1 || p.w.queued[0].text != "newer" {
		t.Error("newer was not held behind selected older input")
	}
	p.mu.Unlock()
	p.w.inputMu.Unlock()
	<-done
	if got := string(<-bp.writes); got != "older" {
		t.Fatal(got)
	}
}
