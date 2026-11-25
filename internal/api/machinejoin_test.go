package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/api"
	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/peers"
)

const alphaHost = "alpha.example.ts.net"

// trusted is a daemon reached at the address that stands behind its trusted https name.
type trusted struct {
	*apitest.Daemon
	URL string
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func production(t *testing.T, d *apitest.Daemon, machine string) {
	t.Helper()
	p := config.Paths{Home: d.Home}
	c, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	c.Profile, c.Machine = config.ProfileProduction, machine
	if err := config.Save(p, c); err != nil {
		t.Fatal(err)
	}
}

// joinPost posts JSON and returns the status and the whole body: the test must see everything a
// page would.
func joinPost(t *testing.T, base, path, token string, hdr map[string]string, body any) (int, string) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", base+path, bytes.NewReader(b))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(out)
}

// A new machine joins the owner's fleet: a one-time code at the machine that is in, typed at
// the new one. Both end up with the same pair token and each other in "peers"; the token is in
// no answer a page gets; only the owner can start either half; the code works once.
func TestMachineJoin(t *testing.T) {
	// What `tailscale serve` does in front of alpha: the request arrives from this machine, for
	// alpha's trusted name, marked https.
	proxied := map[string]string{"Host": alphaHost, "X-Forwarded-Proto": "https"}
	viaProxy := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.Host = alphaHost
		r.Header.Set("X-Forwarded-Proto", "https")
		return http.DefaultTransport.RoundTrip(r)
	})}
	t.Cleanup(api.SetJoinForTest(func(raw string) (string, error) { return strings.TrimRight(raw, "/"), nil }, viaProxy))
	a0, b := apitest.New(t), apitest.New(t)
	a := &trusted{Daemon: a0, URL: a0.WithTrustedHost(t, alphaHost)}
	tokenOf := func(d *apitest.Daemon, peer string) string {
		return peers.NewTokens(config.Paths{Home: d.Home}.PeersFile()).For(peer)
	}

	// The trial profile stays local-only: no code, no join.
	if got, _ := joinPost(t, a.URL, "/api/machines/join-code", a.OwnerToken, nil, nil); got != http.StatusConflict {
		t.Fatalf("a join code under the trial profile: %d", got)
	}
	production(t, a.Daemon, "alpha")
	if got, _ := joinPost(t, b.URL, "/api/machines/join", b.OwnerToken, nil, map[string]string{"url": a.URL, "code": "ABCD-EFGH-JKMN", "own_url": b.URL}); got != http.StatusConflict {
		t.Fatalf("joining from the trial profile: %d", got)
	}
	production(t, b, "beta")

	// Only the owner makes a code.
	for token, want := range map[string]int{"": http.StatusUnauthorized, a.AgentToken(t, "head"): http.StatusForbidden, a.AgentToken(t, mkWorker(t, a.Daemon)): http.StatusForbidden} {
		if got, _ := joinPost(t, a.URL, "/api/machines/join-code", token, nil, nil); got != want {
			t.Fatalf("join code as a non-owner: %d, want %d", got, want)
		}
	}
	var issued struct {
		Code      string `json:"code"`
		ExpiresAt int64  `json:"expires_at"`
	}
	status, body := joinPost(t, a.URL, "/api/machines/join-code", a.OwnerToken, nil, nil)
	if status != 200 || json.Unmarshal([]byte(body), &issued) != nil || len(issued.Code) != 14 || issued.ExpiresAt == 0 {
		t.Fatalf("join code: %d %s", status, body)
	}

	accept := func(hdr map[string]string, req map[string]string) (int, string) {
		all := map[string]string{}
		for k, v := range proxied {
			all[k] = v
		}
		for k, v := range hdr {
			all[k] = v
		}
		return joinPost(t, a.URL, "/api/machines/join/accept", "", all, req)
	}
	// Straight at the port, or under a name that is not this machine's trusted one, even the
	// right code opens nothing - and stays usable for the real machine.
	right := map[string]string{"code": issued.Code, "name": "beta", "url": b.URL}
	if got, _ := joinPost(t, a.URL, "/api/machines/join/accept", "", nil, right); got != http.StatusForbidden {
		t.Fatalf("the accept route straight at the port: %d", got)
	}
	if got, _ := joinPost(t, a.URL, "/api/machines/join/accept", "", map[string]string{"Host": "evil.example.ts.net", "X-Forwarded-Proto": "https"}, right); got != http.StatusForbidden {
		t.Fatalf("the accept route under another name: %d", got)
	}
	if got, _ := joinPost(t, a.URL, "/api/machines/join/accept", "", map[string]string{"Host": alphaHost, "X-Forwarded-Proto": "http"}, right); got != http.StatusForbidden {
		t.Fatalf("the accept route without https: %d", got)
	}
	// A wrong code, and a browser, get nothing - and learn nothing.
	if got, out := accept(nil, map[string]string{"code": "WRNG-WRNG-WRNG", "name": "beta", "url": b.URL}); got != http.StatusForbidden || strings.Contains(out, "alpha") || !strings.Contains(out, "no machine can join") {
		t.Fatalf("a wrong code: %d %s", got, out)
	}
	// A browser - a foreign page, or even this machine's own trusted page - is not a machine.
	for _, origin := range []string{"https://evil.example", "https://" + alphaHost} {
		if got, _ := accept(map[string]string{"Origin": origin}, map[string]string{"code": issued.Code, "name": "beta", "url": b.URL}); got != http.StatusForbidden {
			t.Fatalf("a browser at %s: %d", origin, got)
		}
	}
	// The right code with settings that cannot work is refused without using the code up.
	for _, bad := range []map[string]string{
		{"code": issued.Code, "name": "alpha", "url": b.URL},    // this machine's own name
		{"code": issued.Code, "name": "../etc", "url": b.URL},   // not a fleet name
		{"code": issued.Code, "name": "Beta Box", "url": b.URL}, // not a fleet name
	} {
		if got, out := accept(nil, bad); got != http.StatusBadRequest || strings.Contains(out, "token") {
			t.Fatalf("%v: %d %s", bad, got, out)
		}
	}

	// A name that already holds a token here (a machine already in the fleet) cannot be joined
	// again by whoever holds a code: its token stays what it was, and the code stays usable.
	held := peers.NewPairToken()
	if _, err := peers.NewTokens(config.Paths{Home: a.Home}.PeersFile()).Store("gamma", held, "test"); err != nil {
		t.Fatal(err)
	}
	if got, out := accept(nil, map[string]string{"code": issued.Code, "name": "gamma", "url": b.URL}); got != http.StatusConflict || strings.Contains(out, "token") {
		t.Fatalf("joining over a machine already in the fleet: %d %s", got, out)
	}
	if tokenOf(a.Daemon, "gamma") != held {
		t.Fatal("a join replaced the pair token of a machine already in the fleet")
	}

	// At the new machine: only its owner; then the join.
	join := map[string]string{"url": a.URL, "code": strings.ToLower(issued.Code), "own_url": b.URL}
	for token, want := range map[string]int{"": http.StatusUnauthorized, b.AgentToken(t, "head"): http.StatusForbidden} {
		if got, _ := joinPost(t, b.URL, "/api/machines/join", token, nil, join); got != want {
			t.Fatalf("join as a non-owner: %d, want %d", got, want)
		}
	}
	if got, out := joinPost(t, b.URL, "/api/machines/join", b.OwnerToken, nil, map[string]string{"url": a.URL, "code": "WRNG-WRNG-WRNG", "own_url": b.URL}); got != http.StatusForbidden {
		t.Fatalf("join with a wrong code: %d %s", got, out)
	}
	status, body = joinPost(t, b.URL, "/api/machines/join", b.OwnerToken, nil, join)
	var joined struct {
		Machine     string `json:"machine"`
		Fingerprint string `json:"fingerprint"`
		Next        string `json:"next"`
	}
	if status != 200 || json.Unmarshal([]byte(body), &joined) != nil || joined.Machine != "alpha" || joined.Fingerprint == "" || !strings.Contains(joined.Next, "without restarting") {
		t.Fatalf("join: %d %s", status, body)
	}

	// Both hold the same token; the answer the owner's page got does not carry it.
	ta, tb := tokenOf(a.Daemon, "beta"), tokenOf(b, "alpha")
	if ta == "" || ta != tb || peers.Fingerprint(tb) != joined.Fingerprint {
		t.Fatalf("pair tokens differ or are missing (a %d chars, b %d chars)", len(ta), len(tb))
	}
	if strings.Contains(body, ta) || strings.Contains(body, "token") {
		t.Fatalf("the owner's answer carries the token: %s", body)
	}
	ca, _ := config.Load(config.Paths{Home: a.Home})
	cb, _ := config.Load(config.Paths{Home: b.Home})
	if len(ca.Peers) != 1 || ca.Peers[0].Name != "beta" || ca.Peers[0].URL != b.URL || len(cb.Peers) != 1 || cb.Peers[0].Name != "alpha" || cb.Peers[0].URL != a.URL {
		t.Fatalf("peers after the join: a %+v, b %+v", ca.Peers, cb.Peers)
	}

	// The code worked once: a second machine cannot ride on it.
	if got, _ := accept(nil, map[string]string{"code": issued.Code, "name": "delta", "url": b.URL}); got != http.StatusForbidden {
		t.Fatalf("the code was accepted twice: %d", got)
	}
	if tokenOf(a.Daemon, "delta") != "" {
		t.Fatal("a second machine got a token from a used code")
	}

	// After the restart the answer asks for: beta, started with its saved peers, takes the
	// joined token from alpha on its peer routes - and no other token.
	pc := peers.New(cb, config.Paths{Home: b.Home}, nil)
	peerURL := b.WithPeers(t, pc)
	var roster struct {
		Host string `json:"host"`
	}
	if got := peerDo(t, peerURL, "GET", "/api/sessions", "alpha", ta, nil, nil, &roster); got != 200 || roster.Host != "beta" {
		t.Fatalf("alpha reads beta with the joined token: %d %+v", got, roster)
	}
	if got := peerDo(t, peerURL, "GET", "/api/sessions", "alpha", peers.NewPairToken(), nil, nil, nil); got != http.StatusUnauthorized {
		t.Fatalf("another token: %d", got)
	}
}

// The new machine's side of the same rule: if the machine it joins through answers a name that
// already holds a token here, that token is not replaced and nothing is listed.
func TestMachineJoinNeverReplacesATokenAtTheNewMachine(t *testing.T) {
	viaProxy := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.Host = alphaHost
		r.Header.Set("X-Forwarded-Proto", "https")
		return http.DefaultTransport.RoundTrip(r)
	})}
	t.Cleanup(api.SetJoinForTest(func(raw string) (string, error) { return strings.TrimRight(raw, "/"), nil }, viaProxy))
	a0, b := apitest.New(t), apitest.New(t)
	aURL := a0.WithTrustedHost(t, alphaHost)
	production(t, a0, "alpha")
	production(t, b, "beta")
	held := peers.NewPairToken()
	tb := peers.NewTokens(config.Paths{Home: b.Home}.PeersFile())
	if _, err := tb.Store("alpha", held, "test"); err != nil {
		t.Fatal(err)
	}
	var issued struct {
		Code string `json:"code"`
	}
	if status, body := joinPost(t, aURL, "/api/machines/join-code", a0.OwnerToken, nil, nil); status != 200 || json.Unmarshal([]byte(body), &issued) != nil {
		t.Fatalf("join code: %d", status)
	}
	status, body := joinPost(t, b.URL, "/api/machines/join", b.OwnerToken, nil, map[string]string{"url": aURL, "code": issued.Code, "own_url": b.URL})
	if status != http.StatusConflict || strings.Contains(body, held) {
		t.Fatalf("join over a name that holds a token: %d %s", status, body)
	}
	if got := peers.NewTokens(config.Paths{Home: b.Home}.PeersFile()).For("alpha"); got != held {
		t.Fatal("the join replaced a pair token the new machine already held")
	}
	if cb, err := config.Load(config.Paths{Home: b.Home}); err != nil || len(cb.Peers) != 0 {
		t.Fatalf("the refused join listed a machine: %+v %v", cb.Peers, err)
	}
}

// Before any code is spent the joining machine asks the other one its name (public health): if
// that name is already in its fleet it stops there, and the other machine lists nothing and
// keeps its code. A machine that does not answer its health is not asked to join.
func TestMachineJoinStopsBeforeSpendingTheCode(t *testing.T) {
	viaProxy := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.Host = alphaHost
		r.Header.Set("X-Forwarded-Proto", "https")
		return http.DefaultTransport.RoundTrip(r)
	})}
	t.Cleanup(api.SetJoinForTest(func(raw string) (string, error) { return strings.TrimRight(raw, "/"), nil }, viaProxy))
	a0, b := apitest.New(t), apitest.New(t)
	aURL := a0.WithTrustedHost(t, alphaHost)
	production(t, a0, "alpha")
	production(t, b, "beta")

	// The name alpha's health answers (the daemon's own name at its start).
	res, err := viaProxy.Get(aURL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	var health struct {
		Host string `json:"host"`
	}
	err = json.NewDecoder(res.Body).Decode(&health)
	res.Body.Close()
	if err != nil || health.Host == "" {
		t.Fatalf("health: %v %+v", err, health)
	}
	held := peers.NewPairToken()
	if _, err := peers.NewTokens(config.Paths{Home: b.Home}.PeersFile()).Store(health.Host, held, "test"); err != nil {
		t.Fatal(err)
	}
	var issued struct {
		Code string `json:"code"`
	}
	if status, body := joinPost(t, aURL, "/api/machines/join-code", a0.OwnerToken, nil, nil); status != 200 || json.Unmarshal([]byte(body), &issued) != nil {
		t.Fatalf("join code: %d", status)
	}
	join := map[string]string{"url": aURL, "code": issued.Code, "own_url": b.URL}
	if status, body := joinPost(t, b.URL, "/api/machines/join", b.OwnerToken, nil, join); status != http.StatusConflict || !strings.Contains(body, health.Host) {
		t.Fatalf("join when the other machine's name is already here: %d %s", status, body)
	}
	// Alpha was never asked: it lists nothing, holds no token for beta, and its code still works.
	ca, _ := config.Load(config.Paths{Home: a0.Home})
	if len(ca.Peers) != 0 || peers.NewTokens(config.Paths{Home: a0.Home}.PeersFile()).For("beta") != "" {
		t.Fatalf("the accepting machine kept something: %+v", ca.Peers)
	}
	if got := peers.NewTokens(config.Paths{Home: b.Home}.PeersFile()).For(health.Host); got != held {
		t.Fatal("the held token changed")
	}
	proxied := map[string]string{"Host": alphaHost, "X-Forwarded-Proto": "https"}
	if got, out := joinPost(t, aURL, "/api/machines/join/accept", "", proxied, map[string]string{"code": issued.Code, "name": "beta", "url": b.URL}); got != 200 {
		t.Fatalf("the code was spent by a join that stopped early: %d %s", got, out)
	}

	// A machine that does not answer: 502, and nothing changes here.
	if status, _ := joinPost(t, b.URL, "/api/machines/join", b.OwnerToken, nil, map[string]string{"url": "http://127.0.0.1:1", "code": "ABCD-EFGH-JKMN", "own_url": b.URL}); status != http.StatusBadGateway {
		t.Fatalf("join through a machine that does not answer: %d", status)
	}
}

// Listing a machine and storing its token are one step: if the token cannot be stored, the
// machine is not left listed, holds no token, the code is used up as any consumed code is, and
// a later join under the same name is not refused as "already in the fleet".
func TestMachineJoinLeavesNothingBehindWhenTheTokenCannotBeStored(t *testing.T) {
	a0 := apitest.New(t)
	aURL := a0.WithTrustedHost(t, alphaHost)
	production(t, a0, "alpha")
	proxied := map[string]string{"Host": alphaHost, "X-Forwarded-Proto": "https"}
	code := func() string {
		var issued struct {
			Code string `json:"code"`
		}
		if status, body := joinPost(t, aURL, "/api/machines/join-code", a0.OwnerToken, nil, nil); status != 200 || json.Unmarshal([]byte(body), &issued) != nil {
			t.Fatalf("join code: %d", status)
		}
		return issued.Code
	}
	state := func() (int, string) {
		c, err := config.Load(config.Paths{Home: a0.Home})
		if err != nil {
			t.Fatal(err)
		}
		return len(c.Peers), peers.NewTokens(config.Paths{Home: a0.Home}.PeersFile()).For("beta")
	}
	req := map[string]string{"name": "beta", "url": "https://beta.example.ts.net"}

	restore := api.FailJoinTokenStoreForTest()
	req["code"] = code()
	got, out := joinPost(t, aURL, "/api/machines/join/accept", "", proxied, req)
	restore()
	if got < 500 || strings.Contains(out, "token") {
		t.Fatalf("a failed store: %d %s", got, out)
	}
	if n, tok := state(); n != 0 || tok != "" {
		t.Fatalf("left behind after a failed store: %d listed, token %q", n, tok)
	}
	// The same machine joins with a new code as if nothing had happened.
	req["code"] = code()
	if got, out := joinPost(t, aURL, "/api/machines/join/accept", "", proxied, req); got != 200 {
		t.Fatalf("the retry: %d %s", got, out)
	}
	if n, tok := state(); n != 1 || tok == "" {
		t.Fatalf("after the retry: %d listed, token set %v", n, tok != "")
	}
}
