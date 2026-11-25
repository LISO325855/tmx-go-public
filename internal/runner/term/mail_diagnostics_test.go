package term

import (
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/runner"
)

func TestMailDiagnosticsRemainReadableDuringBlockedPaste(t *testing.T) {
	p, bp := newGuardedTestProcess(t)
	p.done = make(chan struct{})
	defer close(p.done)
	p.w.info.SubmitDelay = time.Millisecond
	p.w.scr.Write([]byte(claudeBox("")))
	done := make(chan struct{})
	go func() { defer close(done); _ = p.Interject("private owner words", []string{"owner-mail"}) }()
	select {
	case <-bp.entered:
	case <-time.After(time.Second):
		t.Fatal("paste not started")
	}
	within(t, time.Second, "diagnosis of blocked write", func() {
		if got := p.MailStage("owner-mail"); got != "paste_write" {
			t.Errorf("stage %q", got)
		}
	})
	close(bp.release)
	<-done
	// Drain the fake's entry signal for the subsequent Enter write.
	select {
	case <-bp.entered:
	case <-time.After(time.Second):
		t.Fatal("no Enter")
	}
	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-p.Events():
			if ev.Kind == runner.EvMailDelivery {
				if ev.Delivery != "submitted" {
					t.Fatal(ev)
				}
				return
			}
		case <-deadline:
			t.Fatal("no receipt")
		}
	}
}

func TestMailProgressTimestampTracksStageRatherThanQueueAge(t *testing.T) {
	p, _ := newGuardedTestProcess(t)
	in := mailInput{ids: []string{"timed"}}
	old := time.Now().Add(-10 * time.Minute).UnixMilli()
	p.deliveryStages.Store("timed", mailProgress{stage: "runner_queue", since: old})
	p.w.mailStage(in, "runner_queue")
	if stage, since := p.MailProgress("timed"); stage != "runner_queue" || since != old {
		t.Fatal("unchanged stage lost original time")
	}
	before := time.Now().UnixMilli()
	p.w.mailStage(in, "paste_write")
	if stage, since := p.MailProgress("timed"); stage != "paste_write" || since < before {
		t.Fatal("write inherited queue age")
	}
	p.ForgetMailStage("timed")
	if stage, since := p.MailProgress("timed"); stage != "" || since != 0 {
		t.Fatal("forgotten progress retained")
	}
}

func TestReviewShellBlockedWriteHasProgress(t *testing.T) {
	p, bp := newGuardedTestProcess(t)
	p.w = nil // shell processes have no watcher
	done := make(chan struct{})
	go func() { defer close(done); _ = p.SendMail("private owner words", []string{"shell-mail"}) }()
	select {
	case <-bp.entered:
	case <-time.After(time.Second):
		t.Fatal("write not started")
	}
	stage, since := p.MailProgress("shell-mail")
	close(bp.release)
	<-done
	if stage != "shell_write" || since <= 0 {
		t.Fatalf("real blocked shell write invisible to watchdog: stage=%q since=%d", stage, since)
	}
	if got := p.MailStage("shell-mail"); got != "submitted" {
		t.Fatalf("completed shell write still appears blocked: %s", got)
	}
	select {
	case ev := <-p.Events():
		if ev.Kind != runner.EvMailDelivery || ev.Delivery != "submitted" {
			t.Fatalf("receipt: %+v", ev)
		}
	default:
		t.Fatal("missing shell receipt")
	}
	p.ForgetMailStage("shell-mail")
	if stage, since := p.MailProgress("shell-mail"); stage != "" || since != 0 {
		t.Fatal("shell progress not cleaned up")
	}
}

type shortShellPty struct{ *blockingPty }

func (*shortShellPty) Write(b []byte) (int, error) { return len(b) - 1, nil }

func TestShellPartialWriteProgressBecomesUnconfirmed(t *testing.T) {
	p := newTestProcess(t, &shortShellPty{newBlockingPty()})
	p.w = nil
	if err := p.SendMail("owner words", []string{"partial"}); err != nil {
		t.Fatalf("partial effect invited replay: %v", err)
	}
	if stage, since := p.MailProgress("partial"); stage != "unconfirmed" || since <= 0 {
		t.Fatalf("partial write progress %s %d", stage, since)
	}
	ev := <-p.Events()
	if ev.Kind != runner.EvMailDelivery || ev.Delivery != "unconfirmed" {
		t.Fatalf("receipt %+v", ev)
	}
}
