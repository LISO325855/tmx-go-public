package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/peers"
)

// Joining a new machine to the owner's fleet (DESIGN.md "Python fleet interop": two Go machines
// had no way to get their first pair token; `tmx peers import-python` only moves tokens a Python
// install already had).
//
//  1. At machine A (already in the fleet) the owner, confirmed by the owner gate, asks for a
//     join code: one code at a time, valid 10 minutes, usable once, gone after five wrong tries.
//  2. At the new machine B the owner, confirmed there too, types A's https tailnet address and
//     the code. B's daemon calls A over https with the code and B's own name and address.
//  3. A checks the code, makes a fresh pair token, stores it, adds B to its "peers" and answers
//     the token to B's daemon, which stores it and adds A to its own "peers".
//
// The pair token travels once, daemon to daemon over https, and is never in a page, a log or a
// chat: both sides log and answer its fingerprint only. A join only ever ADDS a machine: a name
// or address already in the fleet, or a name that already holds a token, is refused on both
// sides and nothing of it is changed. The code is not the token and opens
// nothing but this one exchange. No agent can start any of it: steps 1 and 2 are the owner's,
// behind the owner gate; step 3 is unauthenticated by design (B has no credential yet), is
// served only through A's trusted https address, and does nothing without a live code.
// Production daemons publish the saved peers live.

const (
	joinCodeTTL   = 10 * time.Minute
	joinMaxTries  = 5
	joinCodeChars = 12
	// joinAlphabet has no 0/O, 1/I/L: the code is read on one screen and typed on another.
	joinAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"
	maxJoinReply = 16 << 10
)

// joinPeerURL validates a machine's address (https, a tailnet name, nothing after it) and
// joinClient calls the other machine. Variables only so tests can stand in a local server:
// production never changes them.
var (
	joinPeerURL = config.PeerURL
	joinClient  = &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil},
		// Never follow a redirect: it would carry the join code wherever it points.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
)

// joinState is the one live join code of this machine (its hash, never the code).
type joinState struct {
	mu       sync.Mutex
	hash     [sha256.Size]byte
	expires  time.Time
	tries    int
	live     bool
	bound    config.Peer
	window   time.Time
	attempts int
}

// Bound all public exchange attempts, including malformed/expired/replayed codes.
// The global window avoids trusting proxy-supplied client IPs or unbounded IP maps.
func (j *joinState) allowAttempt(now time.Time) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.window.IsZero() || !now.Before(j.window.Add(time.Minute)) {
		j.window, j.attempts = now, 0
	}
	j.attempts++
	return j.attempts <= 30
}

func normalizeJoinCode(s string) string {
	return strings.ToUpper(strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' {
			return -1
		}
		return r
	}, strings.TrimSpace(s)))
}

func newJoinCode() (string, error) {
	// Rejection sampling: a byte at or above the largest multiple of the alphabet's size
	// (248 = 8 x 31) is drawn again, so every letter is equally likely.
	limit := byte(256 - 256%len(joinAlphabet))
	out := make([]byte, 0, joinCodeChars+2)
	var b [1]byte
	for n := 0; n < joinCodeChars; {
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		if b[0] >= limit {
			continue
		}
		if n > 0 && n%4 == 0 {
			out = append(out, '-')
		}
		out = append(out, joinAlphabet[int(b[0])%len(joinAlphabet)])
		n++
	}
	return string(out), nil
}

// issue replaces any live code with a new one.
func (j *joinState) issue(now time.Time) (code string, expires time.Time, err error) {
	code, err = newJoinCode()
	if err != nil {
		return "", time.Time{}, err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.bound = config.Peer{}
	j.hash, j.expires, j.tries, j.live = sha256.Sum256([]byte(normalizeJoinCode(code))), now.Add(joinCodeTTL), 0, true
	return code, j.expires, nil
}

// check reports whether code is the live one. A wrong code counts; the fifth ends the code.
// It does not consume a right one: the caller does, once the rest of the request holds.
func (j *joinState) check(code string, now time.Time) bool {
	sum := sha256.Sum256([]byte(normalizeJoinCode(code)))
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.live || !now.Before(j.expires) {
		j.live = false
		return false
	}
	if subtle.ConstantTimeCompare(sum[:], j.hash[:]) != 1 {
		if j.tries++; j.tries >= joinMaxTries {
			j.live = false
		}
		return false
	}
	return true
}

// consume ends the live code if it is still this one (false: someone else used it first).
func (j *joinState) consume(code string) bool {
	sum := sha256.Sum256([]byte(normalizeJoinCode(code)))
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.live || subtle.ConstantTimeCompare(sum[:], j.hash[:]) != 1 {
		return false
	}
	j.live = false
	return true
}

func (s *Server) machineJoinRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/peers/import-python", s.ownerOnly(s.peerImportPython))
	mux.HandleFunc("POST /api/peers/invite", s.ownerOnly(s.peerInvite))
	mux.HandleFunc("POST /api/peers/join", s.ownerOnly(s.machineJoin))
	mux.HandleFunc("DELETE /api/peers/{name}", s.ownerOnly(s.peerRemove))
	mux.HandleFunc("POST /api/machines/join-code", s.ownerOnly(s.machineJoinCode))
	mux.HandleFunc("POST /api/machines/join", s.ownerOnly(s.machineJoin))
	mux.HandleFunc("POST /api/machines/join/accept", s.machineJoinAccept) // public: see publicJoinRoute
}

// publicJoinRoute: the one route of the exchange a machine without any credential calls. It
// answers nothing without a live join code.
func publicJoinRoute(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.Path == "/api/machines/join/accept"
}

// joinConfig loads this machine's config and says why it cannot take part, if it cannot.
func (s *Server) joinConfig() (config.Config, string) {
	c, err := config.Load(s.f.Paths())
	if err != nil {
		return c, "this machine's config.json could not be read"
	}
	if !c.Production() {
		return c, `machines join under the "production" profile: the trial stays local-only`
	}
	if !config.ValidHostName(strings.ToLower(c.Machine)) {
		return c, `this machine needs a fleet name first (config.json "machine": lowercase letters, digits, dashes)`
	}
	return c, ""
}

// machineJoinCode is POST /api/machines/join-code (the owner, confirmed): a new join code.
func (s *Server) machineJoinCode(w http.ResponseWriter, r *http.Request) {
	s.peerEditMu.Lock()
	defer s.peerEditMu.Unlock()
	if _, why := s.joinConfig(); why != "" {
		writeError(w, http.StatusConflict, why)
		return
	}
	if !s.RequireOwner(w, r, "let a new machine join your fleet with a one-time code", nil) {
		return
	}
	code, expires, err := s.join.issue(time.Now())
	if err != nil {
		writeErr(w, err)
		return
	}
	s.log.Info("machine join: a join code was issued", "expires", expires.Format(time.RFC3339))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"code": code, "expires_at": expires.UnixMilli(), "valid_minutes": int(joinCodeTTL / time.Minute)})
}

type joinAcceptRequest struct {
	Code string `json:"code"`
	Name string `json:"name"` // the joining machine's fleet name
	URL  string `json:"url"`  // its https tailnet address
}

type joinAcceptReply struct {
	Name  string `json:"name"`  // this machine's fleet name
	Token string `json:"token"` // the new pair token: daemon to daemon only
}

// inFleet reports whether name is already one of this machine's machines: listed in "peers",
// at any address, or holding a pair token. A join never touches such a machine - not its
// address and not its token: whoever holds a join code could otherwise take an existing
// machine's place. Joining it again means removing it (or rotating its token) first.
func inFleet(c config.Config, tokens *peers.Tokens, name string) bool {
	for _, p := range c.Peers {
		if strings.EqualFold(p.Name, name) {
			return true
		}
	}
	return tokens.For(name) != ""
}

// addressInFleet reports whether a machine at url is already listed.
func addressInFleet(c config.Config, url string) bool {
	for _, p := range c.Peers {
		if strings.EqualFold(p.URL, url) {
			return true
		}
	}
	return false
}

// listAndStore adds peer to this machine's "peers" and stores its pair token, as one step: the
// list is saved first, and if the token then cannot be stored the list is put back as it was.
// A machine is therefore never left holding a token without being listed (which would refuse
// every later join under that name), and a failure leaves nothing behind here.
func (s *Server) listAndStore(c config.Config, tokens *peers.Tokens, peer config.Peer, token string) (fingerprint string, err error) {
	before := append([]config.Peer(nil), c.Peers...)
	c.Peers = append(c.Peers, peer)
	if err := config.Save(s.f.Paths(), c); err != nil {
		return "", err
	}
	fp, err := storeJoinToken(tokens, peer.Name, token)
	if err != nil {
		c.Peers = before
		if undo := config.Save(s.f.Paths(), c); undo != nil {
			s.log.Error("machine join: the token was not stored and the machine could not be unlisted again; remove it from config.json \"peers\"", "machine", peer.Name, "err", undo)
		}
		return "", err
	}
	if s.peers != nil {
		s.peers.ReplacePeersLocked(c.Peers)
	}
	return fp, nil
}

// storeJoinToken stores a joined machine's token. A variable so a test can make it fail.
var storeJoinToken = func(tokens *peers.Tokens, name, token string) (string, error) {
	return tokens.StoreLocked(name, token, "join")
}

func (s *Server) pairTokens() *peers.Tokens {
	if s.peers != nil {
		return s.peers.Tokens()
	}
	return peers.NewTokens(s.f.Paths().PeersFile())
}

// machineJoinAccept is POST /api/machines/join/accept: the joining machine's daemon presents
// the code. Never from a browser. Every refusal before the code is checked says the same
// thing, so the route tells a stranger nothing about this machine.
func (s *Server) machineJoinAccept(w http.ResponseWriter, r *http.Request) {
	const refused = "no machine can join with that right now"
	if len(r.Header.Values("Origin")) > 0 {
		writeError(w, http.StatusForbidden, "machines only")
		return
	}
	// Only through this machine's trusted https address (tailscale serve), as device pairing:
	// never straight at the port, from this machine or another.
	if !s.trustedProxy(r) {
		writeError(w, http.StatusForbidden, "reach this machine at its trusted https address")
		return
	}
	if !s.join.allowAttempt(time.Now()) {
		writeError(w, http.StatusTooManyRequests, "too many join attempts; wait one minute")
		return
	}
	var req joinAcceptRequest
	if !decodeMax(w, r, &req, 4<<10) {
		return
	}
	s.peerEditMu.Lock()
	defer s.peerEditMu.Unlock()
	now := time.Now()
	c, why := s.joinConfig()
	if why != "" || !s.join.check(req.Code, now) {
		writeError(w, http.StatusForbidden, refused)
		return
	}
	// The code is right. What follows may still fail without ending it: a typo in the new
	// machine's own settings should not cost the owner a new code.
	name := strings.ToLower(strings.TrimSpace(req.Name))
	if !config.ValidHostName(name) || name == strings.ToLower(c.Machine) {
		writeError(w, http.StatusBadRequest, "the joining machine needs its own fleet name (lowercase letters, digits, dashes), different from this machine's")
		return
	}
	url, err := joinPeerURL(req.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "the joining machine's address must be its https tailnet name")
		return
	}
	s.join.mu.Lock()
	bound := s.join.bound
	s.join.mu.Unlock()
	if bound.Name != "" && (bound.Name != name || bound.URL != url) {
		writeError(w, http.StatusForbidden, refused)
		return
	}
	tokens := s.pairTokens()
	if inFleet(c, tokens, name) || addressInFleet(c, url) {
		writeError(w, http.StatusConflict, "a machine with that name or address is already in this fleet: remove it there first, then join again")
		return
	}
	if !s.join.check(req.Code, time.Now()) || !s.join.consume(req.Code) {
		writeError(w, http.StatusForbidden, refused)
		return
	}
	token := peers.NewPairToken()
	fp, err := s.listAndStore(c, tokens, config.Peer{Name: name, URL: url}, token)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.log.Info("machine join: a machine joined", "machine", name, "fingerprint", fp)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, joinAcceptReply{Name: strings.ToLower(c.Machine), Token: token})
}

// machineJoin is POST /api/machines/join (the owner at the NEW machine, confirmed):
// {url: the other machine's https address, code, own_url?}. own_url is this machine's own
// https address; left out, it is the first of api.trusted_hosts.
func (s *Server) machineJoin(w http.ResponseWriter, r *http.Request) {
	s.peerEditMu.Lock()
	defer s.peerEditMu.Unlock()
	var req struct {
		URL    string `json:"url"`
		Code   string `json:"code"`
		OwnURL string `json:"own_url"`
	}
	if !decode(w, r, &req) {
		return
	}
	c, why := s.joinConfig()
	if why != "" {
		writeError(w, http.StatusConflict, why)
		return
	}
	other, err := joinPeerURL(req.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "the other machine's address must be its https tailnet name, e.g. https://workstation.example.ts.net")
		return
	}
	if req.OwnURL == "" && len(c.API.TrustedHosts) > 0 {
		req.OwnURL = "https://" + c.API.TrustedHosts[0]
	}
	own, err := joinPeerURL(req.OwnURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, `this machine's own https tailnet name is needed (config.json api.trusted_hosts, or "own_url")`)
		return
	}
	if normalizeJoinCode(req.Code) == "" {
		writeError(w, http.StatusBadRequest, "type the join code the other machine shows")
		return
	}
	tokens := s.pairTokens()
	if addressInFleet(c, other) {
		writeError(w, http.StatusConflict, "the machine at that address is already in this machine's fleet: remove it here first, then join again")
		return
	}
	// Before any code is spent: the other machine's public health answer names it. If a machine
	// of that name is already here, stop now - the other side then lists nothing. This is a
	// hint only (the name that counts is the one its answer to the code carries, checked below);
	// a machine that does not answer its health does not answer the join either.
	hint, err := callJoinHealth(r.Context(), other)
	if err != nil {
		writeError(w, http.StatusBadGateway, "the other machine did not answer at "+other)
		return
	}
	if hint != "" && (inFleet(c, tokens, hint) || hint == strings.ToLower(c.Machine)) {
		writeError(w, http.StatusConflict, "the machine at that address calls itself "+hint+", a name already in this machine's fleet: nothing was changed. Remove it here first, then join again.")
		return
	}
	// The code is deliberately not part of what the gate remembers about this action.
	if !s.RequireOwner(w, r, "join this machine to your fleet through "+other, map[string]string{"url": other}) {
		return
	}
	reply, status, err := callJoinAccept(r.Context(), other, joinAcceptRequest{Code: req.Code, Name: strings.ToLower(c.Machine), URL: own})
	switch {
	case err != nil:
		writeError(w, http.StatusBadGateway, "the other machine did not answer at "+other)
		return
	case status == http.StatusForbidden:
		writeError(w, http.StatusForbidden, "the other machine did not take that code: it may be mistyped, used or older than 10 minutes. Make a new one there.")
		return
	case status != http.StatusOK:
		writeError(w, http.StatusBadGateway, "the other machine refused: "+clampText(reply.err, 200))
		return
	}
	name := strings.ToLower(strings.TrimSpace(reply.Name))
	if !config.ValidHostName(name) || name == strings.ToLower(c.Machine) {
		writeError(w, http.StatusBadGateway, "the other machine answered a name this machine cannot use")
		return
	}
	// The other machine has answered a name this machine already has (listed, or holding a
	// token): its token here is not replaced. The other side has now listed this machine with
	// a token nobody holds; the owner removes that entry there.
	if inFleet(c, tokens, name) {
		writeError(w, http.StatusConflict, "a machine called "+name+" is already in this machine's fleet: nothing was changed here. Remove it here first; and remove this machine's new entry on "+name+", which it just made.")
		return
	}
	fp, err := s.listAndStore(c, tokens, config.Peer{Name: name, URL: other}, reply.Token)
	if err != nil {
		writeError(w, http.StatusBadGateway, "the other machine's answer could not be kept here; nothing was changed on this machine. Remove this machine's new entry on "+name+", then join again with a new code.")
		return
	}
	s.log.Info("machine join: joined a fleet", "through", name, "fingerprint", fp)
	writeJSON(w, http.StatusOK, map[string]any{"machine": name, "fingerprint": fp,
		"next": "Pairing saved. Both production daemons see the peer without restarting."})
}

// callJoinHealth reads the other machine's fleet name from its public GET /api/health ("host").
// "" when it answers without a usable name; an error when it does not answer.
func callJoinHealth(ctx context.Context, base string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/health", nil)
	if err != nil {
		return "", err
	}
	res, err := joinClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxJoinReply))
	if err != nil {
		return "", err
	}
	var h struct {
		Host string `json:"host"`
	}
	if res.StatusCode != http.StatusOK || json.Unmarshal(raw, &h) != nil {
		return "", nil
	}
	if name := strings.ToLower(strings.TrimSpace(h.Host)); config.ValidHostName(name) {
		return name, nil
	}
	return "", nil
}

type joinAcceptAnswer struct {
	joinAcceptReply
	err string
}

// callJoinAccept posts the code to the other machine. Nothing of the reply is logged.
func callJoinAccept(ctx context.Context, base string, body joinAcceptRequest) (joinAcceptAnswer, int, error) {
	var out joinAcceptAnswer
	b, err := json.Marshal(body)
	if err != nil {
		return out, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/machines/join/accept", bytes.NewReader(b))
	if err != nil {
		return out, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := joinClient.Do(req)
	if err != nil {
		return out, 0, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxJoinReply))
	if err != nil {
		return out, res.StatusCode, err
	}
	if res.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		out.err = e.Error
		return out, res.StatusCode, nil
	}
	if err := json.Unmarshal(raw, &out.joinAcceptReply); err != nil {
		return out, http.StatusBadGateway, nil
	}
	return out, http.StatusOK, nil
}
