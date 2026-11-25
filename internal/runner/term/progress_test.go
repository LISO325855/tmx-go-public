package term

import (
	"fmt"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/harness"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
	"github.com/blackcode-ch/tmx-go/internal/runner/term/screen"
)

func TestCodexScreenProgressIgnoresRedrawsAndSpinners(t *testing.T) {
	p, _ := newGuardedTestProcess(t)
	p.w.info, _ = harness.Get(model.HarnessCodex)
	p.state = model.StateWorking // no further state transition during the long turn
	paint := func(body, status string) int {
		t.Helper()
		p.w.output([]byte("\x1b[2J\x1b[H" + body + "\r\n" + status))
		settleProgressFrame(p)
		p.w.evaluate()
		count := 0
		for len(p.events) > 0 {
			ev := <-p.events
			if ev.Kind == runner.EvState {
				t.Fatalf("progress must not require a state transition: %+v", ev)
			}
			if ev.Kind == runner.EvProgress {
				count++
			}
		}
		return count
	}
	body := "Completed batch 109; processing batch 110"
	if n := paint(body, "⠋ Working (1s • esc to interrupt)"); n != 1 {
		t.Fatalf("new output: %d progress events", n)
	}
	for _, status := range []string{"⠙ Working (2s • esc to interrupt)", "⠹ Working (999s • esc to interrupt)"} {
		if n := paint("\x1b[32m"+body+"\x1b[0m", status); n != 0 {
			t.Fatalf("cosmetic redraw counted as progress: %d", n)
		}
	}
	if n := paint("", "⠸ Working (1000s • esc to interrupt)"); n != 0 {
		t.Fatal("clearing text counted as progress")
	}
	if n := paint(body, "⠼ Working (1001s • esc to interrupt)"); n != 0 {
		t.Fatal("restoring identical text counted as progress")
	}
	for batch := 110; batch < 113; batch++ {
		body += fmt.Sprintf("\r\nCompleted batch %d; processing batch %d", batch, batch+1)
		if n := paint(body, "Working (1200s • esc to interrupt)"); n != 1 {
			t.Fatalf("changing progress without state transitions: %d", n)
		}
	}
	if n := paint("Completed batch 112; processing batch 113\r\nWriting results to disk", "Working (1201s • esc to interrupt)"); n != 1 {
		t.Fatal("new output line was ignored")
	}
	if n := paint("Publishing results for batch 114", ""); n != 1 {
		t.Fatal("output during an active turn without a visible footer was ignored")
	}
	paint("⠋ Reconnecting in 5s", "")
	for seconds := 4; seconds >= 0; seconds-- {
		if n := paint(fmt.Sprintf("⠙ Reconnecting in %ds", seconds), ""); n != 0 {
			t.Fatal("footer-less countdown was counted as progress")
		}
	}
}

// Observe the candidate, then advance its stability clock without sleeping per paint.
func settleProgressFrame(p *process) {
	p.w.evaluate()
	p.mu.Lock()
	p.w.progressCandidateAt = time.Now().Add(-progressSettle - time.Millisecond)
	p.mu.Unlock()
}

func TestProgressNumericRewritesVersusAppendedRows(t *testing.T) {
	for _, tt := range []struct {
		name          string
		before, after []string
		want          bool
	}{
		{"countdown", []string{"Reconnecting in 5s"}, []string{"Reconnecting in 4s"}, false},
		{"timer", []string{"Elapsed 99s"}, []string{"Elapsed 100s"}, false},
		{"counter rewritten", []string{"Completed batch 109"}, []string{"Completed batch 110"}, false},
		{"counter appended", []string{"Completed batch 109"}, []string{"Completed batch 109", "Completed batch 110"}, true},
		{"counter scroll", []string{"Completed batch 109", "Completed batch 110"}, []string{"Completed batch 110", "Completed batch 111"}, true},
		{"counter scroll under header", []string{"Task", "Completed batch 109", "Completed batch 110", "Footer"}, []string{"Task", "Completed batch 110", "Completed batch 111", "Footer"}, true},
		{"fixed body countdown", []string{"Processing", "Reconnecting in 5s"}, []string{"Processing", "Reconnecting in 4s"}, false},
		{"new words", []string{"Reading file 1"}, []string{"Writing file 2"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := addedProgress(tt.before, tt.after); got != tt.want {
				t.Fatalf("progress = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReviewFooterlessStatusAnimationDoesNotCreateProgress(t *testing.T) {
	p, _ := newGuardedTestProcess(t)
	p.w.info, _ = harness.Get(model.HarnessCodex)
	p.state = model.StateWorking
	for i := 0; i < 6; i++ {
		// An unchanged body and a cycling wait indicator, without a recognized footer.
		status := fmt.Sprintf("\u280b Reconnecting in %ds", 5-i%5)
		p.w.output([]byte("\x1b[2J\x1b[HUnchanged answer\r\n" + status))
		settleProgressFrame(p)
		p.w.evaluate()
		count := 0
		for len(p.events) > 0 {
			if (<-p.events).Kind == runner.EvProgress {
				count++
			}
		}
		if i > 0 && count != 0 {
			t.Fatalf("footerless countdown created %d progress events on frame %d", count, i)
		}
		if i == 0 && count != 1 {
			t.Fatal("initial settled frame did not establish progress baseline")
		}
	}
}
func TestReviewFragmentedIdenticalRepaintDoesNotCreateProgress(t *testing.T) {
	p, _ := newGuardedTestProcess(t)
	p.w.info, _ = harness.Get(model.HarnessCodex)
	p.state = model.StateWorking
	p.w.output([]byte("\x1b[2J\x1b[HUnchanged answer\r\nWorking (1s esc to interrupt)"))
	settleProgressFrame(p)
	p.w.evaluate()
	initial := 0
	for len(p.events) > 0 {
		if (<-p.events).Kind == runner.EvProgress {
			initial++
		}
	}
	if initial != 1 {
		t.Fatal("missing initial settled baseline")
	}
	// Same final display, but a clear-and-restore paint arrives in two chunks.
	for _, chunk := range []string{"\x1b[2J\x1b[HUnchanged ans", "wer\r\nWorking (2s esc to interrupt)"} {
		p.w.output([]byte(chunk))
		p.w.evaluate()
		for len(p.events) > 0 {
			if (<-p.events).Kind == runner.EvProgress {
				t.Fatal("fragment of identical repaint counted as task progress")
			}
		}
	}
	settleProgressFrame(p)
	p.w.evaluate()
	for len(p.events) > 0 {
		if (<-p.events).Kind == runner.EvProgress {
			t.Fatal("completed identical repaint changed settled baseline")
		}
	}
	// A genuinely changed frame is delayed, then reported exactly once.
	p.w.output([]byte("\x1b[2J\x1b[HA new answer\r\nWorking (3s esc to interrupt)"))
	p.w.evaluate()
	for len(p.events) > 0 {
		if (<-p.events).Kind == runner.EvProgress {
			t.Fatal("unsettled frame counted")
		}
	}
	settleProgressFrame(p)
	p.w.evaluate()
	p.w.evaluate()
	count := 0
	for len(p.events) > 0 {
		if (<-p.events).Kind == runner.EvProgress {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("settled new frame: %d progress events", count)
	}
}

func TestReviewSpinnerDoesNotStarveRealProgress(t *testing.T) {
	p, _ := newGuardedTestProcess(t)
	p.w.info, _ = harness.Get(model.HarnessCodex)
	p.state = model.StateWorking
	p.w.output([]byte("\x1b[2J\x1b[HReading input\r\nWorking (1s esc to interrupt)"))
	settleProgressFrame(p)
	p.w.evaluate()
	for len(p.events) > 0 {
		<-p.events
	}
	// The body is complete and unchanged throughout; only the status redraws.
	p.w.output([]byte("\x1b[2J\x1b[HReading input\r\nWriting verified results\r\nWorking (2s esc to interrupt)"))
	count := 0
	for i := 0; i < 10; i++ {
		p.w.output([]byte(fmt.Sprintf("\x1b[3;1HWorking (%ds esc to interrupt)", i+3)))
		time.Sleep(100 * time.Millisecond)
		p.w.evaluate()
		for len(p.events) > 0 {
			if (<-p.events).Kind == runner.EvProgress {
				count++
			}
		}
	}
	if count == 0 {
		t.Fatal("complete genuine output was starved by cosmetic status redraws for longer than progressSettle")
	}
}
func TestReviewStreamingOutputDoesNotStarveProgress(t *testing.T) {
	p, _ := newGuardedTestProcess(t)
	p.w.info, _ = harness.Get(model.HarnessCodex)
	p.state = model.StateWorking
	p.w.output([]byte("\x1b[2J\x1b[HReading input\r\nWorking (1s esc to interrupt)"))
	settleProgressFrame(p)
	p.w.evaluate()
	for len(p.events) > 0 {
		<-p.events
	}
	body := "Reading input"
	count := 0
	// Stream beyond the two-second maximum wait without any stable frame.
	for i := 0; i < 24; i++ {
		body += "\r\nVerified another result"
		p.w.output([]byte("\x1b[2J\x1b[H" + body + "\r\nWorking (2s esc to interrupt)"))
		time.Sleep(100 * time.Millisecond)
		p.w.evaluate()
		for len(p.events) > 0 {
			if (<-p.events).Kind == runner.EvProgress {
				count++
			}
		}
	}
	if count == 0 {
		t.Fatal("continuously appended completed output rows never emitted progress")
	}
}

func TestProgressMaxWaitAndBaselineReturn(t *testing.T) {
	p, _ := newGuardedTestProcess(t)
	p.state = model.StateWorking
	p.w.progressText = []string{"Original"}
	start := time.Now()
	for i := 0; i <= 20; i++ {
		body := []string{fmt.Sprintf("Result %c", 'a'+i)}
		p.w.settleProgressLocked(screen.Working, body, start.Add(time.Duration(i)*100*time.Millisecond))
		count := 0
		for len(p.events) > 0 {
			if (<-p.events).Kind == runner.EvProgress {
				count++
			}
		}
		want := 0
		if i == 20 {
			want = 1
		}
		if count != want {
			t.Fatalf("at %d ms: got %d progress events, want %d", i*100, count, want)
		}
	}
	baseline := p.w.progressText
	p.w.settleProgressLocked(screen.Working, []string{"Partial repaint"}, start.Add(3*time.Second))
	p.w.settleProgressLocked(screen.Working, baseline, start.Add(3100*time.Millisecond))
	if !p.w.progressPendingAt.IsZero() {
		t.Fatal("return to baseline retained pending maximum-wait clock")
	}
	p.w.settleProgressLocked(screen.Working, []string{"Another partial repaint"}, start.Add(5*time.Second))
	for len(p.events) > 0 {
		if (<-p.events).Kind == runner.EvProgress {
			t.Fatal("old pending clock allowed immediate partial-frame progress")
		}
	}
}
