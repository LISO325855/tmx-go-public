package fleet

import (
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// A shell runs its start command again in every new process: after a restart and after a stop
// and start. A shell without a start command stays an empty prompt, and a Claude agent's brief
// is never typed again.
func TestAShellRunsItsStartCommandAgainInEveryNewProcess(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	const cmd = "python feed.py --port 8101"

	sh, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "feed", Harness: model.HarnessShell, Brief: cmd})
	if err != nil {
		t.Fatal(err)
	}
	first := e.term.Last(sh.ID)
	eventually(t, "the command at launch", func() bool { return len(first.Sent()) == 1 && first.Sent()[0] == cmd })

	if _, err := f.RestartAgentWith(OwnerCaller, "feed", RestartOptions{}); err != nil {
		t.Fatal(err)
	}
	var second = first
	eventually(t, "a new process after the restart", func() bool { second = e.term.Last(sh.ID); return second != first })
	eventually(t, "the command after the restart", func() bool { return len(second.Sent()) == 1 && second.Sent()[0] == cmd })

	if _, err := f.StopAgent(OwnerCaller, "feed"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.StartAgent(OwnerCaller, "feed"); err != nil {
		t.Fatal(err)
	}
	var third = second
	eventually(t, "a new process after stop and start", func() bool { third = e.term.Last(sh.ID); return third != second })
	eventually(t, "the command after stop and start", func() bool { return len(third.Sent()) == 1 && third.Sent()[0] == cmd })

	// Launched with a first line and no brief: that line is kept as the start command.
	sh2, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "watch", Harness: model.HarnessShell, Prompt: "node watch.js"})
	if err != nil || mustAgent(t, f, "watch").Brief != "node watch.js" {
		t.Fatalf("a first line as the start command: %+v %v", mustAgent(t, f, "watch"), err)
	}
	p2 := e.term.Last(sh2.ID)
	eventually(t, "the first line at launch", func() bool { return len(p2.Sent()) == 1 })
	if _, err := f.RestartAgentWith(OwnerCaller, "watch", RestartOptions{}); err != nil {
		t.Fatal(err)
	}
	var p2b = p2
	eventually(t, "watch restarted", func() bool { p2b = e.term.Last(sh2.ID); return p2b != p2 })
	eventually(t, "watch runs its line again", func() bool { return len(p2b.Sent()) == 1 && p2b.Sent()[0] == "node watch.js" })

	// No start command: an empty prompt after a restart, as before.
	plain, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "plain", Harness: model.HarnessShell})
	if err != nil {
		t.Fatal(err)
	}
	pp := e.term.Last(plain.ID)
	if _, err := f.RestartAgentWith(OwnerCaller, "plain", RestartOptions{}); err != nil {
		t.Fatal(err)
	}
	var pp2 = pp
	eventually(t, "plain restarted", func() bool { pp2 = e.term.Last(plain.ID); return pp2 != pp })
	if len(pp.Sent()) != 0 || len(pp2.Sent()) != 0 {
		t.Fatalf("something was typed into a shell without a start command: %v %v", pp.Sent(), pp2.Sent())
	}
}

func TestOnlyAShellGetsItsBriefAsAStartCommand(t *testing.T) {
	for _, tc := range []struct {
		a      model.Agent
		prompt string
		want   string
	}{
		{model.Agent{Harness: model.HarnessShell, Brief: "  run.cmd  "}, "", "run.cmd"},
		{model.Agent{Harness: model.HarnessShell, Brief: "run.cmd"}, "first line", "first line"},
		{model.Agent{Harness: model.HarnessShell}, "", ""},
		{model.Agent{Harness: model.HarnessClaude, Brief: "Build the report"}, "", ""},
		{model.Agent{Harness: model.HarnessCodex, Brief: "Build the report"}, "", ""},
		{model.Agent{Brief: "Build the report"}, "", ""},
	} {
		if got := shellStartCommand(tc.a, tc.prompt); got != tc.want {
			t.Errorf("%+v with %q: %q, want %q", tc.a, tc.prompt, got, tc.want)
		}
	}
}
