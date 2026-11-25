package term

import (
	"context"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
)

func TestRepaintDeadlineWithFullEventBuffer(t *testing.T) {
	p, _ := newGuardedTestProcess(t)
	p.events = make(chan runner.Event, EventBuffer)
	p.done = make(chan struct{})
	defer close(p.done)
	p.w.output([]byte("unrecognized active output"))
	produced := make(chan struct{})
	go func() {
		defer close(produced)
		for i := 0; i < EventBuffer*4; i++ {
			p.mu.Lock()
			p.emitLocked(runner.Event{Kind: runner.EvOutput, Output: []byte("noise")})
			p.mu.Unlock()
		}
	}()
	select {
	case <-produced:
	case <-time.After(time.Second):
		t.Fatal("output blocked while holding process lock")
	}
	finished := make(chan error, 1)
	go func() { finished <- p.awaitRepaint(context.Background(), 50*time.Millisecond) }()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("unrecognized output was accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("full events channel bypassed repaint deadline")
	}
	p.mu.Lock()
	p.closeEventsLocked()
	p.mu.Unlock()
	for range p.Events() {
	}
}

func TestOverflowPreservesControlOrderAndExit(t *testing.T) {
	p := &process{events: make(chan runner.Event, 1)}
	p.events <- runner.Event{Kind: runner.EvOutput}
	expected := []runner.Event{
		{Kind: runner.EvState, State: model.StateWaiting},
		{Kind: runner.EvApproval, Approval: &runner.ApprovalRequest{ID: "approval"}},
		{Kind: runner.EvMailDelivery, MessageIDs: []string{"owner"}, Delivery: "submitted"},
		{Kind: runner.EvExit, ExitCode: 0},
	}
	emitted := make(chan struct{})
	go func() {
		p.mu.Lock()
		for _, ev := range expected {
			p.emitLocked(ev)
		}
		p.emitLocked(runner.Event{Kind: runner.EvOutput, Output: []byte("overflow")})
		p.closeEventsLocked()
		p.mu.Unlock()
		close(emitted)
	}()
	select {
	case <-emitted:
	case <-time.After(time.Second):
		t.Fatal("control emission blocked on consumer")
	}
	<-p.Events() // original output
	for _, want := range expected {
		select {
		case got := <-p.Events():
			if got.Kind != want.Kind || got.Delivery != want.Delivery {
				t.Fatalf("control order: got %+v want %+v", got, want)
			}
		case <-time.After(time.Second):
			t.Fatal("lost control event")
		}
	}
	select {
	case _, ok := <-p.Events():
		if ok {
			t.Fatal("overflow output was retained")
		}
	case <-time.After(time.Second):
		t.Fatal("events did not close after draining")
	}
}

// Regression from integration-review-c on Hostc: a full output buffer plus
// one progress event used to hold mu and defeat even a 30ms repaint deadline.
func TestIntegrationRepaintFullEvents(t *testing.T) {
	p, _ := newGuardedTestProcess(t)
	p.events = make(chan runner.Event, EventBuffer)
	p.done = make(chan struct{})
	defer close(p.done)
	for i := 0; i < EventBuffer; i++ {
		p.events <- runner.Event{Kind: runner.EvOutput}
	}
	entered := make(chan struct{})
	emitted := make(chan struct{})
	go func() {
		p.mu.Lock()
		close(entered)
		p.emitLocked(runner.Event{Kind: runner.EvProgress})
		p.mu.Unlock()
		close(emitted)
	}()
	<-entered
	defer func() {
		<-p.events // allow cleanup even when testing the old blocking emitter
		<-emitted
		p.mu.Lock()
		p.closeEventsLocked()
		p.mu.Unlock()
		progress := 0
		for ev := range p.events {
			if ev.Kind == runner.EvProgress {
				progress++
			}
		}
		if progress != 1 {
			t.Errorf("lost/duplicated progress event: %d", progress)
		}
	}()
	done := make(chan error, 1)
	go func() { done <- p.awaitRepaint(context.Background(), 30*time.Millisecond) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("missing repaint was accepted")
		}
	case <-time.After(150 * time.Millisecond):
		t.Fatal("30ms repaint deadline blocked beyond 150ms on full event channel")
	}
}
