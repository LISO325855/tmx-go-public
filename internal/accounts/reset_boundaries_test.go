package accounts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestResetRearmPersistsAcrossRestart(t *testing.T) {
	for _, transition := range []string{"sustained", "below", "new-window", "unknown-window"} {
		t.Run(transition, func(t *testing.T) {
			s, e, a, calls, now := resetFixture(t)
			a.Limits[0].UsedPercent = 100
			fake := resetClaim(func(context.Context, *scope, Account, string, bool) string {
				*calls++
				*now = now.Add(time.Second)
				r := *a.FreeResets
				r.Grants = append([]FreeReset{}, r.Grants...)
				r.Grants[0].Remaining--
				r.CheckedAt = now.UnixMilli()
				a.FreeResets = &r
				a.Limits = append([]Limit{}, a.Limits...)
				a.Limits[0].UsedPercent = 99
				return "reset"
			})
			s.resetClaim = fake
			if got := s.trackReset(context.Background(), e, *a, "fable", false, false); got.Reset.State != "succeeded" {
				t.Fatal(got.Reset)
			}
			*now = now.Add(16 * time.Minute)
			a.CheckedAt = now.UnixMilli()
			a.FreeResets.CheckedAt = now.UnixMilli()
			if transition == "below" {
				a.Limits[0].UsedPercent = 98
				s.trackReset(context.Background(), e, *a, "fable", false, false)
				a.Limits[0].UsedPercent = 99
			}
			if transition == "new-window" {
				a.Limits[0].ResetsAt += int64(time.Hour / time.Millisecond)
			}
			if transition == "unknown-window" {
				a.Limits[0].ResetsAt = 0
			}
			restarted := New(s.o)
			defer restarted.Close()
			restarted.resetClaim = fake
			restarted.trackReset(context.Background(), e, *a, "fable", false, false)
			want := 1
			if transition == "below" || transition == "new-window" {
				want = 2
			}
			if *calls != want {
				t.Fatalf("calls=%d want=%d", *calls, want)
			}
		})
	}
}

func TestResetConfirmationBindsCurrentObservation(t *testing.T) {
	for _, change := range []string{"valid", "expired-event", "expired-read", "future-read", "rolled-window", "elapsed-window", "wrong-account", "wrong-email", "below", "unknown-window", "stale-offers"} {
		t.Run(change, func(t *testing.T) {
			_, _, a, _, now := resetFixture(t)
			event := *a
			current := *a
			current.Limits = append([]Limit{}, a.Limits...)
			r := *a.FreeResets
			current.FreeResets = &r
			switch change {
			case "expired-event":
				event.CheckedAt -= int64(3 * time.Minute / time.Millisecond)
			case "expired-read":
				current.CheckedAt -= int64(3 * time.Minute / time.Millisecond)
			case "future-read":
				current.CheckedAt += int64(time.Minute / time.Millisecond)
			case "rolled-window":
				current.Limits[0].ResetsAt++
			case "elapsed-window":
				*now = now.Add(2 * time.Hour)
			case "wrong-account":
				r.accountID = "replacement"
			case "wrong-email":
				current.Account.Email = "other@example.test"
			case "below":
				current.Limits[0].UsedPercent = 98.999
			case "unknown-window":
				current.Limits[0].ResetsAt = 0
			case "stale-offers":
				r.CheckedAt -= int64(3 * time.Minute / time.Millisecond)
			}
			if got := resetCanConfirm(event, current, "fable", now.UnixMilli()); got != (change == "valid") {
				t.Fatalf("confirmation allowed=%v", got)
			}
		})
	}
}

func TestResetDurabilityFailuresBlockClaims(t *testing.T) {
	for _, failAt := range []int{1, 2, 3, 4} {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			s, e, a, calls, _ := resetFixture(t)
			syncs := 0
			s.resetSyncDir = func(dir string) error {
				syncs++
				if syncs == failAt {
					return errors.New("fixture metadata flush failure")
				}
				return syncResetDirectory(dir)
			}
			got := s.trackReset(context.Background(), e, *a, "fable", false, false)
			want := 0
			if failAt >= 3 {
				want = 1
			}
			if *calls != want || got.Reset.State != "needs_you" {
				t.Fatalf("calls=%d status=%+v", *calls, got.Reset)
			}
			if _, err := os.Stat(s.resetFile(a.FreeResets.accountID) + ".lock"); err != nil {
				t.Fatal("failed durability did not retain lock", err)
			}
			s.trackReset(context.Background(), e, *a, "fable", false, false)
			if *calls != want {
				t.Fatal("durability failure permitted another claim")
			}
		})
	}
}

func TestResetDurablePublishUsesRealDirectoryFlush(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attempt.json")
	if err := saveReset(path, resetAttempt{Email: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if err := saveReset(path, resetAttempt{Email: "replacement"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "replacement") {
		t.Fatal("atomic replacement failed", err)
	}
}

type fixtureOffers func() *FreeResets

func (f fixtureOffers) ReadOffers(context.Context, string, string, string, time.Time) *FreeResets {
	return f()
}

func TestResetDryRunDoesNotInvokeOfferReader(t *testing.T) {
	if _, exists := reflect.TypeOf(Options{}).FieldByName("OfferHTTP"); exists {
		t.Fatal("arbitrary HTTP injection reintroduced")
	}
	s, e, a, _, now := resetFixture(t)
	cfg, _ := offerFiles(t, *now)
	s.o.Env = []string{"CLAUDE_CONFIG_DIR=" + filepath.Dir(cfg)}
	calls := 0
	s.offerReader = fixtureOffers(func() *FreeResets { calls++; return a.FreeResets })
	sc := s.scope(e)
	sc.resetDryRun = true
	sc.claudeFreeResets(context.Background(), a.Account.Email)
	sc.resetDryRun = false
	s.o.ResetDryRun = true
	sc.claudeFreeResets(context.Background(), a.Account.Email)
	if calls != 0 {
		t.Fatal("dry run reached reader")
	}
	s.o.ResetDryRun = false
	if !sc.claudeFreeResets(context.Background(), a.Account.Email).Known || calls != 1 {
		t.Fatal("fake reader not injected")
	}
}

func TestResetDryRunDoesNotStartUsageRead(t *testing.T) {
	s, e, a, _, _ := resetFixture(t)
	e.acct = *a
	e.def.check = func(context.Context, *scope, string) result {
		t.Fatal("dry run started a usage command")
		return result{}
	}
	if got := s.checkMode(e, true); got.Reset == nil || got.Reset.State != "dry_run" {
		t.Fatal("missing dry-run result")
	}
	s.o.ResetDryRun = true
	if got := s.check(e); got.Reset == nil || got.Reset.State != "dry_run" {
		t.Fatal("missing global dry-run result")
	}
}

func TestResetPTYCreationBoundaryRejectsFakeExecutable(t *testing.T) {
	// The test binary is the fake executable. If launched, only the marker helper
	// runs; no Claude installation, shell, or real account can be reached.
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, dry := range []bool{false, true} {
		marker := filepath.Join(t.TempDir(), "started")
		args := []string{"-test.run=^TestResetFakeProcess$", "--", "/limit-reset fable"}
		helper, err := startLogin(Claude, t.TempDir(), executable, args, append(os.Environ(), "TMX_RESET_FAKE_MARKER="+marker), dry)
		if helper != nil {
			helper.stop(time.Millisecond)
			t.Fatal("reset process launched")
		}
		if err == nil || !strings.Contains(err.Error(), "disabled") {
			t.Fatalf("wrong boundary refusal: %v", err)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatal("fake executable started")
		}
	}
}

func TestResetFakeProcess(t *testing.T) {
	if marker := os.Getenv("TMX_RESET_FAKE_MARKER"); marker != "" {
		_ = os.WriteFile(marker, []byte("started"), 0600)
		os.Exit(0)
	}
}
