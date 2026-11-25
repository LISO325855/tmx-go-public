package term

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	termutil "github.com/charmbracelet/x/term"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
	"github.com/blackcode-ch/tmx-go/internal/runner/host"
	"github.com/blackcode-ch/tmx-go/internal/runner/host/hosttest"
)

// testHostMain is the test binary run as `tmx host [--launch] DIR`.
func testHostMain(args []string) int {
	var err error
	switch {
	case len(args) == 2 && args[0] == "--launch":
		exe, _ := os.Executable() // the tmx-host-* copy (hosttest.Executable)
		err = host.Launch(exe, args[1])
	case len(args) == 1:
		err = host.Run(args[0])
	default:
		err = errors.New("usage")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// fakeLine is the program in the host: "ready", then "got:<line>" for every line read.
func fakeLine() {
	if os.Getenv("TMXG_FAKE_RESIZE") == "1" {
		fakeResizeLine()
		return
	}
	fmt.Print("ready\r\n")
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "draw-86" {
			rule := strings.Repeat("\u2500", 86)
			fmt.Print("\x1b[2J\x1b[Hready\r\n" + rule + "\u276f \r\n" + rule + "  \u23f5\u23f5 bypass permissions on (shift+tab to cycle)")
		} else {
			fmt.Printf("got:%s\r\n", strings.TrimSpace(sc.Text()))
		}
	}
}

// waitOutput reads p's events until out contains want.
func waitOutput(t *testing.T, p runner.Process, out *strings.Builder, want string) {
	t.Helper()
	deadline := time.After(20 * time.Second)
	for !strings.Contains(out.String(), want) {
		select {
		case ev, ok := <-p.Events():
			if !ok {
				t.Fatalf("events closed before %q; output so far %q", want, out.String())
			}
			if ev.Kind == runner.EvOutput {
				out.Write(ev.Output)
			}
		case <-deadline:
			t.Fatalf("no %q within 20 s; output so far %q", want, out.String())
		}
	}
}

// A daemon restart: the first daemon detaches from the session host, the program keeps
// running, and the next daemon reattaches to the very same program: its screen (the replay)
// and input work, and it gets the agent's credential back. Linux and Windows (ConPTY).
func TestSessionHostOutlivesTheDaemon(t *testing.T) {
	t.Setenv("TMXG_FAKE_LINE", "1")
	dir := filepath.Join(t.TempDir(), "host")
	spec := runner.Spec{Harness: model.HarnessShell, Command: os.Args[0], Cwd: t.TempDir(),
		HostDir: dir, HostExe: hosttest.Executable(t), HostToken: "agent-cred-1"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	p1, err := New().Start(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	var out1 strings.Builder
	waitOutput(t, p1, &out1, "ready")
	pid := p1.PID()
	if pid <= 0 {
		t.Fatalf("pid %d", pid)
	}

	// The first daemon goes away.
	if !p1.(runner.Detacher).Detach() {
		t.Fatal("not detachable")
	}
	for ev := range p1.Events() {
		if ev.Kind == runner.EvExit {
			t.Fatalf("a detach reported an exit: %+v", ev)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "host.json")); err != nil {
		t.Fatalf("the host went away with the daemon: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "spec.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the spec (it holds the environment) was left behind: %v", err)
	}

	// The next daemon.
	p2, cred, err := New().(runner.Reattacher).Reattach(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if cred != "agent-cred-1" {
		t.Fatalf("credential %q", cred)
	}
	if p2.PID() != pid {
		t.Fatalf("a different program: %d, was %d", p2.PID(), pid)
	}
	var out2 strings.Builder
	waitOutput(t, p2, &out2, "ready") // the replay
	if err := p2.Write([]byte("hello\r"), true); err != nil {
		t.Fatal(err)
	}
	waitOutput(t, p2, &out2, "got:hello")

	// A real stop still ends it, and the host with it.
	sctx, scancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer scancel()
	if err := p2.Stop(sctx); err != nil {
		t.Fatal(err)
	}
	exited := false
	for ev := range p2.Events() {
		exited = exited || ev.Kind == runner.EvExit
	}
	if !exited {
		t.Fatal("no exit after a stop")
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "host.json")); errors.Is(err, os.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the host did not end after its program")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, _, err := New().(runner.Reattacher).Reattach(ctx, spec); !errors.Is(err, host.ErrNoHost) {
		t.Fatalf("reattach after the end: %v", err)
	}
}

// A viewer resized the terminal before a daemon restart. Full-width rules wrap
// implicitly: replaying at the launch/default width merges the prompt into a
// rule, falsely detecting a draft and holding owner chat forever.
func TestReattachRetainsViewerSizeAndDeliversOwnerChat(t *testing.T) {
	testViewerReattach(t, false)
}

func testViewerReattach(t *testing.T, legacy bool) {
	t.Helper()
	t.Setenv("TMXG_FAKE_LINE", "1")
	spec := runner.Spec{Harness: model.HarnessShell, Command: os.Args[0], Cwd: t.TempDir(),
		HostDir: filepath.Join(t.TempDir(), "host"), HostExe: hosttest.Executable(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	p1, err := New().Start(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	current := p1
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = current.Stop(c)
	})
	var out strings.Builder
	waitOutput(t, p1, &out, "ready")
	if err := p1.Resize(86, 32); err != nil {
		t.Fatal(err)
	}
	if err := p1.Write([]byte("draw-86\r"), true); err != nil {
		t.Fatal(err)
	}
	waitOutput(t, p1, &out, "shift+tab to cycle")
	if !p1.(runner.Detacher).Detach() {
		t.Fatal("detach failed")
	}
	for range p1.Events() {
	}
	if legacy {
		host.ClearState(spec.HostDir)
	}
	// Reattach as a Claude watcher; the tiny hosted fixture remains our fake.
	spec.Harness = model.HarnessClaude
	p2, _, err := New().(runner.Reattacher).Reattach(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	current = p2
	if legacy && os.Getenv("TMXG_FAKE_DRAFT") == "1" {
		pp := p2.(*process)
		if !strings.Contains(pp.w.scr.Text(), "unsent human draft") || pp.w.boxEmpty() {
			t.Fatal("draft lost after reattach")
		}
		return // successful reattach cannot request Fleet's restart fallback
	}
	if legacy && strings.Contains(string(p2.Replay()), "ready") {
		t.Fatal("retained legacy replay reached new process")
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-p2.Events():
			if ev.Kind == runner.EvState && ev.State == model.StateIdle {
				goto idle
			}
		case <-deadline:
			t.Fatal("viewer-sized replay never became idle after reattach")
		}
	}
idle:
	if err := p2.(runner.Interjector).Interject("owner-chat", []string{"owner-id"}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	waitOutput(t, p2, &out, "got:owner-chat")
}

// fakeResizeLine redraws from the actual PTY geometry, on both Unix and ConPTY.
// It does not accept a repaint keystroke: recovery must use only Resize.
func fakeResizeLine() {
	var mu sync.Mutex
	active := false
	width, _, _ := termutil.GetSize(os.Stdout.Fd())
	draw := func(cols int) {
		rule := strings.Repeat("\u2500", cols)
		draft := ""
		if os.Getenv("TMXG_FAKE_DRAFT") == "1" {
			draft = "unsent human draft"
		}
		fmt.Print("\x1b[2J\x1b[Hfresh-repaint\r\n" + rule + "\u276f " + draft + "\r\n" + rule + "  ? for shortcuts (shift+tab to cycle)")
	}
	fmt.Print("ready\r\n")
	go func() {
		for range time.NewTicker(10 * time.Millisecond).C {
			cols, _, err := termutil.GetSize(os.Stdout.Fd())
			if err != nil {
				continue
			}
			mu.Lock()
			if cols != width {
				width = cols
				if active {
					draw(cols)
				}
			}
			mu.Unlock()
		}
	}()
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		mu.Lock()
		if strings.TrimSpace(sc.Text()) == "draw-86" {
			active = true
			width, _, _ = termutil.GetSize(os.Stdout.Fd())
			draw(width)
		} else {
			fmt.Printf("got:%s\r\n", strings.TrimSpace(sc.Text()))
		}
		mu.Unlock()
	}
}

func TestLegacyReattachDiscardsReplayAndWaitsForRepaint(t *testing.T) {
	t.Setenv("TMXG_FAKE_RESIZE", "1")
	testViewerReattach(t, true)
}

func TestRepaintWaitIsBoundedWithoutFreshOutput(t *testing.T) {
	p, _ := newGuardedTestProcess(t)
	p.done = make(chan struct{})
	defer close(p.done)
	// Even an apparently idle state is not sufficient without fresh output.
	started := time.Now()
	if err := p.awaitRepaint(context.Background(), 30*time.Millisecond); err == nil {
		t.Fatal("accepted missing repaint")
	}
	if time.Since(started) > time.Second {
		t.Fatal("repaint wait not bounded")
	}
}

func TestLegacySilentHostReturnsRecoveryErrorAndStaysAlive(t *testing.T) {
	t.Setenv("TMXG_FAKE_LINE", "1")
	spec := runner.Spec{Harness: model.HarnessShell, Command: os.Args[0], Cwd: t.TempDir(), HostDir: filepath.Join(t.TempDir(), "host"), HostExe: hosttest.Executable(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, err := New().Start(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Stop(spec.HostDir)
	var out strings.Builder
	waitOutput(t, p, &out, "ready")
	p.(runner.Detacher).Detach()
	for range p.Events() {
	}
	host.ClearState(spec.HostDir)
	spec.Harness = model.HarnessClaude
	started := time.Now()
	_, _, err = New().(runner.Reattacher).Reattach(ctx, spec)
	if !errors.Is(err, runner.ErrHostRepaint) || !errors.Is(err, runner.ErrHostUnknown) {
		t.Fatalf("wrong recovery error: %v", err)
	}
	if runtime.GOOS == "windows" && errors.Is(err, runner.ErrHostSilent) {
		t.Fatal("ConPTY resize output incorrectly treated as silent")
	}
	if elapsed := time.Since(started); elapsed < 20*time.Second || elapsed > 25*time.Second {
		t.Fatalf("unbounded wait: %v", elapsed)
	}
	hc, err := host.Dial(spec.HostDir, host.End)
	if err != nil {
		t.Fatalf("host was stopped: %v", err)
	}
	hc.Close()
	var size terminalSize
	if host.LoadState(spec.HostDir, &size) {
		t.Fatal("failed repaint saved a trusted size")
	}
}

// Repaint is evidence of a live screen, not permission to send chat. A draft
// intentionally keeps the watcher Starting, and must never trigger a restart.
func TestRepaintAcceptsDraftWorkingAndMenuBeforeStateTransition(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		state      model.AgentState
	}{
		{"draft", claudeBox("the owner's unsent draft"), model.StateStarting},
		{"mid-turn", "Working (esc to interrupt)\r\n" + claudeBox(""), model.StateWorking},
		{"menu", menuScreen, model.StateWaiting},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, bp := newGuardedTestProcess(t)
			p.state = model.StateStarting
			p.w.ready = false
			p.done = make(chan struct{})
			defer close(p.done)
			p.w.output([]byte(tc.text))
			before := p.w.scr.Text()
			if err := p.awaitRepaint(context.Background(), 50*time.Millisecond); err != nil {
				t.Fatalf("recognized fresh screen rejected: %v", err)
			}
			if p.state != model.StateStarting {
				t.Fatal("repaint check changed lifecycle state")
			}
			if p.w.scr.Text() != before {
				t.Fatal("repaint changed screen or draft")
			}
			for i := 0; i < menuSettle; i++ {
				p.w.evaluate()
			}
			if p.state != tc.state {
				t.Fatalf("normal classification: got %s want %s", p.state, tc.state)
			}
			if tc.name == "draft" && p.w.boxEmpty() {
				t.Fatal("draft lost")
			}
			select {
			case b := <-bp.writes:
				t.Fatalf("recovery typed into screen: %q", b)
			default:
			}
		})
	}
}

func TestRepaintSilenceRequiresNoOutputForEntireWait(t *testing.T) {
	for _, output := range []string{"", "unrecognized but active terminal"} {
		t.Run(output, func(t *testing.T) {
			p, _ := newGuardedTestProcess(t)
			p.done = make(chan struct{})
			defer close(p.done)
			if output != "" {
				p.w.output([]byte(output))
			}
			err := p.awaitRepaint(context.Background(), 30*time.Millisecond)
			if err == nil {
				t.Fatal("unrecognized screen accepted")
			}
			if errors.Is(err, runner.ErrHostSilent) != (output == "") {
				t.Fatalf("wrong silence evidence: %v", err)
			}
		})
	}
}

func TestLegacyReattachPreservesDraftWithoutRestartFallback(t *testing.T) {
	t.Setenv("TMXG_FAKE_RESIZE", "1")
	t.Setenv("TMXG_FAKE_DRAFT", "1")
	testViewerReattach(t, true)
}
