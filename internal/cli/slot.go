package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/client"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/hiddenexec"
	"github.com/blackcode-ch/tmx-go/internal/slots"
)

const (
	slotRenewEvery = 10 * time.Second
	slotWaitSecs   = 15
	slotNoticeGap  = 30 * time.Second
)

// slotClient talks to the daemon as the agent this runs inside (its env), else as the owner.
func (e *env) slotClient() (*client.Client, error) {
	url, tok := os.Getenv(fleet.EnvURL), os.Getenv(fleet.EnvToken)
	if url != "" && tok != "" {
		return client.New(url, tok), nil
	}
	return e.owner()
}

// cmdSlot runs a command once a machine-wide slot of its kind is free:
// tmx slot [--kind heavy|browser] -- <command...>. When the daemon cannot be reached the
// command runs anyway (the queue never blocks work).
func cmdSlot(e *env, args []string) error {
	fs := newFlags("slot")
	kind := fs.String("kind", "heavy", "slot kind: heavy (builds, tests, installs) or browser")
	if err := fs.Parse(args); err != nil {
		return usageErr("tmx slot: %v", err)
	}
	argv := fs.Args()
	if len(argv) == 0 {
		return usageErr("usage: tmx slot [--kind heavy|browser] -- <command...>")
	}
	c, err := e.slotClient()
	if err != nil {
		fmt.Fprintf(e.errw, "tmx slot: %v; running without a slot\n", err)
		return runSlotCommand(argv, nil)
	}
	cx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var l struct {
		slots.Lease
		Proof string `json:"recovery_proof,omitempty"`
	}
	acquire := func() error {
		return c.Post(cx, "/api/slots", map[string]any{"kind": *kind, "command": strings.Join(argv, " "), "pid": os.Getpid()}, &l)
	}
	retry := func() bool {
		select {
		case <-cx.Done():
			return false
		case <-time.After(time.Second):
			return true
		}
	}
	err = acquire()
	var ae *client.APIError
	if errors.As(err, &ae) && ae.Status == http.StatusServiceUnavailable {
		fmt.Fprintln(e.errw, "tmx slot: daemon stopping; waiting to acquire a slot after restart")
		for err != nil && retry() {
			err = acquire()
			if errors.As(err, &ae) && ae.Status != http.StatusServiceUnavailable {
				break
			}
		}
		if cx.Err() != nil {
			return exitError{code: 130, msg: "interrupted while waiting for a slot"}
		}
	}
	switch {
	case errors.As(err, &ae) && ae.Status != http.StatusNotImplemented && ae.Status != http.StatusServiceUnavailable:
		return err
	case err != nil:
		fmt.Fprintf(e.errw, "tmx slot: the daemon is not reachable (%v); running without a slot\n", err)
		return runSlotCommand(argv, nil)
	}
	release := func() {
		rc, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer rcancel()
		if l.Proof != "" {
			_ = c.Do(rc, http.MethodDelete, "/api/slots/recovery", map[string]string{"proof": l.Proof}, nil)
			return
		}
		_ = c.Do(rc, http.MethodDelete, "/api/slots/"+client.Seg(l.ID), nil, nil)
	}
	defer release()
	recoverLease := func(ctx context.Context) error {
		return c.Post(ctx, "/api/slots/recovery", map[string]string{"proof": l.Proof}, &l)
	}

	lastPos, lastNote := 0, time.Time{}
	for l.State != slots.Held {
		if l.Position != lastPos || time.Since(lastNote) > slotNoticeGap {
			fmt.Fprintf(e.errw, "tmx slot: waiting for a %s slot (%s in line)\n", l.Kind, ordinal(l.Position))
			lastPos, lastNote = l.Position, time.Now()
		}
		var err error
		if l.Proof != "" {
			err = recoverLease(cx)
		} else {
			err = c.Post(cx, fmt.Sprintf("/api/slots/%s/wait?timeout=%d", client.Seg(l.ID), slotWaitSecs), nil, &l)
		}
		if cx.Err() != nil {
			return exitError{code: 130, msg: "interrupted while waiting for a slot"}
		}
		if err != nil {
			if !errors.As(err, &ae) || ae.Status == http.StatusServiceUnavailable {
				retry()
				continue
			}
			return err
		}
	}
	renew := func(stop <-chan struct{}) {
		t := time.NewTicker(slotRenewEvery)
		defer t.Stop()
		warned := false
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				rc, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
				var err error
				if l.Proof != "" {
					err = recoverLease(rc)
				} else {
					err = c.Post(rc, "/api/slots/"+client.Seg(l.ID)+"/renew", nil, nil)
				}
				rcancel()
				if err != nil && !warned {
					fmt.Fprintf(e.errw, "tmx slot: could not renew the slot (%v); the command keeps running\n", err)
					warned = true
				}
				var lost *client.APIError
				if errors.As(err, &lost) && lost.Status == http.StatusNotFound {
					return
				}
				if err == nil {
					warned = false
				}
			}
		}
	}
	return runSlotCommand(argv, renew)
}

// runSlotCommand runs argv with this terminal, forwards Ctrl-C/SIGTERM, and returns its exit code.
func runSlotCommand(argv []string, during func(stop <-chan struct{})) error {
	cmd := hiddenexec.CommandInheritConsole(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return exitError{code: 127, msg: err.Error()}
	}
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sigs:
				_ = cmd.Process.Signal(s)
			case <-stop:
				return
			}
		}
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if during != nil {
			during(stop)
		}
	}()
	err := cmd.Wait()
	close(stop)
	<-done
	if err == nil {
		return nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code := ee.ExitCode()
		if code < 0 {
			code = 128 + 9
		}
		return exitError{code: code}
	}
	return err
}

func ordinal(n int) string {
	if n <= 0 {
		return "next"
	}
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", n, suffix)
}

func cmdSlots(e *env, args []string) error {
	if _, err := parse(newFlags("slots"), args); err != nil {
		return err
	}
	return withOwner(e, func(c *client.Client, cx context.Context) error {
		var v slots.View
		if err := c.Get(cx, "/api/slots", &v); err != nil {
			return err
		}
		tw := tabwriter.NewWriter(e.out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "KIND\tIN USE\tWAITING")
		for _, k := range v.Kinds {
			fmt.Fprintf(tw, "%s\t%d of %d\t%d\n", k.Kind, k.Held, k.Size, k.Waiting)
		}
		if len(v.Leases) > 0 {
			fmt.Fprintln(tw, "\nHOLDER\tKIND\tSTATE\tCOMMAND")
			for _, l := range v.Leases {
				st := l.State
				if l.State == slots.Waiting {
					st = fmt.Sprintf("waiting (%s)", ordinal(l.Position))
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", l.Holder, l.Kind, st, truncate(l.Command, 80))
			}
		}
		return tw.Flush()
	})
}
