package fleet

import (
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// launchedBy records lead as the launcher of shell sh, as LaunchAgent does for an agent's
// launch (on this branch only the owner can launch a shell; the record is what differs).
func launchedBy(f *Fleet, sh model.Agent, by string) {
	f.mu.Lock()
	f.regShellLaunchedLocked(sh, f.agents[by], f.projectThreadLocked(sh.ProjectID))
	f.mu.Unlock()
}

func TestRegistryIsTheShellsHandler(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	os := &fakeOS{}
	os.use(f)
	if _, err := f.CreateProject(OwnerCaller, ProjectRequest{Name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	head, lead := mustAgent(t, f, "head"), mustAgent(t, f, "alpha-lead")
	e.chat.Last(lead.ID).State(model.StateWorking)
	w, err := f.LaunchAgent(callerFor(t, f, e.chat, lead.ID), LaunchRequest{Name: "w1"})
	if err != nil {
		t.Fatal(err)
	}
	hc, lc, wc := AgentCaller(head.ID), AgentCaller(lead.ID), AgentCaller(w.ID)

	// The owner's own shell, launched in the project with a start command.
	dev, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "dev", ProjectID: lead.ProjectID, Level: model.LevelWorker,
		Harness: model.HarnessShell, Prompt: "npm run dev -- --port 3000\nsecond line"})
	if err != nil {
		t.Fatal(err)
	}
	devID := "shell:" + dev.ID
	got := entry(registry(t, f, OwnerCaller), devID)
	if got == nil || got.Kind != model.MachineShell || got.What != "shell dev" || got.Command != "npm run dev -- --port 3000" ||
		got.Agent != "the owner" || got.AgentID != "" || got.Project != "alpha" || got.Thread != "alpha" || got.ThreadID == "" ||
		got.PID <= 0 || got.Since == 0 || got.State == "" || got.Orphan || !got.RestartsWithNode || strings.Join(got.Actions, ",") != "stop,restart" {
		t.Fatalf("the owner's shell: %+v", got)
	}
	// The head sees it but it is the owner's terminal; a worker does not see it.
	if e := entry(registry(t, f, hc), devID); e == nil || len(e.Actions) != 0 {
		t.Fatalf("head on the owner's shell: %+v", e)
	}
	if err := f.RegistryStop(hc, devID); KindOf(err) != KindForbidden || !strings.Contains(err.Error(), "the owner's own terminal") {
		t.Fatalf("head stopped the owner's shell: %v", err)
	}
	if e := entry(registry(t, f, wc), devID); e != nil {
		t.Fatalf("a worker sees the owner's shell: %+v", e)
	}

	// A shell an agent launched: launched by, project, thread, command; the launcher and the
	// head may stop and restart it, another agent may not.
	watch, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "watcher", ProjectID: lead.ProjectID, Level: model.LevelWorker,
		Harness: model.HarnessShell, Brief: "go run ./cmd/watch"})
	if err != nil {
		t.Fatal(err)
	}
	launchedBy(f, watch, lead.ID)
	id := "shell:" + watch.ID
	got = entry(registry(t, f, lc), id)
	if got == nil || got.Agent != "alpha-lead" || got.AgentID != lead.ID || got.Command != "go run ./cmd/watch" || got.Thread != "alpha" ||
		!got.RestartsWithNode || strings.Join(got.Actions, ",") != "stop,restart" {
		t.Fatalf("the lead on the shell it launched: %+v", got)
	}
	if e := entry(registry(t, f, hc), id); e == nil || strings.Join(e.Actions, ",") != "stop,restart" {
		t.Fatalf("the head on an agent's shell: %+v", e)
	}
	if err := f.RegistryStop(wc, id); KindOf(err) != KindNotFound {
		t.Fatalf("another agent stopped it: %v", err)
	}
	first := e.term.Last(watch.ID)
	if err := f.RegistryStop(lc, id); err != nil {
		t.Fatal(err)
	}
	got = entry(registry(t, f, lc), id)
	if got == nil || got.PID != 0 || got.State != "stopped" || got.RestartsWithNode || got.Orphan || strings.Join(got.Actions, ",") != "restart" || !first.Stopped() {
		t.Fatalf("after the launcher stopped it: %+v", got)
	}
	// Restart: a new process, and the start command is typed again.
	again, err := f.RegistryRestart(lc, id)
	if err != nil {
		t.Fatal(err)
	}
	second := e.term.Last(watch.ID)
	if again.ID != id || again.PID <= 0 || !again.RestartsWithNode || second == first {
		t.Fatalf("restarted: %+v", again)
	}
	eventually(t, "the start command is typed again", func() bool {
		return strings.Contains(strings.Join(second.Sent(), "\n")+string(second.Written()), "go run ./cmd/watch")
	})

	// The daily digest names the new shells, once.
	lines := f.newShells(model.Now() - 3600_000)
	if len(lines) != 2 || lines[0]+"|"+lines[1] != "dev (by the owner, alpha): npm run dev -- --port 3000|watcher (by alpha-lead, alpha): go run ./cmd/watch" &&
		lines[1]+"|"+lines[0] != "dev (by the owner, alpha): npm run dev -- --port 3000|watcher (by alpha-lead, alpha): go run ./cmd/watch" {
		t.Fatalf("new shells: %q", lines)
	}
	if got := f.newShells(model.Now() + 1); len(got) != 0 {
		t.Fatalf("shells from before the window: %q", got)
	}
	hp := e.chat.Last(head.ID)
	hp.State(model.StateIdle)
	waitState(t, f, "head", model.StateIdle)
	zero := 0
	b := f.watch.board
	b.mu.Lock()
	b.cfg = (&config.WatchConfig{DailyHour: &zero}).Effective()
	b.d.LastDaily = "2000-01-01"
	b.mu.Unlock()
	before := len(hp.Sent())
	f.WatchTick()
	eventually(t, "daily digest", func() bool { return len(hp.Sent()) == before+1 })
	if d := hp.Sent()[before]; !strings.Contains(d, "│ - 2 shell(s) started in the last 24 h: ") || !strings.Contains(d, "watcher (by alpha-lead, alpha): go run ./cmd/watch") {
		t.Fatalf("daily digest: %q", d)
	}
	f.WatchTick()
	if n := len(watchMail(t, f, head.ID)); n != 1 {
		t.Fatalf("the shells were listed %d times", n)
	}

	// Its launcher stops; the shell runs on: an orphan, flagged, in the head's digest.
	if _, err := f.StopAgent(OwnerCaller, lead.ID); err != nil {
		t.Fatal(err)
	}
	got = entry(registry(t, f, hc), id)
	if got == nil || !got.Orphan || got.State != "orphan" || got.Why != "alpha-lead is not running" || strings.Join(got.Actions, ",") != "stop,restart" {
		t.Fatalf("orphan shell: %+v", got)
	}
	f.WatchTick()
	if row := watchRow(f, "orphan:"+lead.ID); row == nil || !strings.Contains(row.Text, "started by alpha-lead still run although alpha-lead is not running: go run ./cmd/watch (PID ") {
		t.Fatalf("orphan row: %+v", f.Watch().Rows)
	}

	// A new daemon remembers who launched it; removing the shell forgets the record.
	f2 := e.fleet()
	os.use(f2)
	if e := entry(registry(t, f2, OwnerCaller), id); e == nil || e.Agent != "alpha-lead" || e.Thread != "alpha" {
		t.Fatalf("after a restart: %+v", e)
	}
	if err := f.RemoveAgent(OwnerCaller, watch.ID); err != nil {
		t.Fatal(err)
	}
	if e := entry(registry(t, f, OwnerCaller), id); e != nil {
		t.Fatalf("a removed shell is listed: %+v", e)
	}
	f.procreg.mu.Lock()
	_, kept := f.procreg.shells[watch.ID]
	f.procreg.mu.Unlock()
	if kept {
		t.Fatal("the launch record of a removed shell was kept")
	}
	if shellCommand("  ") != "(an empty prompt)" {
		t.Fatal(shellCommand("  "))
	}
}

// End to end: an agent launches a shell, sees it in the registry and stops it there.
//
// On a base where only the owner can launch a shell (the refusal in prepareLaunchLocked) the
// test skips itself with that reason; it runs by itself as soon as that refusal is gone
// (go-refusals-f's branch, merged in build6). Nothing has to be switched on by hand. Any other
// refusal is a failure: it would mean agents still cannot use the shells' handler.
func TestAgentLaunchesAShellSeesItAndStopsIt(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	os := &fakeOS{}
	os.use(f)
	if _, err := f.CreateProject(OwnerCaller, ProjectRequest{Name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	head, lead := mustAgent(t, f, "head"), mustAgent(t, f, "alpha-lead")
	e.chat.Last(lead.ID).State(model.StateWorking)
	lcall := callerFor(t, f, e.chat, lead.ID)
	w, err := f.LaunchAgent(lcall, LaunchRequest{Name: "w1"})
	if err != nil {
		t.Fatal(err)
	}
	sh, err := f.LaunchAgent(lcall, LaunchRequest{Name: "srv", Level: model.LevelWorker, Harness: model.HarnessShell, Prompt: "python -m http.server 8123"})
	if KindOf(err) == KindForbidden && strings.Contains(err.Error(), "only the owner can launch a shell") {
		t.Skip("agents cannot launch shells on this base yet (the refusal in prepareLaunchLocked); runs once it is removed")
	}
	if err != nil {
		t.Fatalf("the lead could not launch a shell: %v", err)
	}
	id := "shell:" + sh.ID
	hc, lc, wc := AgentCaller(head.ID), AgentCaller(lead.ID), AgentCaller(w.ID)

	// It sees its shell, with everything the handler shows.
	got := entry(registry(t, f, lc), id)
	if got == nil || got.Kind != model.MachineShell || got.What != "shell srv" || got.Command != "python -m http.server 8123" ||
		got.Agent != "alpha-lead" || got.AgentID != lead.ID || got.Project != "alpha" || got.Thread != "alpha" ||
		got.PID <= 0 || got.Since == 0 || got.Orphan || !got.RestartsWithNode || strings.Join(got.Actions, ",") != "stop,restart" {
		t.Fatalf("the launcher's view: %+v", got)
	}
	// The head and the owner see it and may act; another agent does not see it.
	if e := entry(registry(t, f, hc), id); e == nil || strings.Join(e.Actions, ",") != "stop,restart" {
		t.Fatalf("the head's view: %+v", e)
	}
	if e := entry(registry(t, f, OwnerCaller), id); e == nil || strings.Join(e.Actions, ",") != "stop,restart" {
		t.Fatalf("the owner's view: %+v", e)
	}
	if e := entry(registry(t, f, wc), id); e != nil {
		t.Fatalf("another agent sees it: %+v", e)
	}
	if err := f.RegistryStop(wc, id); err == nil {
		t.Fatal("another agent stopped it")
	}
	// The new shell is in the daily digest's list.
	if lines := f.newShells(model.Now() - 3600_000); len(lines) != 1 || lines[0] != "srv (by alpha-lead, alpha): python -m http.server 8123" {
		t.Fatalf("new shells: %q", lines)
	}
	// It stops it from the registry: the process ends, the entry stays, stopped.
	proc := e.term.Last(sh.ID)
	if err := f.RegistryStop(lc, id); err != nil {
		t.Fatal(err)
	}
	got = entry(registry(t, f, lc), id)
	if got == nil || got.PID != 0 || got.State != "stopped" || got.RestartsWithNode || !proc.Stopped() || strings.Join(got.Actions, ",") != "restart" {
		t.Fatalf("after the stop: %+v", got)
	}
	if a := mustAgent(t, f, "srv"); a.Desired != "stopped" {
		t.Fatalf("the shell would start again with the node: %+v", a)
	}
}
