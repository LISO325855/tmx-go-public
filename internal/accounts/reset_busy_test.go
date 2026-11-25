package accounts

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/store"
)

func TestResetInFlightBlocksUpdateThroughVerification(t *testing.T) {
	s, e, a, _, now := resetFixture(t)
	paths := config.Paths{Home: t.TempDir()}
	db, err := store.Open(paths.DB())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	f, err := fleet.New(fleet.Options{Store: db, Paths: paths, ResetInFlight: s.ResetInFlight})
	if err != nil {
		t.Fatal(err)
	}
	// Nothing is started: the fleet only supplies the updater's real Busy gate.
	busy := func() bool { return slices.Contains(f.UpdateBusy(time.Now()), "account reset in progress") }
	if busy() {
		t.Fatal("idle accounts blocked update")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	verifying, verified := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.resetClaim = resetClaim(func(claimCtx context.Context, _ *scope, _ Account, _ string, _ bool) string {
		if deadline, ok := claimCtx.Deadline(); !ok || time.Until(deadline) > resetClaimTimeout {
			t.Error("claim has no bounded deadline")
		}
		close(entered)
		select {
		case <-release:
		case <-claimCtx.Done():
		}
		return "reset"
	})
	e.def.check = func(context.Context, *scope, string) result {
		close(verifying)
		select {
		case <-verified:
		case <-ctx.Done():
		}
		*now = now.Add(time.Second)
		a.Limits[0].UsedPercent = 0
		a.FreeResets.Grants[0].Remaining--
		a.FreeResets.CheckedAt = now.UnixMilli()
		return result{known: true, connected: true, id: a.Account, limits: a.Limits, freeResets: a.FreeResets}
	}
	done := make(chan Account, 1)
	go func() { done <- s.trackReset(ctx, e, *a, "fable", false, false) }()
	wait := func(c <-chan struct{}) {
		t.Helper()
		select {
		case <-c:
		case <-ctx.Done():
			t.Fatal("fake reset timed out")
		}
	}
	wait(entered)
	if !busy() {
		t.Error("update may interrupt in-flight fake claim")
	}
	if releaseRestart, err := s.ReserveRestart(); err == nil {
		releaseRestart()
		t.Error("restart reservation accepted during claim")
	}
	close(release)
	wait(verifying)
	if !busy() {
		t.Error("update may interrupt result verification")
	}
	if releaseRestart, err := s.ReserveRestart(); err == nil {
		releaseRestart()
		t.Error("restart reservation accepted during verification")
	}
	close(verified)
	select {
	case got := <-done:
		if got.Reset.State != "succeeded" {
			t.Fatalf("reset result: %+v", got.Reset)
		}
	case <-ctx.Done():
		t.Fatal("reset did not finish")
	}
	if busy() {
		t.Fatal("update cannot proceed after verified result")
	}
}

func TestResetInFlightDeadlineAndConcurrentClaims(t *testing.T) {
	s := &Service{}
	ctx1, finish1, _ := s.beginReset(context.Background())
	defer finish1()
	ctx2, finish2, _ := s.beginReset(context.Background())
	defer finish2()
	finish1()
	if !s.ResetInFlight(time.Now()) {
		t.Fatal("finishing one claim cleared another")
	}
	deadline, _ := ctx2.Deadline()
	if !s.ResetInFlight(deadline.Add(-time.Nanosecond)) {
		t.Fatal("lease ended early")
	}
	if s.ResetInFlight(deadline) {
		t.Fatal("stuck claim blocks update after timeout")
	}
	if ctx1.Err() != context.Canceled {
		t.Fatal("finished claim was not canceled")
	}
	parent, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Second))
	defer cancel()
	child, finish, _ := s.beginReset(parent)
	defer finish()
	parentDeadline, _ := parent.Deadline()
	childDeadline, _ := child.Deadline()
	if childDeadline != parentDeadline || s.ResetInFlight(parentDeadline) {
		t.Fatal("caller deadline did not bound the busy signal")
	}
}

func TestResetRestartReservationRefusesClaimUntilReleased(t *testing.T) {
	s, e, a, calls, _ := resetFixture(t)
	release, err := s.ReserveRestart()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := s.ReserveRestart(); err == nil {
		t.Fatal("duplicate reservation admitted")
	}
	got := s.trackReset(context.Background(), e, *a, "fable", false, false)
	if *calls != 0 || got.Reset == nil || got.Reset.State != "needs_you" {
		t.Fatalf("claim admitted during restart: calls=%d result=%+v", *calls, got.Reset)
	}
	release()
	// An old release must not clear a newer reservation.
	release2, err := s.ReserveRestart()
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, _, err := s.beginReset(context.Background()); err == nil {
		t.Fatal("stale release reopened admission")
	}
	release2()
	s.trackReset(context.Background(), e, *a, "fable", false, false)
	if *calls != 1 {
		t.Fatalf("abandoned restart did not restore claim admission: %d", *calls)
	}
}

func TestResetRestartReservationRacesClaimAdmission(t *testing.T) {
	for i := 0; i < 100; i++ {
		s := &Service{}
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var claimDone, restartDone func()
		var claimErr, restartErr error
		go func() { defer wg.Done(); <-start; _, claimDone, claimErr = s.beginReset(context.Background()) }()
		go func() { defer wg.Done(); <-start; restartDone, restartErr = s.ReserveRestart() }()
		close(start)
		wg.Wait()
		if claimDone != nil {
			claimDone()
		}
		if restartDone != nil {
			restartDone()
		}
		if (claimErr == nil) == (restartErr == nil) {
			t.Fatalf("exactly one must be admitted: claim=%v restart=%v", claimErr, restartErr)
		}
	}
}
