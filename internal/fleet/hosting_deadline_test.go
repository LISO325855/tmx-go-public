package fleet

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
)

type delayedReattacher struct {
	runner.Runner
	entered chan struct{}
	release chan struct{}
	late    *lateDetachedProcess
}

func (r *delayedReattacher) Reattach(ctx context.Context, _ runner.Spec) (runner.Process, string, error) {
	close(r.entered)
	<-r.release // intentionally ignores cancellation to exercise the outer bound
	return r.late, "", nil
}

type lateDetachedProcess struct {
	runner.Process
	detached chan struct{}
	events   chan runner.Event
}

func (p *lateDetachedProcess) Detach() bool                { close(p.detached); close(p.events); return true }
func (p *lateDetachedProcess) Events() <-chan runner.Event { return p.events }

func TestReattachDeadlineDetachesLateResult(t *testing.T) {
	late := &lateDetachedProcess{detached: make(chan struct{}), events: make(chan runner.Event)}
	ra := &delayedReattacher{entered: make(chan struct{}), release: make(chan struct{}), late: late}
	started := time.Now()
	p, _, err := reattachWithin(context.Background(), ra, runner.Spec{}, 30*time.Millisecond)
	if p != nil || !errors.Is(err, runner.ErrHostUnknown) || time.Since(started) > time.Second {
		t.Fatalf("unbounded handover: %v", err)
	}
	close(ra.release)
	select {
	case <-late.detached:
	case <-time.After(time.Second):
		t.Fatal("late process was adopted or leaked")
	}
}

type cancellationReattacher struct {
	runner.Runner
	entered chan struct{}
}

func (r *cancellationReattacher) Reattach(ctx context.Context, _ runner.Spec) (runner.Process, string, error) {
	close(r.entered)
	<-ctx.Done()
	return nil, "", runner.ErrHostUnknown
}

func TestStartupCancellationReachesReattach(t *testing.T) {
	e := newEnv(t)
	hr := newHostedRunner(e.term)
	e.hostExe, e.termRunner = "/opt/tmx/tmx-host", hr
	f1 := e.started()
	_, err := f1.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: workerProject(t, f1), Name: "cancel-handover", Mode: model.ModeTerminal})
	if err != nil {
		t.Fatal(err)
	}
	if err := f1.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	held := &cancellationReattacher{Runner: e.term, entered: make(chan struct{})}
	e.termRunner = held
	f2 := e.fleet()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- f2.StartContext(ctx) }()
	select {
	case <-held.entered:
	case <-time.After(time.Second):
		t.Fatal("reattach not entered")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("startup cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("daemon shutdown signal did not interrupt startup")
	}
}
