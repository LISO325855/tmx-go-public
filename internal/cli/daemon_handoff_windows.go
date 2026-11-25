package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/hiddenexec"
	"golang.org/x/sys/windows"
)

// Give the helper a handle to this exact process. The executable paths can be
// renamed during an update, and a PID alone can be reused before the helper runs.
func handoffDaemon(e *env, exe, helper string) error {
	if helper == "" {
		return fmt.Errorf("daemon restart helper is unavailable")
	}
	parent, err := windows.OpenProcess(windows.SYNCHRONIZE, true, uint32(os.Getpid()))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(parent)
	out, err := os.OpenFile(filepath.Join(e.paths.LogsDir(), "daemon.stderr.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	cmd := hiddenexec.Command(helper, "daemon-after-exit", strconv.FormatUint(uint64(parent), 10), strconv.Itoa(os.Getpid()), exe)
	cmd.Stdout, cmd.Stderr = out, out
	cmd.Env = append(os.Environ(), config.EnvHome+"="+e.paths.Home)
	detach(cmd)
	cmd.SysProcAttr.AdditionalInheritedHandles = []syscall.Handle{syscall.Handle(parent)}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func cmdDaemonAfterExit(e *env, args []string) error {
	if len(args) != 3 || !filepath.IsAbs(args[2]) {
		return fmt.Errorf("invalid daemon handoff arguments")
	}
	value, err := strconv.ParseUint(args[0], 10, strconv.IntSize)
	if err != nil || value == 0 {
		return fmt.Errorf("invalid daemon handoff handle")
	}
	parent := windows.Handle(value)
	defer windows.CloseHandle(parent)
	const timeout = 60 * time.Second
	state, err := windows.WaitForSingleObject(parent, uint32(timeout/time.Millisecond))
	if err != nil || state != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("daemon PID %s did not exit within %s (wait state %d, error %v)", args[1], timeout, state, err)
	}
	if err := e.load(); err != nil {
		return err
	}
	return respawnDaemon(e, args[2])
}
