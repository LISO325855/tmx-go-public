package fleet

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/sysinfo"
)

// fakeOS is a process table the test edits, and a record of what was "killed".
type fakeOS struct {
	mu     sync.Mutex
	rows   []sysinfo.Proc
	killed []sysinfo.Proc
	err    error // what kill answers
}

func (o *fakeOS) procs() ([]sysinfo.Proc, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]sysinfo.Proc(nil), o.rows...), nil
}

func (o *fakeOS) kill(p sysinfo.Proc) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.err != nil {
		return o.err
	}
	o.killed = append(o.killed, p)
	kept := o.rows[:0]
	for _, r := range o.rows {
		if r.PID != p.PID {
			kept = append(kept, r)
		}
	}
	o.rows = kept
	return nil
}

func (o *fakeOS) set(rows ...sysinfo.Proc) { o.mu.Lock(); o.rows = rows; o.mu.Unlock() }

// root: every agent harness in these tests was "started by the daemon" with start time 100.
func (o *fakeOS) root(pid int) (sysinfo.Proc, bool) { return sysinfo.Proc{PID: pid, Start: 100}, true }

func (o *fakeOS) use(f *Fleet) {
	f.procreg.mu.Lock()
	f.procreg.procs, f.procreg.root, f.procreg.kill = o.procs, o.root, o.kill
	f.procreg.mu.Unlock()
}

// age makes every seen process old enough to be listed.
func age(f *Fleet) {
	f.procreg.mu.Lock()
	for _, p := range f.procreg.seen {
		p.FirstSeen = model.Now() - (2 * time.Minute).Milliseconds()
	}
	f.procreg.mu.Unlock()
}

func entry(list []model.MachineProcess, id string) *model.MachineProcess {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

func registry(t *testing.T, f *Fleet, c Caller) []model.MachineProcess {
	t.Helper()
	list, err := f.Registry(c, "")
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestRegistryListsWhatAgentsStartedAndFlagsOrphans(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	watchNoDaily(f)
	os := &fakeOS{}
	os.use(f)
	head := mustAgent(t, f, "head")
	w1, w2 := launch(t, f, "w1", ""), launch(t, f, "w2", "")
	w1 = mustAgent(t, f, "w1")
	if w1.PID <= 0 {
		t.Fatalf("the fake agent has no PID: %+v", w1)
	}
	os.set(
		sysinfo.Proc{PID: w1.PID, PPID: 1, Start: 100, Name: "claude.exe"},
		sysinfo.Proc{PID: 5001, PPID: w1.PID, Start: 200, Name: "node.exe"},
		sysinfo.Proc{PID: 5002, PPID: 5001, Start: 210, Name: "python.exe"},
		sysinfo.Proc{PID: 5003, PPID: 5001, Start: 211, Name: "conhost.exe"},    // console helper: noise
		sysinfo.Proc{PID: 5004, PPID: w1.PID, Start: 90, Name: "old.exe"},       // older than its "parent": a reused PID, not a child
		sysinfo.Proc{PID: 7001, PPID: 1, Start: 50, Name: "explorer.exe"},       // not an agent's
		sysinfo.Proc{PID: 5005, PPID: w1.PID, Start: 220, Name: "tmx-host-abc"}, // a session host
	)
	// Young processes are the many short commands an agent runs: not entries.
	if list := registry(t, f, OwnerCaller); len(list) != 0 {
		t.Fatalf("young processes listed: %+v", list)
	}
	age(f)
	list := registry(t, f, OwnerCaller)
	node, py := entry(list, "pid:5001:200"), entry(list, "pid:5002:210")
	if len(list) != 2 || node == nil || py == nil {
		t.Fatalf("entries: %+v", list)
	}
	if node.Kind != model.MachineProcessRun || node.Command != "node.exe" || node.Agent != "w1" || node.AgentID != w1.ID || node.ProjectID != w1.ProjectID ||
		node.Project == "" || node.PID != 5001 || node.State != "running" || node.Orphan || node.Since == 0 || strings.Join(node.Actions, ",") != "stop" {
		t.Fatalf("node entry: %+v", node)
	}
	// Who sees and may do what: the head sees all but may not stop a live agent's process;
	// the agent that started it may; another worker sees nothing of it.
	hc, c1, c2 := AgentCaller(head.ID), AgentCaller(w1.ID), AgentCaller(w2.ID)
	if got := entry(registry(t, f, hc), "pid:5001:200"); got == nil || len(got.Actions) != 0 {
		t.Fatalf("head on a live agent's process: %+v", got)
	}
	if got := entry(registry(t, f, c1), "pid:5001:200"); got == nil || strings.Join(got.Actions, ",") != "stop" {
		t.Fatalf("the agent on its own process: %+v", got)
	}
	if list := registry(t, f, c2); len(list) != 0 {
		t.Fatalf("another worker sees: %+v", list)
	}
	if err := f.RegistryStop(hc, "pid:5001:200"); KindOf(err) != KindForbidden || !strings.Contains(err.Error(), "belongs to w1, which still runs") {
		t.Fatalf("head stopped a live agent's process: %v", err)
	}
	if err := f.RegistryStop(c2, "pid:5001:200"); KindOf(err) != KindNotFound {
		t.Fatalf("another worker: %v", err)
	}
	if _, err := f.Registry(Caller{Companion: true}, ""); KindOf(err) != KindForbidden {
		t.Fatalf("Companion read the registry: %v", err)
	}
	if list, err := f.Registry(OwnerCaller, "no-such-project"); err != nil || len(list) != 0 {
		t.Fatalf("project filter: %+v %v", list, err)
	}
	if len(os.killed) != 0 {
		t.Fatalf("a refused stop killed: %+v", os.killed)
	}

	// The agent stops; what it started runs on: orphans, flagged, and in the head's digest.
	if _, err := f.StopAgent(OwnerCaller, w1.ID); err != nil {
		t.Fatal(err)
	}
	list = registry(t, f, hc)
	node = entry(list, "pid:5001:200")
	if node == nil || !node.Orphan || node.State != "orphan" || node.Why != "w1 is not running" || strings.Join(node.Actions, ",") != "stop" || !list[0].Orphan {
		t.Fatalf("orphan entry: %+v", list)
	}
	f.WatchTick()
	row := watchRow(f, "orphan:"+w1.ID)
	if row == nil || row.Urgent || !strings.HasPrefix(row.Text, "2 process(es) started by w1 still run although w1 is not running: ") ||
		!strings.Contains(row.Text, "node.exe (PID 5001)") || !strings.Contains(row.Text, "python.exe (PID 5002)") {
		t.Fatalf("orphan row: %+v", f.Watch().Rows)
	}

	// Stop is by PID AND start time. A PID that is another process now is refused.
	os.mu.Lock()
	os.err = sysinfo.ErrNotChild
	os.mu.Unlock()
	if err := f.RegistryStop(hc, "pid:5001:200"); KindOf(err) != KindConflict || !strings.Contains(err.Error(), "different process now") {
		t.Fatalf("reused PID: %v", err)
	}
	os.mu.Lock()
	os.err = sysinfo.ErrProtected
	os.mu.Unlock()
	if err := f.RegistryStop(hc, "pid:5001:200"); KindOf(err) != KindForbidden || !strings.Contains(err.Error(), "protected program") {
		t.Fatalf("protected: %v", err)
	}
	os.mu.Lock()
	os.err = errors.New("access denied")
	os.mu.Unlock()
	if err := f.RegistryStop(hc, "pid:5001:200"); KindOf(err) != KindConflict {
		t.Fatalf("failed kill: %v", err)
	}
	os.mu.Lock()
	os.err = nil
	os.mu.Unlock()
	if err := f.RegistryStop(hc, "pid:5001:200"); err != nil {
		t.Fatal(err)
	}
	if len(os.killed) != 1 || os.killed[0].PID != 5001 || os.killed[0].Start != 200 {
		t.Fatalf("killed: %+v (one process, by PID and start time; never its tree)", os.killed)
	}
	list = registry(t, f, hc)
	if entry(list, "pid:5001:200") != nil || entry(list, "pid:5002:210") == nil {
		t.Fatalf("after the stop: %+v", list)
	}
	for _, bad := range []string{"pid:5002", "pid:x:1", "5002", "pid:5002:999", "tool:nope", "shell:" + head.ID, ""} {
		if err := f.RegistryStop(OwnerCaller, bad); err == nil {
			t.Fatalf("stop %q was accepted", bad)
		}
	}
	if _, err := f.RegistryRestart(OwnerCaller, "pid:5002:210"); KindOf(err) != KindForbidden || !strings.Contains(err.Error(), "does not know the command") {
		t.Fatalf("restart of a seen process: %v", err)
	}
	// The agent is removed: still an orphan, still named.
	if err := f.RemoveAgent(OwnerCaller, w1.ID); err != nil {
		t.Fatal(err)
	}
	if got := entry(registry(t, f, OwnerCaller), "pid:5002:210"); got == nil || !got.Orphan || got.Agent != "w1" || got.Why != "w1 was removed" {
		t.Fatalf("after the agent was removed: %+v", got)
	}
	// The same PID with another start time is another process: the old entry is gone.
	os.set(sysinfo.Proc{PID: 5002, PPID: 1, Start: 999, Name: "notepad.exe"})
	if list := registry(t, f, OwnerCaller); len(list) != 0 {
		t.Fatalf("a reused PID kept the entry: %+v", list)
	}
	f.WatchTick()
	if row := watchRow(f, "orphan:"+w1.ID); row != nil {
		t.Fatalf("orphan row kept: %+v", row)
	}
}

func TestRegistryDetachedAndRememberedAcrossARestart(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	os := &fakeOS{}
	os.use(f)
	w := launch(t, f, "w1", "")
	w = mustAgent(t, f, "w1")
	os.set(
		sysinfo.Proc{PID: w.PID, PPID: 1, Start: 100, Name: "claude.exe"},
		sysinfo.Proc{PID: 5001, PPID: w.PID, Start: 200, Name: "sh"},
		sysinfo.Proc{PID: 5002, PPID: 5001, Start: 210, Name: "server"},
	)
	registry(t, f, OwnerCaller)
	age(f)
	// The intermediate shell ends; the server runs on, its agent too: detached, not an orphan.
	os.set(
		sysinfo.Proc{PID: w.PID, PPID: 1, Start: 100, Name: "claude.exe"},
		sysinfo.Proc{PID: 5002, PPID: 5001, Start: 210, Name: "server"},
	)
	list := registry(t, f, OwnerCaller)
	if len(list) != 1 || list[0].State != "detached" || list[0].Orphan || !strings.Contains(list[0].Why, "w1 still runs") {
		t.Fatalf("detached: %+v", list)
	}
	f.WatchTick()
	if row := watchRow(f, "orphan:"+w.ID); row != nil {
		t.Fatalf("a detached process of a running agent is not an orphan: %+v", row)
	}
	// A new daemon on the same home remembers it (its agent is not running there yet).
	f2 := e.fleet()
	os.use(f2)
	list = registry(t, f2, OwnerCaller)
	if len(list) != 1 || list[0].ID != "pid:5002:210" || list[0].Agent != "w1" || !list[0].Orphan {
		t.Fatalf("after a restart: %+v", list)
	}
	// A harness PID that is another process now (another start time) has no children here.
	os.set(
		sysinfo.Proc{PID: w.PID, PPID: 1, Start: 777, Name: "other.exe"},
		sysinfo.Proc{PID: 5002, PPID: 5001, Start: 210, Name: "server"},
		sysinfo.Proc{PID: 6001, PPID: w.PID, Start: 800, Name: "stranger.exe"},
	)
	registry(t, f, OwnerCaller)
	f.procreg.mu.Lock()
	_, stranger := f.procreg.seen["pid:6001:800"]
	f.procreg.mu.Unlock()
	if stranger {
		t.Fatal("a reused harness PID gave the agent someone else's children")
	}
	// An unreadable process table changes nothing.
	f2.procreg.mu.Lock()
	f2.procreg.procs = func() ([]sysinfo.Proc, error) { return nil, sysinfo.ErrUnsupported }
	f2.procreg.mu.Unlock()
	if list := registry(t, f2, OwnerCaller); len(list) != 1 {
		t.Fatalf("an unreadable table dropped entries: %+v", list)
	}
}

func TestRegistryRefusals(t *testing.T) {
	for _, tc := range []struct {
		e    model.MachineProcess
		act  string
		want string
	}{
		{model.MachineProcess{Kind: model.MachineSystemTool}, "stop", "the daemon restarts it itself"},
		{model.MachineProcess{Kind: model.MachineShell}, "stop", "the owner's own terminal"},
		{model.MachineProcess{Kind: model.MachineProcessRun, Orphan: true}, "restart", "does not know the command"},
		{model.MachineProcess{Kind: model.MachineProcessRun, Command: "node", PID: 7, Agent: "w1"}, "stop", "node (PID 7) belongs to w1"},
		{model.MachineProcess{Kind: model.MachineTool}, "stop", "not yours to stop"},
	} {
		if got := registryRefusal(tc.e, tc.act); !strings.Contains(got, tc.want) {
			t.Fatalf("%+v %s: %q", tc.e, tc.act, got)
		}
	}
	if _, _, ok := parsePidID("pid:12:34"); !ok {
		t.Fatal("pid:12:34")
	}
	for _, bad := range []string{"pid:0:1", "pid:1:0", "pid:-1:5", "pid:1:2:3", "tool:1:2", "pid:1"} {
		if _, _, ok := parsePidID(bad); ok {
			t.Fatalf("%q parsed", bad)
		}
	}
}
