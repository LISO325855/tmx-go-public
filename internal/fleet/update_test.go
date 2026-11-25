package fleet

import (
	"slices"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
)

func TestUpdateBusyIdleHeartbeatAndQuietTimeout(t *testing.T) {
	e := newUnguardedEnv(t)
	f := e.started()
	a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: workerProject(t, f), Name: "update-probe", Harness: model.HarnessClaude, Mode: model.ModeTerminal})
	if err != nil {
		t.Fatal(err)
	}
	p := e.term.Last(a.ID)
	f.mu.Lock()
	r := f.agents[a.ID]
	f.mu.Unlock()
	busy := func(now time.Time) bool { return slices.Contains(f.UpdateBusy(now), a.Name) }
	// Simulate the watcher's periodic positive idle event repairing a fleet state
	// left working by a missed transition or an earlier turn.
	f.handleEvent(r, p, runner.Event{Kind: runner.EvState, State: model.StateWorking})
	if !busy(time.Now()) {
		t.Fatal("active turn must block")
	}
	f.handleEvent(r, p, runner.Event{Kind: runner.EvState, State: model.StateIdle})
	if busy(time.Now()) {
		t.Fatal("positive idle heartbeat must release the update")
	}
	// Resource brakes retain StatePaused even when the runner reports idle.
	f.mu.Lock()
	r.a.State = model.StatePaused
	if f.res.paused == nil {
		f.res.paused = map[string]*pauseRec{}
	}
	f.res.paused[a.ID] = &pauseRec{prev: model.StateWorking}
	f.mu.Unlock()
	f.handleEvent(r, p, runner.Event{Kind: runner.EvState, State: model.StateIdle})
	if busy(time.Now()) {
		t.Fatal("fresh idle evidence must supersede masked state")
	}
	if !busy(time.Now().Add(6 * time.Second)) {
		t.Fatal("expired idle evidence must not override busy state")
	}
	f.mu.Lock()
	delete(f.res.paused, a.ID)
	f.mu.Unlock()
	// A new turn invalidates the earlier idle evidence immediately.
	f.handleEvent(r, p, runner.Event{Kind: runner.EvState, State: model.StateWorking})
	if !busy(time.Now()) {
		t.Fatal("new working state must invalidate idle evidence")
	}
	for _, state := range []model.AgentState{model.StateWorking, model.StateStarting, model.StateWaiting, model.StatePaused} {
		t.Run(string(state), func(t *testing.T) {
			now := time.Now()
			f.mu.Lock()
			r.a.State = state
			r.updateActivity = now.Add(-UpdateQuietLimit)
			r.updateIdleAt = time.Time{}
			f.mu.Unlock()
			if !busy(now.Add(-time.Millisecond)) {
				t.Fatal("must wait the full quiet interval")
			}
			// Raw terminal redraws and operational notes are not turn activity.
			f.handleEvent(r, p, runner.Event{Kind: runner.EvOutput, Output: []byte("redraw")})
			f.handleEvent(r, p, runner.Event{Kind: runner.EvTimeline, Entry: &model.TimelineEntry{Kind: model.EntrySystem, Text: "system note"}})
			if busy(now) {
				t.Fatal("stuck state must not block after the quiet interval")
			}
			f.handleEvent(r, p, runner.Event{Kind: runner.EvTimeline, Entry: &model.TimelineEntry{Kind: model.EntryToolUse, Text: "real activity"}})
			if !busy(time.Now()) {
				t.Fatal("turn activity must reset the quiet timer")
			}
		})
	}
	// Unknown/adopted timestamps get a full observation window, not an
	// immediate timeout based on an old persisted LastActivityAt.
	now := time.Now()
	f.mu.Lock()
	r.updateActivity = time.Time{}
	r.a.LastActivityAt = 1
	f.mu.Unlock()
	if !busy(now) || busy(now.Add(UpdateQuietLimit)) {
		t.Fatal("unknown activity must get exactly one observation window")
	}
	f.handleEvent(r, p, runner.Event{Kind: runner.EvState, State: model.StatePaused})
	if !busy(time.Now()) {
		t.Fatal("runner state evidence must reset the timer even if fleet state matches")
	}
	// The fallback does not dismiss or bypass a real permission request.
	f.handleEvent(r, p, runner.Event{Kind: runner.EvApproval, Approval: &runner.ApprovalRequest{ID: "update-approval", Tool: "Bash"}})
	if !slices.Contains(f.UpdateBusy(now.Add(2*UpdateQuietLimit)), "1 open approvals") {
		t.Fatal("real approval must remain an independent blocker")
	}
}

func TestReviewUpdateMustNotTreatLiveTerminalProgressAsQuiet(t *testing.T) {
	e := newUnguardedEnv(t)
	f := e.started()
	a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: workerProject(t, f), Name: "active-terminal", Harness: model.HarnessCodex, Mode: model.ModeTerminal})
	if err != nil {
		t.Fatal(err)
	}
	p := e.term.Last(a.ID)
	f.mu.Lock()
	r := f.agents[a.ID]
	f.mu.Unlock()
	f.handleEvent(r, p, runner.Event{Kind: runner.EvState, State: model.StateWorking})
	f.mu.Lock()
	r.updateActivity = time.Now().Add(-16 * time.Minute)
	r.updateIdleAt = time.Time{}
	f.mu.Unlock()
	// Review regression: the still-working Codex screen adds "Completed batch
	// 109; processing batch 110". The terminal watcher now reports that rendered
	// change as EvProgress; TestCodexScreenProgressIgnoresRedrawsAndSpinners
	// exercises the raw-output-to-semantic-event boundary.
	f.handleEvent(r, p, runner.Event{Kind: runner.EvProgress})
	if !slices.Contains(f.UpdateBusy(time.Now()), a.Name) {
		t.Fatal("live terminal progress was ignored; update may restart during active turn")
	}
	f.mu.Lock()
	last := r.updateActivity
	f.mu.Unlock()
	if slices.Contains(f.UpdateBusy(last.Add(UpdateQuietLimit)), a.Name) {
		t.Fatal("terminal that stops making progress must still time out")
	}
}

func TestUpdateHardBusyLimitIgnoresScreenActivity(t *testing.T) {
	e := newUnguardedEnv(t)
	f := e.started()
	a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: workerProject(t, f), Name: "stuck-screen", Harness: model.HarnessCodex, Mode: model.ModeTerminal})
	if err != nil {
		t.Fatal(err)
	}
	p := e.term.Last(a.ID)
	f.mu.Lock()
	r := f.agents[a.ID]
	f.mu.Unlock()
	f.handleEvent(r, p, runner.Event{Kind: runner.EvState, State: model.StateWorking})
	now := time.Now()
	f.mu.Lock()
	r.updateTurnActivity = now.Add(-UpdateBusyLimit)
	f.mu.Unlock()
	for i := 0; i < 3; i++ {
		f.handleEvent(r, p, runner.Event{Kind: runner.EvProgress})
		// A duplicate state report also cannot extend the hard deadline.
		f.handleEvent(r, p, runner.Event{Kind: runner.EvState, State: model.StateWorking})
	}
	if !slices.Contains(f.UpdateBusy(now.Add(-time.Millisecond)), a.Name) {
		t.Fatal("hard deadline expired early")
	}
	if slices.Contains(f.UpdateBusy(now), a.Name) {
		t.Fatal("screen activity extended the two-hour hard deadline")
	}
	for _, ev := range []runner.Event{
		{Kind: runner.EvState, State: model.StateStarting},
		{Kind: runner.EvTimeline, Entry: &model.TimelineEntry{Kind: model.EntryToolUse, Text: "real transcript activity"}},
		{Kind: runner.EvTimeline, Entry: &model.TimelineEntry{Kind: model.EntryUser, Text: "new turn", Meta: map[string]any{"source": "screen"}}},
	} {
		f.mu.Lock()
		r.updateTurnActivity = now.Add(-UpdateBusyLimit)
		f.mu.Unlock()
		f.handleEvent(r, p, ev)
		if !slices.Contains(f.UpdateBusy(time.Now()), a.Name) {
			t.Fatalf("state transition or turn event did not reset hard deadline: %+v", ev)
		}
	}
	// Fleet-owned transitions (for example a resource brake) count too, even
	// when no runner state event caused them.
	f.mu.Lock()
	r.updateTurnActivity = now.Add(-UpdateBusyLimit)
	r.a.State = model.StatePaused
	f.saveAgentLocked(r)
	f.mu.Unlock()
	if !slices.Contains(f.UpdateBusy(time.Now()), a.Name) {
		t.Fatal("fleet-owned transition did not reset hard deadline")
	}
}
