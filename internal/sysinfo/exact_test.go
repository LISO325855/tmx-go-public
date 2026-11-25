package sysinfo

import (
	"errors"
	"os"
	"testing"
	"time"
)

func TestExactProtected(t *testing.T) {
	for name, want := range map[string]bool{
		"python.exe": false, "pythonw.exe": false, "/usr/bin/python": false, "node.exe": false, "server": false,
		"explorer.exe": true, "tailscaled": true, "sshd": true, "tmx.exe": true, "tmx-host-abc.exe": true, "": true,
		`C:\Windows\System32\lsass.exe`: true, "WindowsTerminal.exe": true,
	} {
		if got := exactProtected(name); got != want {
			t.Errorf("%q: protected = %v, want %v", name, got, want)
		}
	}
}

// Only this test binary's own disposable fixture is started and ended.
func TestKillExactIsOneProcessByPIDAndStartTime(t *testing.T) {
	if !pinnedControlAvailable {
		t.Skip("no pinned control backend")
	}
	cmd := startStopFixture(t, false)
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	rows, err := Procs()
	if err != nil {
		t.Fatal(err)
	}
	p, ok := find(rows, cmd.Process.Pid)
	if !ok || p.Start <= 0 {
		t.Fatalf("fixture not in the process table: %+v", p)
	}
	alive := func() bool {
		select {
		case <-done:
			return false
		case <-time.After(300 * time.Millisecond):
			return true
		}
	}
	// The right PID with another start time is another process: refused, nothing ends.
	if err := KillExact(Proc{PID: p.PID, Start: p.Start + 1}); !errors.Is(err, ErrNotChild) {
		t.Fatalf("wrong start time: %v", err)
	}
	if err := KillExact(Proc{PID: p.PID}); !errors.Is(err, ErrNotChild) {
		t.Fatalf("no start time: %v", err)
	}
	if !alive() {
		t.Fatal("a refused kill ended the process")
	}
	// The daemon itself (here: this test) and what it runs under are never ended.
	me, ok := find(rows, os.Getpid())
	if !ok {
		t.Fatal("this process is not in the table")
	}
	if err := KillExact(me); !errors.Is(err, ErrProtected) {
		t.Fatalf("self: %v", err)
	}
	if parent, ok := find(rows, me.PPID); ok && parent.Start > 0 {
		if err := KillExact(parent); !errors.Is(err, ErrProtected) {
			t.Fatalf("parent: %v", err)
		}
	}
	if err := KillExact(p); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the process survived KillExact")
	}
	// Gone already: not an error.
	if err := KillExact(p); err != nil {
		t.Fatalf("already gone: %v", err)
	}
}

func TestRegisteredRoot(t *testing.T) {
	if _, ok := RegisteredRoot(os.Getpid()); ok {
		t.Fatal("this process is not a child the daemon started")
	}
	if !pinnedControlAvailable {
		return
	}
	cmd := startStopFixture(t, false)
	RegisterChild(cmd.Process.Pid, false)
	root, ok := RegisteredRoot(cmd.Process.Pid)
	if !ok || root.PID != cmd.Process.Pid || root.Start <= 0 {
		t.Fatalf("registered root: %+v %v", root, ok)
	}
}
