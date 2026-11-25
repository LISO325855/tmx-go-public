//go:build !windows

package cli

import "errors"

func handoffDaemon(e *env, exe, _ string) error { return respawnDaemon(e, exe) }

func cmdDaemonAfterExit(_ *env, _ []string) error {
	return errors.New("daemon process handoff is only available on Windows")
}
