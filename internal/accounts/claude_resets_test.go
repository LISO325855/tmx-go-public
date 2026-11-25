package accounts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixtureOfferClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

type fakeOfferResponseReader struct{ client *http.Client }

func (f fakeOfferResponseReader) readOffer(r *http.Request) (*http.Response, error) {
	return f.client.Do(r)
}

type offerTransport func(*http.Request) (*http.Response, error)

func (f offerTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClaudeResetOffers(t *testing.T) {
	now := time.Date(2026, 11, 18, 19, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, body string
		known      bool
		count      int
	}{
		{"separate types", `{"cedar_ember":{"eligible":true,"grants":[{"type":"weekly","gives_back_weekly":true,"resets_left":2,"ends_at":"2026-11-20T00:00:00Z"},{"type":"weekly","resets_left":1,"ends_at":"2026-11-21T00:00:00Z"},{"type":"weekly","resets_left":8,"ends_at":"2026-11-17T00:00:00Z"}]}}`, true, 2},
		{"confirmed absent", `{"limits":[]}`, true, 0},
		{"confirmed null", `{"cedar_ember":null}`, true, 0},
		{"empty response", `{}`, false, 0},
		{"in band error", `{"error":"unavailable","limits":[]}`, false, 0},
		{"malformed count", `{"cedar_ember":{"eligible":true,"grants":[{"type":"weekly","resets_left":-1}]}}`, false, 0},
		{"malformed expiry", `{"cedar_ember":{"eligible":true,"grants":[{"type":"weekly","resets_left":1,"ends_at":"bad"}]}}`, false, 0},
		{"missing eligibility", `{"cedar_ember":{}}`, false, 0},
		{"temporarily unavailable", `{"cedar_ember":{"eligible":false,"ineligible_reason":"unavailable"}}`, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := parseClaudeResetOffers([]byte(tc.body), now)
			if r.Known != tc.known || len(r.Grants) != tc.count {
				t.Fatalf("got %+v", r)
			}
			if tc.count == 2 && (r.Grants[0].Type != "fable" || r.Grants[0].Remaining != 2 || r.Grants[1].Type != "weekly" || r.Grants[1].Remaining != 1 || r.Grants[0].ExpiresAt == 0) {
				t.Fatalf("separation: %+v", r)
			}
		})
	}
}

func offerFiles(t *testing.T, now time.Time) (string, string) {
	t.Helper()
	home := t.TempDir()
	cfg, cred := filepath.Join(home, ".claude.json"), filepath.Join(home, ".credentials.json")
	if err := os.WriteFile(cfg, []byte(`{"oauthAccount":{"accountUuid":"account-a","emailAddress":"a@example.test"},"cachedUsageUtilization":{"utilization":{"cedar_ember":null}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{"accessToken": "fixture-only", "expiresAt": now.Add(time.Hour).UnixMilli()}})
	if err := os.WriteFile(cred, b, 0600); err != nil {
		t.Fatal(err)
	}
	return cfg, cred
}

func TestClaudeResetDedicatedRead(t *testing.T) {
	now := time.Now()
	cfg, cred := offerFiles(t, now)
	calls := 0
	client := fixtureOfferClient()
	client.Transport = offerTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != "GET" || req.URL.String() != claudeOffersURL || req.Header.Get("Authorization") != "Bearer fixture-only" {
			t.Fatal("wrong request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"cedar_ember":{"eligible":true,"grants":[{"type":"weekly","resets_left":3}]}}`)), Header: make(http.Header), Request: req}, nil
	})
	r := readClaudeOffersWithReader(context.Background(), fakeOfferResponseReader{client}, cfg, cred, "a@example.test", now)
	if !r.Known || len(r.Grants) != 1 || r.Grants[0].Remaining != 3 || calls != 1 {
		t.Fatalf("read: %+v, calls %d", r, calls)
	}
	encoded, _ := json.Marshal(r)
	if strings.Contains(string(encoded), "fixture-only") {
		t.Fatal("credential leaked")
	}
	// A wrong account must never reach the transport.
	r = readClaudeOffersWithReader(context.Background(), fakeOfferResponseReader{client}, cfg, cred, "b@example.test", now)
	if r.Known || calls != 1 {
		t.Fatal("wrong account was read")
	}
	// Expired tokens are left to Claude, never refreshed by this reader.
	r = readClaudeOffersWithReader(context.Background(), fakeOfferResponseReader{client}, cfg, cred, "a@example.test", now.Add(2*time.Hour))
	if r.Known || calls != 1 {
		t.Fatal("expired token used")
	}
}

func TestClaudeResetReadFailuresStayUnknown(t *testing.T) {
	now := time.Now()
	cfg, cred := offerFiles(t, now)
	for _, status := range []int{401, 403, 429, 500, 302} {
		calls := 0
		client := fixtureOfferClient()
		client.Transport = offerTransport(func(req *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"cedar_ember":null}`)), Header: http.Header{"Location": []string{"https://example.test/steal"}}, Request: req}, nil
		})
		r := readClaudeOffersWithReader(context.Background(), fakeOfferResponseReader{client}, cfg, cred, "a@example.test", now)
		if r.Known || calls != 1 {
			t.Fatalf("status %d: %+v calls %d", status, r, calls)
		}
	}
}

func TestClaudeResetCacheAloneIsUnknown(t *testing.T) {
	now := time.Now()
	cfg, _ := offerFiles(t, now)
	s := New(Options{Dir: t.TempDir(), Env: []string{"CLAUDE_CONFIG_DIR=" + filepath.Dir(cfg)}, Now: func() time.Time { return now }})
	defer s.Close()
	// A named account with no login must not inherit the default account's token.
	other := t.TempDir()
	b, _ := os.ReadFile(cfg)
	os.WriteFile(filepath.Join(other, ".claude.json"), b, 0600)
	r := (&scope{Service: s, home: HomeEnv(Claude, other)}).claudeFreeResets(context.Background(), "a@example.test")
	if r.Known {
		t.Fatal("cache or another account's login was accepted")
	}
}

func TestClaudeResetAccountChangesDuringRead(t *testing.T) {
	now := time.Now()
	cfg, cred := offerFiles(t, now)
	client := fixtureOfferClient()
	client.Transport = offerTransport(func(req *http.Request) (*http.Response, error) {
		if err := os.WriteFile(cfg, []byte(`{"oauthAccount":{"accountUuid":"account-b","emailAddress":"a@example.test"}}`), 0600); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"cedar_ember":null}`)), Header: make(http.Header), Request: req}, nil
	})
	if r := readClaudeOffersWithReader(context.Background(), fakeOfferResponseReader{client}, cfg, cred, "a@example.test", now); r.Known {
		t.Fatal("account changed but response was accepted")
	}
}
