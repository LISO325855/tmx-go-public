package accounts

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

func resetFixture(t *testing.T) (*Service, *entry, *Account, *int, *time.Time) {
	t.Helper()
	now := time.Now()
	a := Account{Harness: "claude", Name: "default", Connected: true, Installed: true, Account: Identity{Email: "a@example.test", Method: MethodSubscription}, CheckedAt: now.UnixMilli(), Limits: []Limit{{ID: "week_fable", UsedPercent: 99, ResetsAt: now.Add(time.Hour).UnixMilli()}, {ID: "week", UsedPercent: 99, ResetsAt: now.Add(time.Hour).UnixMilli()}}, FreeResets: &FreeResets{Known: true, CheckedAt: now.UnixMilli(), accountID: "account-a", Grants: []FreeReset{{Type: "fable", Remaining: 2, UsableNow: true}, {Type: "weekly", Remaining: 1, UsableNow: true}}}}
	s := New(Options{Dir: t.TempDir(), AllowUserLogins: true, Every: -1, Now: func() time.Time { return now }, LookPath: func(model.Harness) (string, error) { return "fixture", nil }})
	t.Cleanup(s.Close)
	e := s.entry("claude", "default")
	e.def = &def{h: Claude, check: func(context.Context, *scope, string) result {
		return result{known: true, connected: a.Connected, id: a.Account, limits: a.Limits, freeResets: a.FreeResets, note: a.LimitsNote, err: a.Error}
	}}
	calls := 0
	s.resetClaim = resetClaim(func(context.Context, *scope, Account, string, bool) string {
		calls++
		now = now.Add(time.Second)
		a.FreeResets.CheckedAt = now.UnixMilli()
		return "reset"
	})
	return s, e, &a, &calls, &now
}

func TestResetTrackerHardRules(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		pct        float64
		owner, dry bool
		calls      int
	}{
		{"below threshold", "fable", 98.999, false, false, 0},
		{"genuine 99", "fable", 99, false, false, 1},
		{"weekly never automatic", "weekly", 100, false, false, 0},
		{"weekly owner switch", "weekly", 99, true, false, 1},
		{"first manual below 99", "fable", 98, true, false, 0},
		{"dry run at 100", "fable", 100, true, true, 0},
		{"nan", "fable", math.NaN(), false, false, 0},
		{"infinity", "fable", math.Inf(1), false, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, e, a, calls, _ := resetFixture(t)
			a.Limits[0].UsedPercent = tc.pct
			s.trackReset(context.Background(), e, *a, tc.kind, tc.owner, tc.dry)
			if *calls != tc.calls {
				t.Fatalf("calls %d, want %d", *calls, tc.calls)
			}
		})
	}
}

func TestResetTrackerRejectsUntrustedObservation(t *testing.T) {
	for _, change := range []func(*Account){
		func(a *Account) { a.Error = "read failed" },
		func(a *Account) { a.LimitsNote = "old usage" },
		func(a *Account) { a.FreeResets.Known = false },
		func(a *Account) { a.FreeResets.accountID = "" },
		func(a *Account) { a.CheckedAt -= 180000 },
		func(a *Account) { a.FreeResets.CheckedAt -= 180000 },
		func(a *Account) { a.FreeResets.CheckedAt += 180000 },
		func(a *Account) { a.FreeResets.Grants[0].UsableNow = false },
		func(a *Account) { a.FreeResets.Grants[0].ExpiresAt = 1 },
	} {
		s, e, a, calls, _ := resetFixture(t)
		change(a)
		s.trackReset(context.Background(), e, *a, "fable", false, false)
		if *calls != 0 {
			t.Fatal("untrusted observation reached claim")
		}
	}
}

func TestResetTrackerIntentBeforeCallAndVerification(t *testing.T) {
	for _, drop := range []string{"both", "usage", "grant", "neither", "window", "identity"} {
		t.Run(drop, func(t *testing.T) {
			s, e, a, calls, now := resetFixture(t)
			s.resetClaim = resetClaim(func(context.Context, *scope, Account, string, bool) string {
				*calls++
				b, err := os.ReadFile(s.resetFile(a.FreeResets.accountID))
				if err != nil {
					t.Fatal(err)
				}
				var intent resetAttempt
				if json.Unmarshal(b, &intent) != nil || intent.State != "needs_you" || intent.Used != 99 {
					t.Fatal("missing durable intent")
				}
				*now = now.Add(time.Second)
				// Replacement pointers mimic a genuinely fresh check.
				r := *a.FreeResets
				r.Grants = append([]FreeReset{}, r.Grants...)
				r.CheckedAt = now.UnixMilli()
				a.FreeResets = &r
				a.Limits = append([]Limit{}, a.Limits...)
				if drop == "both" || drop == "usage" || drop == "window" || drop == "identity" {
					a.Limits[0].UsedPercent = 1
				}
				if drop == "both" || drop == "grant" || drop == "window" || drop == "identity" {
					r.Grants[0].Remaining--
				}
				if drop == "window" {
					a.Limits[0].ResetsAt++
				}
				if drop == "identity" {
					r.accountID = "account-b"
				}
				return "reset"
			})
			got := s.trackReset(context.Background(), e, *a, "fable", false, false)
			if (got.Reset.State == "succeeded") != (drop == "both") {
				t.Fatalf("%+v", got.Reset)
			}
			// A new Service instance sees persisted state; no second mutation after restart.
			s2 := New(Options{Dir: s.o.Dir, Every: -1, Now: func() time.Time { return *now }})
			defer s2.Close()
			s2.resetClaim = resetClaim(func(context.Context, *scope, Account, string, bool) string { t.Fatal("repeat mutation"); return "" })
			a.CheckedAt = now.UnixMilli()
			s2.trackReset(context.Background(), e, *a, "fable", false, false)
			if *calls != 1 {
				t.Fatal("duplicate")
			}
		})
	}
}

func TestResetTrackerLockAndCorruptState(t *testing.T) {
	for _, suffix := range []string{".lock", ""} {
		t.Run(suffix, func(t *testing.T) {
			s, e, a, calls, _ := resetFixture(t)
			if err := os.WriteFile(s.resetFile(a.FreeResets.accountID)+suffix, []byte("broken"), 0600); err != nil {
				t.Fatal(err)
			}
			got := s.trackReset(context.Background(), e, *a, "fable", false, false)
			if *calls != 0 || got.Reset.State != "needs_you" {
				t.Fatal("unsafe recovery")
			}
		})
	}
}

func TestResetTrackerDryRunAndReconciliation(t *testing.T) {
	s, e, a, calls, now := resetFixture(t)
	s.o.ResetDryRun = true
	s.trackReset(context.Background(), e, *a, "fable", false, false)
	if *calls != 0 {
		t.Fatal("dry-run option called executor")
	}
	if _, err := os.Stat(s.resetFile(a.FreeResets.accountID)); !os.IsNotExist(err) {
		t.Fatal("dry run persisted mutation intent")
	}
	s.o.ResetDryRun = false
	s.trackReset(context.Background(), e, *a, "fable", false, false)
	*now = now.Add(time.Minute)
	a.CheckedAt = now.UnixMilli()
	a.FreeResets.CheckedAt = now.UnixMilli()
	a.Limits[0].UsedPercent = 2
	a.FreeResets.Grants[0].Remaining = 1
	got := s.trackReset(context.Background(), e, *a, "fable", false, false)
	if *calls != 1 || got.Reset.State != "succeeded" {
		t.Fatalf("reconciliation: calls=%d status=%+v", *calls, got.Reset)
	}
}

func TestResetTrackerSingleFlightAcrossAliases(t *testing.T) {
	s, e, a, _, _ := resetFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	s.resetClaim = resetClaim(func(context.Context, *scope, Account, string, bool) string {
		close(entered)
		<-release
		return "unavailable"
	})
	done := make(chan struct{})
	go func() { defer close(done); s.trackReset(context.Background(), e, *a, "fable", false, false) }()
	<-entered
	alias := &entry{name: "other-name", def: e.def}
	got := s.trackReset(context.Background(), alias, *a, "fable", false, false)
	close(release)
	<-done
	if got.Reset == nil || !strings.Contains(got.Reset.Message, "locked") {
		t.Fatalf("second alias was not locked: %+v", got.Reset)
	}
}

func TestResetRealClientsRefuseGoTest(t *testing.T) {
	s, e, a, _, _ := resetFixture(t)
	if got := liveResetClaim(context.Background(), s.scope(e), *a, "fable", false); got != "disabled" {
		t.Fatal(got)
	}
	// A fake sentinel transport proves the guard stops before real networking.
	old := http.DefaultTransport
	defer func() { http.DefaultTransport = old }()
	http.DefaultTransport = offerTransport(func(*http.Request) (*http.Response, error) { t.Fatal("live network reached"); return nil, nil })
	for _, url := range []string{claudeOffersURL, "https://api.anthropic.com/api/organizations/fixture/reset_rate_limits"} {
		req, _ := http.NewRequest("GET", url, nil)
		if _, err := offerClient().Do(req); err == nil {
			t.Fatal("real client allowed under go test")
		}
	}
	// Injected fakes remain usable without disabling the real-client guard.
	fake := &http.Client{Transport: offerTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"limits":[]}`)), Header: make(http.Header)}, nil
	})}
	cfg, cred := offerFiles(t, time.Now())
	if !readClaudeOffersWithReader(context.Background(), fakeOfferResponseReader{fake}, cfg, cred, "a@example.test", time.Now()).Known {
		t.Fatal("fake client refused")
	}
}

func TestResetConfirmationNeverChoosesOtherSpending(t *testing.T) {
	yes := "Use a Fable reset?\n\u276f 1. Yes, use my Fable reset\n  2. No, keep it"
	for _, tc := range []struct{ text, kind, want string }{
		{yes, "fable", "yes"},
		{strings.ReplaceAll(yes, "Fable", "weekly"), "fable", ""},
		{yes + "\nEnable extra usage", "fable", ""},
		{"\u276f Yes\nPurchase credits", "fable", ""},
		{strings.ReplaceAll(yes, "\u276f 1.", "1."), "fable", ""},
		{"Use a Fable reset?\n  1. Yes, use my Fable reset\n\u276f 2. No, keep it", "fable", "no"},
	} {
		if got := resetScreenConfirmation(tc.text, tc.kind); got != tc.want {
			t.Fatalf("got %q want %q", got, tc.want)
		}
	}
}
