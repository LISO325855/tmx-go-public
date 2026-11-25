package fleet

import (
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
)

// Mail is delivered when it is sent and when its receiver becomes idle (the runner's state
// event). A safety net for a missed moment (17 Nov on hosta-go: mail waited until the owner
// wrote again): every mailBackstopEvery, each idle agent with a process is offered its queued
// mail once more; working terminals also retry held owner turns. tryDeliver keeps every rule (paused, held, someone typing, the owner's
// messages first); with nothing queued it does nothing.
const mailBackstopEvery = 10 * time.Second

func (f *Fleet) startMailLoop() {
	f.mailLoopOnce.Do(func() {
		// Independent of delivery: a stuck runner.Send must not also stop the watchdog.
		f.goTracked(func() {
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for {
				select {
				case <-f.ctx.Done():
					return
				case now := <-tick.C:
					f.surfaceStalledOwnerMail(now)
				}
			}
		})
		f.goTracked(func() {
			t := time.NewTicker(mailBackstopEvery)
			defer t.Stop()
			for {
				select {
				case <-f.ctx.Done():
					return
				case <-t.C:
					f.deliverWaiting()
				}
			}
		})
	})
}

// deliverWaiting offers idle agents mail and working terminals held owner turns.
func (f *Fleet) deliverWaiting() {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	var ids []string
	for id, r := range f.agents {
		if r.proc != nil && (r.a.State == model.StateIdle || typedTerminal(r.a)) {
			ids = append(ids, id)
		}
	}
	f.mu.Unlock()
	for _, id := range ids {
		f.tryDeliver(id)
	}
}

const ownerDispatchTimeout = 60 * time.Second

// Receipt changes and late submission events share f.mu: a timeout cannot
// overwrite an already submitted/read message. The original effect is never
// automatically replayed; a late real submission can still replace the warning.
func (f *Fleet) surfaceStalledOwnerMail(now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	msgs, err := f.st.OverdueOwnerDispatches(now.Add(-ownerDispatchTimeout).UnixMilli())
	if err != nil {
		f.log.Error("check owner dispatch timeout", "err", err)
		return
	}
	for _, m := range msgs {
		if m.State == model.MsgRead {
			continue
		}
		r := f.agents[m.To]
		if r == nil {
			continue
		}
		d, ok := r.proc.(runner.MailProgressDiagnoser)
		if !ok {
			continue
		} // unknown handoff is not proof of a stalled write
		stage, since := d.MailProgress(m.ID)
		if !stalledWriteStage(stage) || since <= 0 || now.UnixMilli()-since <= ownerDispatchTimeout.Milliseconds() {
			continue
		}

		f.log.Warn("owner message dispatch timed out", "agent", m.To, "message", m.ID, "stage", stage, "after_s", now.Sub(time.UnixMilli(since)).Seconds())
		f.mailReceiptLocked(m.To, []string{m.ID}, "stalled")
	}
}

// Only active write/lock operations can time out. Queue, draft, busy, menu and
// unknown stages may be legitimate holds and must never invite a duplicate retry.
func stalledWriteStage(stage string) bool {
	switch stage {
	case "shell_write", "input_lock_wait", "paste_write", "enter_write", "submit_menu_check", "composer_recheck", "screen_check":
		return true
	default:
		return false
	}
}
