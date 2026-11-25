// Package term runs an agent in a pseudo-terminal (terminal mode): the user's shell, or a
// harness's interactive TUI (Claude Code, Codex, opencode, pi). Output is streamed raw and
// kept in a replay ring buffer. For a harness, a virtual screen (package screen) is fed the
// same output and read for the agent's state and its menus, which become approvals.
package term

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"sync"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/codexguard"
	"github.com/blackcode-ch/tmx-go/internal/harness"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
	"github.com/blackcode-ch/tmx-go/internal/runner/claude"
	"github.com/blackcode-ch/tmx-go/internal/runner/host"
	"github.com/blackcode-ch/tmx-go/internal/sysinfo"
)

const (
	// EventBuffer is the capacity of Process.Events. On overflow, screen output is
	// dropped (Replay retains it); control events wait outside the process lock.
	EventBuffer = 1024
	// ReplaySize is how much recent output Replay keeps.
	ReplaySize = 1 << 20

	defaultCols, defaultRows = 120, 32
	quietAfter               = 2 * time.Second // a shell without output this long = idle
	exitGrace                = 2 * time.Second // after "exit" / SIGTERM, before escalating
	killGrace                = 3 * time.Second
	killedGrace              = 10 * time.Second // how long Stop still waits for a killed process to end
)

type termRunner struct{}

// New returns the terminal-mode runner.
func New() runner.Runner { return termRunner{} }

// launch is what Start runs.
type launch struct {
	path string
	args []string
	env  []string
	info *harness.Info // nil for a shell
	cmd  harness.Command
}

// command picks the program for spec: a harness's TUI, or a shell.
func command(spec runner.Spec) (launch, error) {
	if spec.Harness == model.HarnessShell || spec.Harness == "" {
		return launch{path: shellPath(spec), args: spec.ExtraArgs}, nil
	}
	info, ok := harness.Get(spec.Harness)
	if !ok {
		return launch{}, fmt.Errorf("terminal mode does not know harness %q", spec.Harness)
	}
	c, err := harness.TerminalCommand(spec)
	if err != nil {
		return launch{}, err
	}
	return launch{path: c.Path, args: c.Args, env: c.Env, info: &info, cmd: c}, nil
}

// envFor is the process's final environment. The owner's own shell (HarnessShell: only the
// owner may launch one, fleet.LaunchAgent) is the owner, not an agent, so it keeps their
// usual gh/ssh/account environment unchanged; every other harness gets the owner-credential
// denylist (agentpolicy.FilterEnv, via claude.BaseEnv/MergeEnv).
func envFor(spec runner.Spec, l launch) []string {
	env := append(append(append([]string{}, spec.Env...), l.env...), "TERM=xterm-256color")
	if spec.Harness == model.HarnessShell {
		return claude.MergeEnvUnfiltered(claude.BaseEnvUnfiltered(), env...)
	}
	return claude.MergeEnv(claude.BaseEnv(), env...)
}

func shellPath(spec runner.Spec) string {
	if spec.Command != "" {
		return spec.Command
	}
	if runtime.GOOS == "windows" {
		if c := os.Getenv("COMSPEC"); c != "" {
			return c
		}
		return "powershell.exe"
	}
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

// Start opens the pty and launches the program. ctx only bounds the start itself.
func (termRunner) Start(ctx context.Context, spec runner.Spec) (runner.Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := runner.RefuseElevated(spec); err != nil {
		return nil, err
	}
	if spec.Harness == model.HarnessCodex {
		if err := harness.ValidateCodexLaunch(spec); err != nil {
			return nil, err
		}
	}
	var guardLease *codexguard.Lease
	var guardErr error
	if spec.Harness == model.HarnessCodex {
		spec, guardLease, guardErr = harness.PrepareCodexGuard(spec)
		if guardErr != nil {
			return nil, guardErr
		}
	}
	guardStarted := false
	defer func() {
		if guardLease != nil && !guardStarted {
			guardLease.Close()
		}
	}()
	l, err := command(spec)
	if err != nil {
		return nil, err
	}
	if guardLease != nil {
		if err := codexguard.ProbeEffective(ctx, spec.Command, harness.CodexAppServerArgs(spec), envFor(spec, l), spec.Cwd, spec.CodexGuardExecutable, harness.CodexManagedServers(spec)); err != nil {
			return nil, err
		}
	}
	// One claude start at a time per CLAUDE_CONFIG_DIR (claude.BeginStart also checks it).
	var ticket *claude.StartTicket
	if l.info != nil && l.info.Name == model.HarnessClaude {
		if ticket, err = claude.BeginStart(ctx, envFor(spec, l), claude.HolderAgent, spec.OnStartWait); err != nil {
			return nil, err
		}
	}
	started := false
	defer func() {
		switch {
		case ticket == nil:
		case started:
			ticket.Settled()
		default:
			ticket.Release()
		}
	}()
	cols, rows := spec.Cols, spec.Rows
	if cols <= 0 || rows <= 0 {
		cols, rows = defaultCols, defaultRows
	}
	argv0, args := l.path, l.args
	if spec.LowPriority {
		argv0, args = sysinfo.LowPriorityCommand(argv0, args) // macOS: low disk I/O policy
	}
	var c conduit
	if spec.HostDir != "" && spec.HostExe != "" {
		// A session host owns the terminal: the agent outlives this daemon (runner/host).
		hc, err := host.Spawn(ctx, spec.HostExe, spec.HostDir, host.Spec{Path: argv0, Args: args, Env: envFor(spec, l),
			Cwd: spec.Cwd, Cols: cols, Rows: rows, Token: spec.HostToken})
		if err != nil {
			return nil, err
		}
		sysinfo.RegisterHosted(hc.PID(), hc.HostPID(), hc.HostStart(), l.info == nil)
		c = hc
	} else {
		lc, err := startLocal(argv0, args, spec.Cwd, envFor(spec, l), cols, rows)
		if err != nil {
			return nil, fmt.Errorf("start %s: %w", l.path, err)
		}
		sysinfo.RegisterChild(lc.PID(), l.info == nil)
		c = lc
	}
	started = true
	guardStarted = true
	if spec.LowPriority {
		_ = sysinfo.LowerPriority(c.PID()) // best effort; children inherit it
	}
	return newProcess(c, l, spec, guardLease, cols, rows), nil
}

// Reattach connects to the session host an earlier daemon left spec's agent running in
// (spec.HostDir): the same program, its screen rebuilt from the host's replay. It also returns
// the credential the program was started with (it still uses it). ErrNoHost when there is no
// host to reattach to.
func (termRunner) Reattach(ctx context.Context, spec runner.Spec) (runner.Process, string, error) {
	if spec.HostDir == "" {
		return nil, "", host.ErrNoHost
	}
	l, err := command(spec)
	if err != nil {
		return nil, "", err
	}
	var size terminalSize
	savedSize := host.LoadState(spec.HostDir, &size) && size.Cols > 0 && size.Rows > 0
	from := uint64(0)
	if !savedSize && l.info != nil {
		from = host.End
	}
	hc, err := host.Dial(spec.HostDir, from)
	if err != nil {
		return nil, "", err
	}
	initialOffset := hc.Start()
	if hc.Piped() {
		// The host runs a chat process (pipes), not a terminal: see runner.ErrHostMode.
		_ = hc.Close()
		return nil, "", runner.ErrHostMode
	}
	sysinfo.RegisterHosted(hc.PID(), hc.HostPID(), hc.HostStart(), l.info == nil)
	cols, rows := spec.Cols, spec.Rows
	if cols <= 0 || rows <= 0 {
		cols, rows = defaultCols, defaultRows
	}
	if savedSize {
		cols, rows = size.Cols, size.Rows
	} else if l.info != nil {
		// Legacy hosts retain a PTY at an unknown viewer size. Never feed their
		// old-width replay to state detection. Jiggle first, then reconnect at
		// End to discard the intermediate-width repaint as well.
		go func(c *host.Conn) { _, _ = io.Copy(io.Discard, c) }(hc)
		jiggle := cols - 1
		if jiggle < 1 {
			jiggle = cols + 1
		}
		if err := hc.Resize(jiggle, rows); err != nil {
			_ = hc.Close()
			return nil, "", err
		}
		select {
		case <-ctx.Done():
			_ = hc.Close()
			return nil, "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		_ = hc.Close()
		hc, err = host.Dial(spec.HostDir, host.End)
		if err != nil {
			return nil, "", err
		}
		if err := hc.Resize(cols, rows); err != nil {
			_ = hc.Close()
			return nil, "", err
		}
	}
	p := newProcess(hc, l, spec, nil, cols, rows)
	if !savedSize && p.w != nil {
		if err := p.awaitRepaint(ctx, 20*time.Second); err != nil {
			p.Detach()
			go func() {
				for range p.Events() {
				}
			}()
			host.ClearState(spec.HostDir) // next attempt must also discard legacy output
			if ctx.Err() != nil {
				return nil, "", fmt.Errorf("%w: %v", host.ErrHostUnknown, ctx.Err())
			}
			repaintErr := fmt.Errorf("%w: %w: %v", host.ErrHostUnknown, runner.ErrHostRepaint, err)
			if errors.Is(err, runner.ErrHostSilent) && hc.OutputOffset() == initialOffset {
				repaintErr = errors.Join(repaintErr, runner.ErrHostSilent)
			}
			return nil, "", repaintErr
		}
	}
	return p, hc.Token(), nil
}

// awaitRepaint bounds recovery without consuming events intended for Fleet.
// A silent legacy host stays live but is reported as unverified, never started
// a second time and never declared ready from the discarded screen.
func (p *process) awaitRepaint(ctx context.Context, limit time.Duration) error {
	deadline := time.NewTimer(limit)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		p.mu.Lock()
		// A recognized prompt with a draft deliberately remains Starting.
		// Repaint evidence must not depend on readiness to submit mail.
		observed := !p.w.lastOutput.IsZero()
		p.mu.Unlock()
		// ConPTY can emit cursor/old text on resize even if the TUI never
		// redraws. Require a recognized harness screen, not generic activity.
		text := p.w.scr.Text()
		patterns := p.w.info.Patterns
		recognized := p.w.classify().State.Menu()
		for _, rx := range []*regexp.Regexp{patterns.AtPrompt, patterns.Working, patterns.Idle, patterns.UsageLimit} {
			recognized = recognized || (rx != nil && rx.MatchString(text))
		}
		if observed && recognized {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.done:
			return errors.New("host disconnected before repaint")
		case <-deadline.C:
			p.mu.Lock()
			silent := p.w.lastOutput.IsZero()
			p.mu.Unlock()
			if silent {
				return runner.ErrHostSilent
			}
			return errors.New("no usable screen after resize")
		case <-tick.C:
		}
	}
}

// A hosted PTY retains the viewer's geometry across daemon restarts. Its
// emulator must use that geometry before consuming replay (including autowrap).
type terminalSize struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

func newProcess(c conduit, l launch, spec runner.Spec, guardLease *codexguard.Lease, cols, rows int) *process {
	p := &process{
		agentID:    spec.AgentID,
		guardLease: guardLease,
		c:          c,
		shell:      l.info == nil,
		events:     make(chan runner.Event, EventBuffer),
		ring:       newRing(ReplaySize),
		done:       make(chan struct{}),
	}
	if hc, ok := c.(*host.Conn); ok {
		_ = hc.SaveState(terminalSize{Cols: cols, Rows: rows})
	}
	p.setState(model.StateStarting)
	if l.info == nil {
		p.quiet = time.AfterFunc(quietAfter, func() { p.setState(model.StateIdle) })
	} else {
		p.w = newWatcher(p, *l.info, spec, l.cmd, cols, rows)
	}
	readDone := make(chan struct{})
	go p.read(readDone)
	go p.wait(readDone)
	if p.w != nil {
		go p.w.run()
		go p.w.watchSession()
	}
	return p
}

// Detach leaves a hosted program running and ends this process's view of it (a daemon
// restart): its events close without EvExit. A program in the daemon's own pty cannot be
// detached (false).
func (p *process) Detach() bool {
	if _, ok := p.c.(*host.Conn); !ok {
		return false
	}
	p.mu.Lock()
	p.detached = true
	p.mu.Unlock()
	_ = p.c.Close()
	return true
}

type process struct {
	deliveryStages sync.Map // message ID -> mailProgress, readable even during blocked shell writes
	agentID        string
	guardLease     *codexguard.Lease
	c              conduit
	shell          bool
	ring           *ring
	w              *watcher // nil for a shell

	resizeMu sync.Mutex // serialize PTY, emulator, and saved geometry changes
	wmu      sync.Mutex // serialises writes to the pty

	mu            sync.Mutex // guards state, closed and sending on events; never held across a pty write
	state         model.AgentState
	closed        bool
	detached      bool // Detach: the program runs on in its host
	events        chan runner.Event
	pendingEvents []runner.Event // control events waiting for the consumer
	eventPump     bool           // one ordered sender; also owns channel close while active
	quiet         *time.Timer    // shells only
	turnAt        time.Time      // when the current turn began (working after idle)

	done chan struct{}
}

func (p *process) Events() <-chan runner.Event { return p.events }
func (p *process) PID() int                    { return p.c.PID() }
func (p *process) Replay() []byte              { return p.ring.Bytes() }

// Write sends a viewer's input (owner or, absent a pending approval, a head/lead typing into
// its worker). Answers to terminal queries are dropped: the runner's own screen answers them,
// and a second answer would arrive as typed garbage.
//
// Lock order: fleet.mu is released by the caller (fleet.procForWriteAct) before Write is ever
// called, so Write only ever has to take p.mu itself here - never the reverse (Write must not
// be called with fleet.mu held). A non-owner's check happens under p.mu, the same lock the
// approval detector (watcher.evaluate -> menuLocked, runner/term/watch.go) holds while it flips
// w.pending/p.state to StateWaiting: either the detector's section runs first and the write
// sees pending and is refused, or the check runs first and the detector's transition waits
// until the key was written (watch.go's guardedWrite).
//
// Nothing holds p.mu across a pty write (Write, the detector's own typeText/press/pick, the
// screen's query answers): a pty write blocks while the program is not reading its input, and
// the output reader needs p.mu to drain what the program prints meanwhile.
//
// A shell has no watcher and nothing between its input and the commands it runs: only the
// owner may write into it (runner.ErrOwnerOnly otherwise).
func (p *process) Write(b []byte, owner bool) error {
	if p.w != nil {
		if b = stripQueryReplies(b); len(b) == 0 {
			return nil
		}
		return p.w.guardedWrite(b, owner)
	}
	if !owner {
		return runner.ErrOwnerOnly
	}
	return p.write(b)
}

func (p *process) write(b []byte) error {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	return p.writeLocked(b)
}

// writeLocked requires wmu.
func (p *process) writeLocked(b []byte) error {
	n, err := p.c.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	return err
}

// Send types text and presses Enter through the ordinary terminal input queue.
func (p *process) Send(text string) error {
	if p.w != nil {
		return p.w.send(text)
	}
	return p.write([]byte(text + "\r"))
}

func (p *process) SendLaunchPrompt(text string) error {
	if p.w != nil && p.w.info.Name == model.HarnessClaude {
		return p.w.sendMail(mailInput{text: text, launchPrompt: true})
	}
	return p.Send(text)
}

func (p *process) SendMail(text string, ids []string) error {
	if p.w != nil {
		return p.w.sendMail(mailInput{text: text, ids: append([]string(nil), ids...)})
	}
	p.recordMailStage(p.agentID, ids, "input_lock_wait")
	p.wmu.Lock()
	p.recordMailStage(p.agentID, ids, "shell_write")
	err := p.writeLocked([]byte(text + "\r"))
	p.wmu.Unlock()
	stage := "submitted"
	if err != nil {
		stage = "unconfirmed"
	}
	p.recordMailStage(p.agentID, ids, stage)
	p.mu.Lock()
	p.emitLocked(runner.Event{Kind: runner.EvMailDelivery, MessageIDs: ids, Delivery: stage})
	p.mu.Unlock()
	// A partial terminal write is uncertain; it must never trigger a blind replay.
	return nil
}

// Interject types the owner's words into the screen although the harness is working (watch.go
// sendMail with now): it queues what is typed for its next step. A shell has no turns: as Send.
func (p *process) Interject(text string, ids []string) error {
	if p.w != nil {
		return p.w.sendMail(mailInput{text: text, ids: append([]string(nil), ids...), now: true})
	}
	return p.SendMail(text, ids)
}

func (p *process) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return errors.New("terminal size must be positive")
	}
	p.resizeMu.Lock()
	defer p.resizeMu.Unlock()
	if p.w != nil {
		p.w.scr.Resize(cols, rows)
	}
	if err := p.c.Resize(cols, rows); err != nil {
		return err
	}
	if hc, ok := p.c.(*host.Conn); ok {
		return hc.SaveState(terminalSize{Cols: cols, Rows: rows})
	}
	return nil
}

func (p *process) Answer(id string, d runner.Decision) error {
	if p.w == nil {
		return errors.New("a shell has no menus to answer; answer in the terminal")
	}
	return p.w.answer(id, d)
}

// Interrupt: Esc for a harness ("esc to interrupt"), Ctrl-C for a shell.
func (p *process) Interrupt() error {
	if p.w != nil {
		p.w.inputMu.Lock()
		defer p.w.inputMu.Unlock()
		return p.write([]byte{0x1b})
	}
	return p.write([]byte{0x03})
}

// Stop asks politely ("exit" for a shell, SIGTERM for a harness); after exitGrace it signals
// the whole session, and once ctx has expired and killGrace passed, it kills it.
func (p *process) Stop(ctx context.Context) error {
	if p.shell {
		go func() { _ = p.write([]byte("exit\r")) }() // not waited on: a shell not reading its input is signalled below all the same
	} else {
		p.c.Terminate()
	}
	if p.waitDone(ctx, exitGrace) {
		return nil
	}
	p.c.Terminate()
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
	}
	if p.waitDone(context.Background(), killGrace) {
		return nil
	}
	p.c.Kill()
	// Never wait without an end: a process that survives the kill (or whose host does not
	// report its end) must not hang the stop, and with it the API call and the caller.
	select {
	case <-p.done:
		return nil
	case <-time.After(killedGrace):
		return errors.New("the process did not end after it was killed")
	}
}

// waitDone reports whether the process ended within d (or before ctx expired).
func (p *process) waitDone(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-p.done:
		return true
	case <-ctx.Done():
		select {
		case <-p.done:
			return true
		default:
			return false
		}
	case <-t.C:
		return false
	}
}

// emitLocked never waits for a consumer while holding mu. During reattach,
// Fleet cannot consume until screen recovery completes. Output is already in
// Replay; control events (receipts, approvals, state, exit) must remain ordered.
func (p *process) emitLocked(ev runner.Event) {
	if p.closed {
		return
	}
	if !p.eventPump {
		select {
		case p.events <- ev:
			return
		default:
		}
	}
	if ev.Kind == runner.EvOutput {
		return
	}
	p.pendingEvents = append(p.pendingEvents, ev)
	if !p.eventPump {
		p.eventPump = true
		go p.flushEvents()
	}
}

func (p *process) flushEvents() {
	for {
		p.mu.Lock()
		if len(p.pendingEvents) == 0 {
			p.eventPump = false
			if p.closed {
				close(p.events)
			}
			p.mu.Unlock()
			return
		}
		ev := p.pendingEvents[0]
		p.pendingEvents[0] = runner.Event{}
		p.pendingEvents = p.pendingEvents[1:]
		p.mu.Unlock()
		p.events <- ev // only this goroutine may close the channel while eventPump is true
	}
}

func (p *process) closeEventsLocked() {
	p.closed = true
	if !p.eventPump {
		close(p.events)
	}
}

func (p *process) setStateLocked(s model.AgentState) {
	if p.state != s {
		if s == model.StateWorking && p.state != model.StateWaiting {
			p.turnAt = time.Now()
		}
		p.state = s
		p.emitLocked(runner.Event{Kind: runner.EvState, State: s})
	}
}

func (p *process) setState(s model.AgentState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.setStateLocked(s)
}

func (p *process) read(done chan<- struct{}) {
	defer close(done)
	buf := make([]byte, 32<<10)
	for {
		n, err := p.c.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			p.ring.Write(chunk)
			if p.w != nil {
				p.w.output(chunk)
			}
			p.mu.Lock()
			if p.w == nil {
				p.setStateLocked(model.StateWorking)
			}
			p.emitLocked(runner.Event{Kind: runner.EvOutput, Output: chunk})
			p.mu.Unlock()
			if p.quiet != nil {
				p.quiet.Reset(quietAfter)
			}
		}
		if err != nil {
			return
		}
	}
}

// wait reaps the process, lets the reader drain, closes the pty and emits EvExit.
func (p *process) wait(readDone <-chan struct{}) {
	code, err := p.c.Wait()
	select {
	case <-readDone:
	case <-time.After(500 * time.Millisecond): // a background job may still hold the tty
	}
	p.c.Close()
	<-readDone
	if p.quiet != nil {
		p.quiet.Stop()
	}
	p.mu.Lock()
	detached := p.detached || errors.Is(err, host.ErrDetached)
	p.mu.Unlock()
	if p.guardLease != nil {
		if detached {
			p.guardLease.CloseShimPin()
		} else {
			p.guardLease.Close()
		}
	}
	if detached {
		// The program runs on in its host; this daemon only stops watching it.
		p.mu.Lock()
		p.closeEventsLocked()
		p.mu.Unlock()
		close(p.done)
		if p.w != nil {
			p.w.scr.Close()
		}
		return
	}

	ev := runner.Event{Kind: runner.EvExit, ExitCode: code}
	if ev.ExitCode != 0 {
		ev.Err = err
		if p.w != nil { // what the harness said last, like a chat runner's stderr tail
			if last := p.w.lastWords(); last != "" {
				ev.Err = fmt.Errorf("%v: %s", err, last)
			}
		}
	}
	p.mu.Lock()
	p.emitLocked(ev)
	p.closeEventsLocked()
	p.mu.Unlock()
	close(p.done)
	if p.w != nil {
		p.w.scr.Close()
	}
}
