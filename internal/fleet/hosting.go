package fleet

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
	"github.com/blackcode-ch/tmx-go/internal/runner/host"
)

// Session hosts (runner/host): an agent's program runs in a host process of its own that
// outlives the daemon. Close leaves it running (runner.Detacher); the next daemon's start
// reattaches to it (runner.Reattacher) instead of starting the agent again, so a restart or an
// update never cuts a running turn.

// hostedLocked reports whether a runs in a session host, when the daemon gave an executable
// for hosts: terminal mode, and claude in chat mode (stream-json through the host's pipes;
// codex's app-server is not hosted yet).
func (f *Fleet) hostedLocked(a model.Agent) bool {
	if f.opts.HostExecutable == "" {
		return false
	}
	switch a.Mode {
	case model.ModeTerminal:
		return true
	case model.ModeChat, "":
		return a.Harness == model.HarnessClaude || a.Harness == ""
	}
	return false
}

// hostRetry is how long the fleet waits before checking an unknown session host again (each
// fleet takes it at New, so a test changing it never races a running fleet).
var hostRetry = 30 * time.Second

// Leave room around the terminal runner's 20s repaint wait for dialing,
// resize/redraw and returning its classified recovery error.
const hostHandoverTimeout = 30 * time.Second

// stopHost ends the agent in its session host by the identity the host recorded, never by
// name (runner/host Stop); a variable for tests.
var stopHost = host.Stop

// stopUnknownHost is the owner's stop of an agent whose session host did not answer: the
// program and its host are ended if they are still the recorded processes. Whatever happens is
// said in the timeline, so the owner is never left with an agent that cannot be stopped
// unawares.
func (f *Fleet) stopUnknownHost(id string) {
	gone, err := f.stopHost(f.hostDir(id))
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.agents[id]
	if r == nil {
		return
	}
	r.hostStopping = false
	if r.proc != nil || r.starting {
		return // something else runs it now: its state is that process's
	}
	var text string
	switch {
	case err != nil:
		r.hostUnknown = true // still unsure: a later stop tries again
		r.a.State, r.a.Error = model.StateError, "could not stop it: its session host could not be verified ("+err.Error()+")"
		text = r.a.Error
	case gone:
		r.a.State, r.a.Error = model.StateStopped, ""
		text = "stopped (its session host was already gone)"
	default:
		r.a.State, r.a.Error = model.StateStopped, ""
		text = "stopped: its session host was ended"
	}
	if err == nil {
		f.deleteCodexGuardAuth(id)
	}
	f.appendLocked(r, model.TimelineEntry{Kind: model.EntrySystem, Text: text, IsError: err != nil})
	f.saveAgentLocked(r)
}

// retryHost checks the agent's session host again after hostRetry, for as long as it is meant
// to run and the check stays unsure. One loop per agent (agentRec.hostRetrying).
func (f *Fleet) retryHost(id string) {
	f.mu.Lock()
	r := f.agents[id]
	if r == nil || r.hostRetrying {
		f.mu.Unlock()
		return
	}
	r.hostRetrying = true
	f.mu.Unlock()
	f.goTracked(func() {
		select {
		case <-f.ctx.Done():
			return
		case <-time.After(f.hostRetry):
		}
		f.mu.Lock()
		again := f.agents[id] == r && r.hostUnknown && r.proc == nil && !r.starting && r.a.Desired == "running"
		r.hostRetrying = false // the next unsure answer starts the next round
		f.mu.Unlock()
		if again {
			_ = f.startAgentOpts(id, "", startOpts{reattach: true})
		}
	})
}

// hostDir is the agent's session host folder (inside its private agent folder).
func (f *Fleet) hostDir(id string) string { return filepath.Join(f.opts.Paths.AgentDir(id), "host") }

// reattachLocked takes over the session host an earlier daemon left r running in. Called with
// f.mu held: if it reattached (true) f.mu is released; otherwise (false: no host, or it could
// not be reached) f.mu is still held and the agent is started as usual.
func (f *Fleet) reattachLocked(r *agentRec, ext extServers) (bool, error) {
	ext.missing = nil // the start that follows a failed reattach says what is missing, once
	ctx, cancel := context.WithTimeout(f.ctx, hostHandoverTimeout)
	defer cancel()
	return f.reattachModeLocked(ctx, r, ext, true)
}

// reattachModeLocked is reattachLocked; otherMode: when the host turns out to run the agent
// in its other mode (runner.ErrHostMode), switch the agent to that mode and try once more.
func (f *Fleet) reattachModeLocked(ctx context.Context, r *agentRec, ext extServers, otherMode bool) (bool, error) {
	ra, ok := f.opts.Runners[r.a.Mode].(runner.Reattacher)
	if !ok || !f.hostedLocked(r.a) {
		return false, nil
	}
	spec, err := f.specLocked(r, ext)
	if err != nil {
		return false, nil // the usual start reports it
	}
	id := r.a.ID
	r.starting = true
	f.mu.Unlock()
	proc, cred, err := reattachWithin(ctx, ra, spec, hostHandoverTimeout)
	f.mu.Lock()
	r.starting = false
	if errors.Is(err, runner.ErrHostMode) && otherMode && f.agents[id] == r && !f.closed && r.proc == nil {
		// A legacy or stale row may differ from its still-running host. Attach using the
		// host's actual mode so the next daemon uses the correct runner protocol.
		other := model.ModeTerminal
		if r.a.Mode == model.ModeTerminal {
			other = model.ModeChat
		}
		if _, _, herr := f.harnessModeLocked(r.a.Harness, other); herr == nil {
			f.applyModeLocked(r, other)
			f.appendLocked(r, model.TimelineEntry{Kind: model.EntrySystem,
				Text: "Found running " + modeWhere(r.a.Harness, other) + " (switched there before the daemon restarted)"})
			return f.reattachModeLocked(ctx, r, ext, false)
		}
	}
	// Once uncertain activity required attention, later silence cannot authorize a restart.
	safeSilentRestart := !r.repaintAttention && errors.Is(err, runner.ErrHostSilent) && (r.handoverState == model.StateIdle || r.handoverState == model.StateStarting)
	if safeSilentRestart && errors.Is(err, runner.ErrHostRepaint) && f.agents[id] == r && !f.closed && r.proc == nil && r.a.Desired == "running" && !r.repaintRestarted {
		r.repaintRestarted = true
		r.hostStopping = true
		f.appendLocked(r, model.TimelineEntry{Kind: model.EntrySystem, Text: "Terminal did not redraw after handover; restarting the agent once to resume this conversation."})
		f.log.Warn("legacy terminal repaint timed out; restarting agent once", "agent", id)
		f.mu.Unlock()
		_, stopErr := f.stopHost(f.hostDir(id))
		f.mu.Lock()
		r.hostStopping = false
		if f.agents[id] != r || f.closed || r.proc != nil || r.a.Desired != "running" {
			f.mu.Unlock()
			return true, nil
		}
		if stopErr == nil {
			r.hostUnknown = false
			return false, nil // normal launch below resumes the recorded conversation
		}
		f.appendLocked(r, model.TimelineEntry{Kind: model.EntrySystem, IsError: true, Text: "Automatic terminal recovery could not stop its verified host; retry or restart the agent."})
		f.log.Warn("legacy terminal recovery stop failed", "agent", id, "err", stopErr)
	}
	if (errors.Is(err, runner.ErrHostUnknown) || errors.Is(err, runner.ErrHostMode)) && f.agents[id] == r && !f.closed && r.proc == nil {
		// It may still run (or does, in a mode that could not be taken over): starting it again
		// would put two copies on one conversation.
		r.hostUnknown = true
		r.a.State, r.a.Error = model.StateError, "its session host could not be checked; checking again"
		if errors.Is(err, runner.ErrHostRepaint) {
			r.a.Error = "needs attention: terminal state could not be confirmed; agent preserved"
			if !r.repaintAttention {
				r.repaintAttention = true
				f.appendLocked(r, model.TimelineEntry{Kind: model.EntrySystem, Text: r.a.Name + " may need a restart", Meta: map[string]any{"terminal_recovery": "needs_attention"}})
			}
			f.log.Warn("terminal needs attention; preserving live agent", "agent", id, "handover_state", r.handoverState, "silent", errors.Is(err, runner.ErrHostSilent))
		}
		f.saveAgentLocked(r)
		f.log.Warn("session host state unknown; not starting a second copy", "agent", id, "err", err)
		detail := err.Error()
		if errors.Is(err, runner.ErrHostRepaint) {
			detail = r.a.Error
		}
		f.mu.Unlock()
		f.retryHost(id)
		return true, errf(KindConflict, "%s", detail)
	}
	r.hostUnknown = false // reattached, or proven gone: either way settled
	if err != nil || f.agents[id] != r || f.closed || r.proc != nil {
		if proc != nil {
			if d, ok := proc.(runner.Detacher); ok {
				d.Detach()
			}
		}
		if err != nil && !errors.Is(err, runner.ErrNoHost) {
			f.log.Warn("reattach to the session host failed; starting the agent again", "agent", id, "err", err)
		}
		return false, nil
	}
	if r.token != "" {
		delete(f.tokens, r.token)
	}
	r.token = cred // the program still calls tmx with what it was started with
	if cred != "" {
		f.tokens[cred] = id
	}
	r.proc, r.done = proc, make(chan struct{})
	r.procStarted = 0 // started by an earlier daemon: what it was given is not known here
	r.stopping = false
	r.a.Desired, r.a.State, r.a.Error = "running", model.StateStarting, ""
	r.updateActivity, r.updateIdleAt = time.Now(), time.Time{}
	r.updateTurnActivity = r.updateActivity
	r.a.PID = proc.PID()
	f.appendLocked(r, model.TimelineEntry{Kind: model.EntrySystem, Text: "still running: reattached after the daemon restarted"})
	f.saveAgentLocked(r)
	done := r.done
	f.goTracked(func() { f.consume(r, proc, done) })
	f.mu.Unlock()
	f.tryDeliver(id)
	return true, nil
}

// A runner's own timeout may exclude dial/resize work. Bound the entire handover
// so one host cannot hold the sequential startup or shutdown indefinitely.
// A late result is detached, never stopped or installed as a second process.
func reattachWithin(parent context.Context, ra runner.Reattacher, spec runner.Spec, limit time.Duration) (runner.Process, string, error) {
	ctx, cancel := context.WithTimeout(parent, limit)
	defer cancel()
	type result struct {
		p     runner.Process
		token string
		err   error
	}
	results := make(chan result)
	go func() {
		p, token, err := ra.Reattach(ctx, spec)
		select {
		case results <- result{p, token, err}:
		case <-ctx.Done():
			if d, ok := p.(runner.Detacher); ok {
				d.Detach()
				for range p.Events() {
				}
			}
		}
	}()
	select {
	case got := <-results:
		return got.p, got.token, got.err
	case <-ctx.Done():
		return nil, "", fmt.Errorf("%w: handover deadline or cancellation: %v", runner.ErrHostUnknown, ctx.Err())
	}
}
