package fleet

import (
	"fmt"
	"sort"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// UpdateQuietLimit bounds stale working/starting/paused states. Session hosts
// survive daemon updates, so a stuck turn must not hold a verified update forever.
const UpdateQuietLimit = 15 * time.Minute

// UpdateBusyLimit bounds even misleading screen progress. Only a state
// transition or a transcript/turn event starts another two-hour window.
const UpdateBusyLimit = 2 * time.Hour

// UpdateBusy reports update blockers using live runner evidence. Positive idle
// heartbeats arrive every two seconds; allow one missed beat. Never infer turn
// activity from EvOutput: idle TUIs can redraw indefinitely. Terminal watchers
// report meaningful screen changes separately as EvProgress. Open approvals and
// other services still retain their independent install gates.
func (f *Fleet) UpdateBusy(now time.Time) []string {
	approvals, err := f.st.ListApprovals(model.ApprovalPending)
	if err != nil {
		return []string{"the fleet (" + err.Error() + ")"}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	if f.opts.ResetInFlight != nil && f.opts.ResetInFlight(now) {
		out = append(out, "account reset in progress")
	}
	for _, r := range f.agents {
		if r.updateTurnActivity.IsZero() {
			r.updateTurnActivity = now
		}
		if r.updateActivity.IsZero() {
			// Give adopted agents a full observation window, regardless of old
			// persisted timestamps. This clock is not reset on each update poll.
			r.updateActivity = now
		}
		switch r.a.State {
		case model.StateWorking, model.StateWaiting, model.StateStarting, model.StatePaused:
			if !r.updateIdleAt.IsZero() && now.Sub(r.updateIdleAt) <= 5*time.Second {
				continue
			}
			if now.Sub(r.updateActivity) >= UpdateQuietLimit || now.Sub(r.updateTurnActivity) >= UpdateBusyLimit {
				continue
			}
			out = append(out, r.a.Name)
		}
	}
	sort.Strings(out)
	if n := len(approvals); n > 0 {
		out = append(out, fmt.Sprintf("%d open approvals", n))
	}
	return out
}
