package slots

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAdoptSurvivorsAboveCapacity(t *testing.T) {
	old := New(Options{Sizes: map[string]int{"heavy": 2}})
	holder := Holder{AgentID: "worker", Name: "worker"}
	first, err := old.Acquire("heavy", holder, "first", 10)
	if err != nil {
		t.Fatal(err)
	}
	second, err := old.Acquire("heavy", holder, "second", 11)
	if err != nil {
		t.Fatal(err)
	}
	old.Close()
	restarted := New(Options{Sizes: map[string]int{"heavy": 1}})
	for _, survivor := range []Lease{first, second, first} {
		if _, err := restarted.Recover(survivor, true); err != nil {
			t.Fatal(err)
		}
	}
	waiter, err := restarted.Acquire("heavy", Holder{}, "third", 12)
	if err != nil || waiter.State != Waiting || restarted.View().Kinds[0].Held != 2 {
		t.Fatalf("waiter=%+v, view=%+v, error=%v", waiter, restarted.View(), err)
	}
	if err := restarted.Release(first.ID, holder); err != nil {
		t.Fatal(err)
	}
	got, err := restarted.Renew(waiter.ID, Holder{})
	if err != nil || got.State != Waiting {
		t.Fatalf("should wait at capacity: %+v, %v", got, err)
	}
	if err := restarted.Release(second.ID, holder); err != nil {
		t.Fatal(err)
	}
	got, err = restarted.Wait(context.Background(), waiter.ID, Holder{}, time.Second)
	if err != nil || got.State != Held {
		t.Fatalf("should run after release: %+v, %v", got, err)
	}
}

func TestRecoverWaiterAfterShutdownAndRejectEndedLeases(t *testing.T) {
	old := New(Options{Sizes: map[string]int{"heavy": 1}})
	survivor, _ := old.Acquire("heavy", Holder{}, "running", 10)
	waiter, _ := old.Acquire("heavy", Holder{}, "waiting", 11)
	old.Close()
	if _, err := old.Recover(waiter, false); !errors.Is(err, ErrClosed) {
		t.Fatalf("recover during close: %v", err)
	}
	next := New(Options{Sizes: map[string]int{"heavy": 1}})
	if _, err := next.Recover(survivor, true); err != nil {
		t.Fatal(err)
	}
	l, err := next.Recover(waiter, true)
	if err != nil || l.State != Waiting {
		t.Fatalf("waiter skipped capacity: %+v, %v", l, err)
	}
	if err := next.Release(survivor.ID, Holder{}); err != nil {
		t.Fatal(err)
	}
	if _, err := next.Recover(survivor, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ended lease recovered in current daemon: %v", err)
	}
	l, err = next.Recover(waiter, false)
	if err != nil || l.State != Held {
		t.Fatalf("waiter was not promoted: %+v, %v", l, err)
	}
}
