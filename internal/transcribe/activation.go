package transcribe

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/hiddenexec"
)

var pythonProbeWait = 5 * time.Second

// probePython verifies the selected executable before any settings are saved. Isolated
// mode and the worker's cleaned environment prevent Python import-path inheritance.
func probePython(ctx context.Context, spec fwSpec) error {
	ctx, cancel := context.WithTimeout(ctx, pythonProbeWait)
	defer cancel()
	cmd := hiddenexec.CommandContext(ctx, spec.Python, "-I", "-c", "import sys; print(sys.version_info[:2])")
	cmd.Env, cmd.Dir = spec.env(), spec.Dir
	cmd.WaitDelay = time.Second
	var output lockedTail
	output.t.max = 1024
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Python 3 probe failed: %w", err)
	}
	var major, minor int
	output.mu.Lock()
	text := strings.TrimSpace(string(output.t.b))
	output.mu.Unlock()
	if n, err := fmt.Sscanf(text, "(%d, %d)", &major, &minor); err != nil || n != 2 || major != 3 || minor < 0 || text != fmt.Sprintf("(%d, %d)", major, minor) {
		return fmt.Errorf("the interpreter did not report a Python 3 version")
	}
	return nil
}

// commitVoiceSettings is called with configureMu held. The existing worker stays
// available throughout probing; only the final save/adoption waits for active jobs.
func (s *Service) commitVoiceSettings(c config.VoiceConfig, p SettingsPatch) error {
	probe := p.Python != nil || p.ToolsDir != nil || p.ModelDir != nil ||
		(p.Engine != nil && c.Engine == config.EngineFasterWhisper)
	var candidate *fwWorker
	if probe {
		spec, err := s.fwSpecFor(c)
		if err != nil {
			return fmt.Errorf("%w: worker activation: %v", ErrInvalid, err)
		}
		if err := probePython(s.ctx, spec); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		if err := s.writeWorkerScript(); err != nil {
			return err
		}
		candidate = &fwWorker{log: s.log}
		defer candidate.close()
		ctx, cancel := context.WithTimeout(s.ctx, fwLoadWait)
		answer, err := candidate.do(ctx, spec, fwRequest{Warm: true}, fwLoadWait, nil)
		cancel()
		if err != nil {
			return fmt.Errorf("%w: worker activation: %v", ErrInvalid, err)
		}
		if !answer.OK {
			return fmt.Errorf("%w: worker activation: %s", ErrInvalid, answer.Error)
		}
	}
	s.fwUseMu.Lock()
	defer s.fwUseMu.Unlock()
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if candidate != nil {
		candidate.mu.Lock()
		defer candidate.mu.Unlock()
		if !candidate.aliveLocked() {
			return fmt.Errorf("%w: activated worker exited before commit", ErrInvalid)
		}
	}
	// Save implementations persist the entire voice object atomically. A save error
	// leaves the existing worker untouched and the temporary worker is closed above.
	if s.o.Save == nil {
		if candidate != nil {
			return fmt.Errorf("voice settings cannot be saved")
		}
		return nil // preserve the existing read-only service behavior for ordinary settings
	}
	if err := s.o.Save(c); err != nil {
		return err
	}
	if candidate != nil {
		s.fw.mu.Lock()
		defer s.fw.mu.Unlock()
		s.fw.stopLocked(false)
		if candidate.idle != nil {
			candidate.idle.Stop()
			candidate.idle = nil
		}
		w := s.fw
		w.spec, w.cmd, w.stdin = candidate.spec, candidate.cmd, candidate.stdin
		w.lines, w.quit, w.exited = candidate.lines, candidate.quit, candidate.exited
		w.stderr, w.last = candidate.stderr, candidate.last
		w.starts++
		w.idle = time.AfterFunc(w.spec.Idle+100*time.Millisecond, w.reap)
		// Ownership of these handles has transferred; deferred candidate.close is inert.
		candidate.cmd, candidate.stdin, candidate.lines = nil, nil, nil
	}
	return nil
}
