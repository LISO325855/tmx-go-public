package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/client"
	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/desktop"
	"github.com/blackcode-ch/tmx-go/internal/hiddenexec"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/rollback"
)

const upTimeout = 20 * time.Second

func cmdUp(e *env, args []string) error {
	if _, err := parse(newFlags("up"), args); err != nil {
		return err
	}
	url, err := e.ensureUp()
	e.printJSON(map[string]any{"ready": err == nil, "url": url})
	if err != nil {
		return exitError{code: 1, msg: err.Error()}
	}
	return nil
}

// ensureUp starts the daemon detached unless one answers, and waits for /api/health.
// ownedHealthy reports whether a tmx daemon answers at url AND is the one for this state home
// (its owner token matches). A daemon from another TMXG_HOME is merely healthy().
func (e *env) ownedHealthy(url string) bool {
	if !healthy(url) {
		return false
	}
	if err := e.load(); err != nil {
		return false
	}
	tok, err := config.OwnerToken(e.paths)
	if err != nil {
		return false
	}
	c := client.New(url, tok)
	c.HTTP.Timeout = 2 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var x map[string]any
	return c.Get(ctx, "/api/snapshot", &x) == nil
}

func (e *env) ensureUp() (string, error) {
	if err := e.load(); err != nil {
		return "", err
	}
	url := e.baseURL()
	if e.ownedHealthy(url) {
		return url, nil
	}
	if healthy(url) {
		return url, fmt.Errorf("port %d is in use by a daemon from a different state home; stop it (or run tmx down with that TMXG_HOME) before starting this one", e.cfg.Port)
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	// The daemon logs to logs/daemon.log itself; this file catches what it prints before
	// its logger exists (and panics).
	out, err := os.OpenFile(filepath.Join(e.paths.LogsDir(), "daemon.stderr.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return "", err
	}
	defer out.Close()
	cmd := hiddenexec.Command(exe, "daemon")
	cmd.Stdout, cmd.Stderr = out, out
	cmd.Env = append(os.Environ(), config.EnvHome+"="+e.paths.Home)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start the daemon: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	url = fmt.Sprintf("http://127.0.0.1:%d", e.cfg.Port)
	deadline := time.Now().Add(upTimeout)
	for time.Now().Before(deadline) {
		if e.ownedHealthy(url) {
			return url, nil
		}
		select {
		case err := <-exited:
			if e.ownedHealthy(e.baseURL()) { // lost a race with another `tmx up`: theirs is fine
				return e.baseURL(), nil
			}
			return url, fmt.Errorf("the daemon exited at start (%v); see %s", err, e.paths.DaemonLog())
		case <-time.After(150 * time.Millisecond):
		}
	}
	return url, fmt.Errorf("the daemon did not answer within %s; see %s", upTimeout, e.paths.DaemonLog())
}

func (e *env) recentUpdateHandover(now time.Time) bool {
	p, ok, err := rollback.OnProbation(filepath.Join(e.paths.Home, "rollback"))
	if err != nil || !ok {
		return false
	}
	age := now.Sub(p.Deadline.Add(-rollback.ProbationWindow))
	return age >= 0 && age < 2*upTimeout
}

func (e *env) waitForOwnedDaemon() bool {
	deadline := time.Now().Add(upTimeout)
	for time.Now().Before(deadline) {
		if e.ownedHealthy(e.baseURL()) {
			return true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return e.ownedHealthy(e.baseURL())
}

func (e *env) restartDesktopDaemon() error {
	// The updater has already arranged to start the successor. Give it the first
	// chance to take the port, even when the old process has stopped answering.
	if e.recentUpdateHandover(time.Now()) && e.waitForOwnedDaemon() {
		return nil
	}
	_, err := e.ensureUp()
	if err != nil && e.recentUpdateHandover(time.Now()) {
		if e.waitForOwnedDaemon() {
			return nil
		}
		_, err = e.ensureUp()
	}
	if err != nil && e.ownedHealthy(e.baseURL()) {
		return nil
	}
	return err
}

func cmdDown(e *env, args []string) error {
	if _, err := parse(newFlags("down"), args); err != nil {
		return err
	}
	c, err := e.owner()
	if err != nil {
		return err
	}
	if !healthy(c.BaseURL) {
		e.printJSON(map[string]any{"stopped": true, "was_running": false})
		return nil
	}
	cx, cancel := ctx()
	defer cancel()
	if err := c.Post(cx, "/api/shutdown", nil, nil); err != nil {
		return err
	}
	deadline := time.Now().Add(45 * time.Second) // agents get a graceful stop first
	for time.Now().Before(deadline) {
		// It stops answering HTTP first and stops its agents after: done once the lock is free.
		if !healthy(c.BaseURL) {
			if l, err := config.AcquireLock(e.paths); err == nil {
				l.Release()
				e.printJSON(map[string]any{"stopped": true, "was_running": true})
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("the daemon has not finished stopping after 45s")
}

func cmdStatus(e *env, args []string) error {
	if _, err := parse(newFlags("status"), args); err != nil {
		return err
	}
	c, err := e.owner()
	if err != nil {
		return err
	}
	var snap model.Snapshot
	cx, cancel := ctx()
	defer cancel()
	if err := c.Get(cx, "/api/snapshot", &snap); err != nil {
		if !healthy(c.BaseURL) {
			fmt.Fprintf(e.out, "tmx daemon is not running (%s)\n", c.BaseURL)
			return exitError{code: 1}
		}
		return err
	}
	running := 0
	for _, a := range snap.Agents {
		switch a.State {
		case model.StateStarting, model.StateWorking, model.StateIdle, model.StateWaiting, model.StatePaused:
			running++
		}
	}
	fmt.Fprintf(e.out, "tmx daemon %s on %s at %s: %d projects, %d agents (%d running), %d approvals waiting\n",
		snap.Version, snap.Machine, c.BaseURL, len(snap.Projects), len(snap.Agents), running, len(snap.Approvals))
	if snap.RollbackWarning != "" {
		fmt.Fprintln(e.out, snap.RollbackWarning)
	}
	return nil
}

// cmdOpen: `tmx open` prints the link that signs a browser on this machine in as the owner;
// `tmx open --launch` starts the daemon if needed and opens that link in the browser itself,
// without printing it (the link carries the owner token).
func cmdOpen(e *env, args []string) error {
	fs := newFlags("open")
	launch := fs.Bool("launch", false, "start tmx if needed and open it in this computer's browser")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	if *launch {
		if _, err := e.ensureUp(); err != nil {
			return err
		}
	}
	c, err := e.owner()
	if err != nil {
		return err
	}
	link := fmt.Sprintf("%s/#token=%s", c.BaseURL, c.Token)
	if !*launch {
		fmt.Fprintln(e.out, link)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := openURL(ctx, link); err != nil {
		// No browser could be started from here: the link itself is the way in.
		fmt.Fprintf(e.errw, "could not open the browser (%v); open this link yourself:\n", err)
		fmt.Fprintln(e.out, link)
		return nil
	}
	fmt.Fprintln(e.out, "Opened tmx in your browser.")
	return nil
}

func cmdApp(e *env, args []string) error {
	if _, err := parse(newFlags("app"), args); err != nil {
		return err
	}
	url, err := e.ensureUp()
	if err != nil {
		return err
	}
	c, err := e.owner()
	if err != nil {
		return err
	}
	return desktop.Run(desktop.Options{URL: url, Token: c.Token, StartDaemon: e.restartDesktopDaemon})
}
