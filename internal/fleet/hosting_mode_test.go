package fleet

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
)

// A chat agent that was switched to its harness's own screen keeps running there in its session
// host across a daemon restart. The saved terminal mode lets the next daemon
// attach using the correct protocol without starting a second copy.
func TestDaemonRestartAfterALiveSwitchReattachesInTheRunningMode(t *testing.T) {
	quickSwitch(t)
	e := newEnv(t)
	hc, ht := newHostedRunner(e.chat), newHostedRunner(e.term)
	hc.sibling, ht.sibling = ht, hc
	e.hostExe, e.chatRunner, e.termRunner = "/opt/tmx/tmx-host", hc, ht
	f1 := e.started()
	a, err := f1.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: workerProject(t, f1), Name: "w-switched"})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "chat started", func() bool { return e.chat.Last(a.ID) != nil })
	chat := e.chat.Last(a.ID)
	chat.Emit(runner.Event{Kind: runner.EvSession, SessionID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"})
	chat.Turn("ready", 0.1, 1)
	chat.State(model.StateIdle)
	eventually(t, "idle in chat", func() bool { return mustAgent(t, f1, "w-switched").State == model.StateIdle })

	if _, err := f1.SetMode(OwnerCaller, a.ID, ModeRequest{Mode: model.ModeTerminal}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the screen started", func() bool { return e.term.Last(a.ID) != nil })
	tui := e.term.Last(a.ID)
	eventually(t, "switched", func() bool {
		got := mustAgent(t, f1, "w-switched")
		return got.Mode == model.ModeTerminal && got.HomeMode == model.ModeChat
	})
	if stored, _ := e.st.GetAgent(a.ID); stored.Mode != model.ModeTerminal {
		t.Fatalf("the store lost the selected terminal mode, has %s", stored.Mode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f1.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if tui.Stopped() {
		t.Fatal("the daemon's stop stopped the hosted screen")
	}

	f2 := e.started()
	eventually(t, "reattached to the screen", func() bool {
		got, err := f2.Agent(a.ID)
		return err == nil && got.PID == tui.PID() && got.Mode == model.ModeTerminal
	})
	if e.chat.Last(a.ID) != chat || e.term.Last(a.ID) != tui {
		t.Fatal("another copy of the switched agent was started")
	}
	ht.mu.Lock()
	n := ht.reattached
	ht.mu.Unlock()
	if n != 1 {
		t.Fatalf("the screen's host was reattached %d times", n)
	}
	if !timelineHas(t, f2, a.ID, "still running: reattached after the daemon restarted") {
		t.Fatal("the timeline does not say what happened")
	}

	// What stuck before: the first prompt after the restart. It reaches the program that runs.
	tui.State(model.StateIdle)
	if _, err := f2.SendOwner(a.ID, "reply with the word ok", ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "delivered to the screen", func() bool {
		for _, s := range tui.Sent() {
			if strings.Contains(s, "reply with the word ok") {
				return true
			}
		}
		return strings.Contains(string(tui.Written()), "reply with the word ok")
	})
	if sent := chat.Sent(); len(sent) != 0 && strings.Contains(strings.Join(sent, "\n"), "reply with the word ok") {
		t.Fatal("the prompt went to the stopped chat process")
	}
	_ = time.Second
}
