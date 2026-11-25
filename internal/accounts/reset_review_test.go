package accounts

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Review-only regression: a drop that remains >=99 must not rearm the event.
func TestReviewSameEventDoesNotReclaim(t *testing.T) {
	s, e, a, calls, now := resetFixture(t)
	a.Limits[0].UsedPercent = 100
	s.resetClaim = resetClaim(func(context.Context, *scope, Account, string, bool) string {
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
	got := s.trackReset(context.Background(), e, *a, "fable", false, false)
	if got.Reset.State != "succeeded" {
		t.Fatalf("first not verified: %+v", got.Reset)
	}
	*now = now.Add(16 * time.Minute)
	a.CheckedAt = now.UnixMilli()
	a.FreeResets.CheckedAt = now.UnixMilli()
	s.trackReset(context.Background(), e, *a, "fable", false, false)
	if *calls != 1 {
		t.Fatalf("same sustained >=99 event claimed %d times", *calls)
	}
}

// No real network: replacing DefaultTransport proves the injected client bypass.
func TestReviewInjectedHTTPStillHitsTransportInGoTest(t *testing.T) {
	old := http.DefaultTransport
	defer func() { http.DefaultTransport = old }()
	calls := 0
	http.DefaultTransport = offerTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"limits":[]}`)), Header: make(http.Header)}, nil
	})
	cfg, cred := offerFiles(t, time.Now())
	// The arbitrary HTTP injection seam was removed. Exercise the only live reader.
	(liveClaudeOfferReader{}).ReadOffers(context.Background(), cfg, cred, "a@example.test", time.Now())
	if calls != 0 {
		t.Fatal("injected standard HTTP client bypassed go-test network refusal")
	}
}
