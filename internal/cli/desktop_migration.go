package cli

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/rollback"
)

// The predecessor can recover a candidate that never reached its API or its
// in-daemon watchdog. The candidate ordinarily confirms or rolls back itself.
func watchDesktopStart(cmd *exec.Cmd, dir string, p rollback.Probation) string {
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	timer := time.NewTimer(max(0, time.Until(p.Deadline.Add(2*probationPoll))))
	defer timer.Stop()
	for {
		select {
		case err := <-exited:
			if err != nil {
				return "desktop daemon exited before confirming health: " + err.Error()
			}
			return "desktop daemon exited before confirming health"
		case <-tick.C:
			current, ok, err := rollback.OnProbation(dir)
			if err == nil && (!ok || current.DesktopSHA256 != p.DesktopSHA256 || !current.Deadline.Equal(p.Deadline)) {
				return ""
			}
		case <-timer.C:
			// Only this exact child is stopped. The usual watchdog had time
			// to request a graceful rollback if it got as far as serving.
			current, ok, err := rollback.OnProbation(dir)
			if err == nil && (!ok || current.DesktopSHA256 != p.DesktopSHA256 || !current.Deadline.Equal(p.Deadline)) {
				return ""
			}
			if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				return "could not stop the desktop daemon after its probation deadline: " + err.Error()
			}
			<-exited
			return "desktop daemon did not confirm health by its probation deadline"
		}
	}
}

func restoreFailedDesktopStart(e *env, exe, dir string, p rollback.Probation, cause error) error {
	lock, err := config.WaitLock(e.paths, 15*time.Second)
	if err != nil {
		return errors.Join(cause, err)
	}
	current, ok, err := rollback.OnProbation(dir)
	if err != nil || !ok || current.DesktopSHA256 != p.DesktopSHA256 || !current.Deadline.Equal(p.Deadline) {
		lock.Release()
		return err // The successor already resolved or superseded the probation.
	}
	if err := rollback.MarkFailed(dir, cause.Error()); err != nil {
		lock.Release()
		return errors.Join(cause, err)
	}
	err = rollback.StartProbation(dir, installationPath(exe), e.paths.DB(), p.To, time.Now())
	lock.Release()
	if !errors.Is(err, rollback.ErrRolledBack) {
		return errors.Join(cause, err)
	}
	slog.Warn("update: desktop migration rolled back; starting the previous executable", "reason", cause.Error())
	if err := respawnDaemon(e, exe); err != nil {
		return fmt.Errorf("restart the restored executable: %w", err)
	}
	return nil
}
