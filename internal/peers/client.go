package peers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// Timings (Python: sessions 2.5 s, mail 8 s, pair 10 s).
const (
	pollEvery      = 10 * time.Second
	sessionTimeout = 3 * time.Second
	sendTimeout    = 8 * time.Second
	maxReply       = 4 << 20
)

// Headers of the Python pair-token API.
const (
	HeaderToken         = "X-TMX-Token"
	HeaderFrom          = "X-TMX-From"
	HeaderOnBehalf      = "X-TMX-On-Behalf"
	HeaderVerifiedAgent = "X-TMX-Verified-Agent"
)

// Session is one agent on a peer, as its /api/sessions lists it (a subset; display only).
type Session struct {
	ID             string   `json:"id,omitempty"`
	Rooms          []string `json:"rooms"`
	Tier           string   `json:"tier,omitempty"`
	Cwd            string   `json:"cwd,omitempty"`
	Adapter        string   `json:"adapter,omitempty"`
	ConversationID string   `json:"conversation_id,omitempty"`
	Name           string   `json:"name"`
	Host           string   `json:"host"`
	State          string   `json:"state"`
	Title          string   `json:"title,omitempty"`
	Project        string   `json:"project,omitempty"`
	Role           string   `json:"role,omitempty"`
	StateSeconds   int64    `json:"state_seconds,omitempty"`
	QueuedMessages int      `json:"queued_messages,omitempty"`
}

// DaemonGo is what a Go daemon answers as "daemon" in GET /api/sessions. A Python daemon has no
// such field. It tells the owner's window which launch body that machine takes (Go's
// fleet.LaunchRequest or Python's), nothing more: it grants nothing.
const DaemonGo = "go"

// System is a machine's own reading, as GET /api/sessions carries it under "system". The field
// names are the Python daemons' (sysmon.snapshot), so a Go and a Python machine read each other
// the same way; disk_free_mb and disk_total_mb are Go's additions (a Python peer leaves them
// out). Every field is optional: a machine that has not measured something yet sends null or
// nothing, never a made-up zero.
type System struct {
	CPUPercent   *float64 `json:"cpu_percent,omitempty"` // all cores, 0-100
	CPUs         int      `json:"cpus,omitempty"`
	Memory       *Memory  `json:"memory,omitempty"`
	DiskWriteMBs *float64 `json:"disk_write_mb_s,omitempty"` // every program
	DiskFreeMB   *int64   `json:"disk_free_mb,omitempty"`    // the workspace's volume
	DiskTotalMB  *int64   `json:"disk_total_mb,omitempty"`
	IOOverloaded bool     `json:"io_overloaded,omitempty"` // the disk guard holds work back now
}

// Memory is System's memory reading (MiB).
type Memory struct {
	TotalMB     int64 `json:"total_mb"`
	AvailableMB int64 `json:"available_mb"`
}

// sane returns s with every number outside what a machine can report dropped: a peer's reading
// is display data, never trusted to be well-formed. The result shares nothing with s; nil when
// nothing usable is left.
func (s *System) sane() *System {
	if s == nil {
		return nil
	}
	const maxMB = 1 << 40 // 2^60 bytes: beyond any machine or volume
	out := System{IOOverloaded: s.IOOverloaded}
	if v := s.CPUPercent; v != nil && *v >= 0 && *v <= 100 {
		c := *v
		out.CPUPercent = &c
	}
	if s.CPUs > 0 && s.CPUs <= 4096 {
		out.CPUs = s.CPUs
	}
	if m := s.Memory; m != nil && m.TotalMB > 0 && m.TotalMB <= maxMB && m.AvailableMB >= 0 && m.AvailableMB <= m.TotalMB {
		out.Memory = &Memory{TotalMB: m.TotalMB, AvailableMB: m.AvailableMB}
	}
	if v := s.DiskWriteMBs; v != nil && *v >= 0 && *v <= 1<<20 {
		w := *v
		out.DiskWriteMBs = &w
	}
	if f, t := s.DiskFreeMB, s.DiskTotalMB; f != nil && t != nil && *t > 0 && *t <= maxMB && *f >= 0 && *f <= *t {
		free, total := *f, *t
		out.DiskFreeMB, out.DiskTotalMB = &free, &total
	}
	if out == (System{}) {
		return nil
	}
	return &out
}

// Status is what this daemon last saw of a peer.
type Status struct {
	Supervisor *SupervisorRef `json:"supervisor"`
	Name       string         `json:"name"`
	URL        string         `json:"url"`
	OK         bool           `json:"ok"`
	Error      string         `json:"error,omitempty"`
	Commit     string         `json:"commit,omitempty"`
	Version    string         `json:"version,omitempty"` // the peer's tmx version, when it says
	Daemon     string         `json:"daemon,omitempty"`  // DaemonGo, or "" for a Python daemon (it does not say)
	System     *System        `json:"system,omitempty"`  // its last machine reading (stale when !OK)
	Paired     bool           `json:"paired"`
	Seen       int64          `json:"seen,omitempty"` // Unix ms of the last good reading
	Sessions   []Session      `json:"sessions"`       // the last good reading (stale when !OK)
}

// SupervisorRef matches Python's roster wire object (or null when absent).
type SupervisorRef struct {
	Name string `json:"name"`
	Host string `json:"host"`
}

// Client talks to the configured peers. It implements fleet.PeerSender.
type Client struct {
	machine string
	peers   map[string]config.Peer
	peersMu sync.RWMutex
	tokens  *Tokens
	http    *http.Client
	log     *slog.Logger

	mu     sync.Mutex
	status map[string]Status

	stateMu    sync.Mutex
	fleetState FleetState // fleetstate.go: the last state block read from a Python peer
}

// New returns a Client for cfg's peers (none outside the production profile).
func New(cfg config.Config, p config.Paths, log *slog.Logger) *Client {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	c := &Client{
		machine: strings.ToLower(cfg.Machine),
		peers:   map[string]config.Peer{},
		tokens:  NewTokens(p.PeersFile()),
		// Never follow a redirect: it would resend the pair token wherever it points.
		http:   &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		log:    log,
		status: map[string]Status{},
	}
	if cfg.Production() {
		for _, peer := range cfg.Peers {
			c.peers[peer.Name] = peer
			c.status[peer.Name] = Status{Name: peer.Name, URL: peer.URL, Sessions: []Session{}}
		}
	}
	return c
}

// SetTransport replaces the HTTP transport (tests: a TLS test server's).
func (c *Client) SetTransport(rt http.RoundTripper) { c.http.Transport = rt }

// Tokens returns the pair-token store (the API authenticates peers with it).
func (c *Client) Tokens() *Tokens { return c.tokens }

// Machine returns this machine's name.
func (c *Client) Machine() string { return c.machine }

// Known reports whether host is a configured peer (fleet.PeerSender).
func (c *Client) Known(host string) bool {
	_, ok := c.lookup(host)
	return ok
}

// Statuses returns the last reading of every peer, sorted by name.
func (c *Client) Statuses() []Status {
	c.mu.Lock()
	out := make([]Status, 0, len(c.status))
	for _, s := range c.status {
		s.Sessions = append([]Session{}, s.Sessions...)
		for i := range s.Sessions {
			s.Sessions[i].Rooms = cloneRooms(s.Sessions[i].Rooms)
		}
		if s.Supervisor != nil {
			v := *s.Supervisor
			s.Supervisor = &v
		}
		s.System = s.System.sane() // a copy: the caller never shares the poller's pointers
		out = append(out, s)
	}
	c.mu.Unlock()
	for i := range out {
		out[i].Paired = c.tokens.For(out[i].Name) != ""
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Refused is a peer's answered refusal: never retried.
type Refused struct {
	Status int
	Msg    string
}

func (e Refused) Error() string { return fmt.Sprintf("refused (HTTP %d): %s", e.Status, e.Msg) }

// do sends one request with the peer's pair token (the only credential: the fleet token is
// retired fleet-wide) and decodes a JSON answer into out. Unreachable, 5xx and unreadable
// answers wrap fleet.ErrPeerUnreachable; 3xx/4xx are Refused.
func (c *Client) do(ctx context.Context, peer config.Peer, method, path string, body, out any, timeout time.Duration) (int, error) {
	return c.doWithToken(ctx, peer, method, path, body, out, timeout, c.tokens.For(peer.Name))
}

func (c *Client) doWithToken(ctx context.Context, peer config.Peer, method, path string, body, out any, timeout time.Duration, tok string) (int, error) {
	return c.doAttested(ctx, peer, method, path, body, out, timeout, tok, "")
}

// Only Send supplies an attested local agent; arbitrary remote relay bodies cannot.
func (c *Client) doAttested(ctx context.Context, peer config.Peer, method, path string, body, out any, timeout time.Duration, tok, verifiedAgent string) (int, error) {
	if tok == "" {
		return 0, Refused{0, "not paired with " + peer.Name + " (tmx peers import-python)"}
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, peer.URL+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set(HeaderToken, tok)
	req.Header.Set(HeaderFrom, c.machine)
	req.Header.Set(HeaderOnBehalf, "daemon")
	if verifiedAgent != "" {
		req.Header.Set(HeaderVerifiedAgent, verifiedAgent)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", fleet.ErrPeerUnreachable, err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, maxReply))
	if err != nil {
		return res.StatusCode, fmt.Errorf("%w: %v", fleet.ErrPeerUnreachable, err)
	}
	if res.StatusCode >= 500 {
		return res.StatusCode, fmt.Errorf("%w: HTTP %d", fleet.ErrPeerUnreachable, res.StatusCode)
	}
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		return res.StatusCode, Refused{res.StatusCode, clean(e.Error, 300)}
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			return res.StatusCode, fmt.Errorf("%w: not JSON: %v", fleet.ErrPeerUnreachable, err)
		}
	}
	return res.StatusCode, nil
}

// Send posts one message to an agent on host (fleet.PeerSender). The peer must answer with
// the same ID. The Python daemon answers 202 when it quarantined the message (this machine is
// not attested there yet): a refusal the owner fixes on that machine.
func (c *Client) Send(host string, m fleet.PeerOutbound) error {
	peer, ok := c.lookup(host)
	if !ok {
		return Refused{404, "no peer machine " + host}
	}
	var res struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	verified := ""
	if m.VerifiedSender == m.Sender && model.ValidName(m.Sender) && !fleet.ReservedAgentName(m.Sender) && m.SenderHost == c.machine {
		verified = m.VerifiedSender
	}
	code, err := c.doAttested(context.Background(), peer, http.MethodPost, "/api/messages/incoming", m, &res, sendTimeout, c.tokens.For(peer.Name), verified)
	if err != nil {
		return err
	}
	if code == http.StatusAccepted || res.ID != m.ID {
		return Refused{code, fmt.Sprintf("%s held the message (state %q) instead of delivering it: attest %s there (external_events.attested_peers)", peer.Name, clean(res.State, 40), c.machine)}
	}
	return nil
}

// Run reads every peer's agents every pollEvery until ctx ends, even when
// initially empty so live additions are discovered. Callers must cancel ctx.
func (c *Client) Run(ctx context.Context) {
	poll := time.NewTicker(pollEvery)
	defer poll.Stop()
	c.PollAll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			c.PollAll(ctx)
		}
	}
}

// PollAll reads every peer's /api/sessions once.
func (c *Client) PollAll(ctx context.Context) {
	var wg sync.WaitGroup
	for _, peer := range c.peerSnapshot() {
		wg.Add(1)
		go func(peer config.Peer) {
			defer wg.Done()
			c.poll(ctx, peer)
		}(peer)
	}
	wg.Wait()
}

func (c *Client) poll(ctx context.Context, peer config.Peer) {
	var res struct {
		Supervisor json.RawMessage `json:"supervisor"`
		Host       string          `json:"host"`
		Commit     string          `json:"commit"`
		Version    string          `json:"version"`
		Daemon     string          `json:"daemon"`
		System     json.RawMessage `json:"system"`
		Sessions   []Session       `json:"sessions"`
	}
	_, err := c.do(ctx, peer, http.MethodGet, "/api/sessions", nil, &res, sessionTimeout)
	if err == nil && !strings.EqualFold(res.Host, peer.Name) {
		err = fmt.Errorf("answered as %q, not %q", clean(res.Host, 40), peer.Name)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	st, exists := c.status[peer.Name]
	if !exists || st.URL != peer.URL {
		return
	}
	if err != nil {
		st.OK, st.Error = false, clean(err.Error(), 300)
		c.status[peer.Name] = st
		return
	}
	st.OK, st.Error, st.Commit, st.Seen = true, "", clean(res.Commit, 64), time.Now().UnixMilli()
	st.Version = clean(res.Version, 64)
	st.Daemon = ""
	if res.Daemon == DaemonGo {
		st.Daemon = DaemonGo
	}
	// The reading is optional and its shape is the peer's own: one that does not parse (an
	// older daemon, a field of another type) only leaves the machine without numbers.
	var sys System
	st.System = nil
	if len(res.System) > 0 && json.Unmarshal(res.System, &sys) == nil {
		st.System = sys.sane()
	}
	st.Supervisor = nil
	var supervisor SupervisorRef
	if json.Unmarshal(res.Supervisor, &supervisor) != nil {
		// Older Go daemons emitted a string. Accept it while upgrading the fleet.
		var legacy string
		if json.Unmarshal(res.Supervisor, &legacy) == nil {
			supervisor.Name, supervisor.Host, _ = strings.Cut(legacy, "@")
		}
	}
	if supervisor.Name != "" {
		supervisor.Name, supervisor.Host = clean(supervisor.Name, 40), clean(supervisor.Host, 80)
		if supervisor.Host == "" {
			supervisor.Host = peer.Name
		}
		st.Supervisor = &supervisor
	}
	st.Sessions = []Session{}
	for i, s := range res.Sessions {
		if i == 500 {
			break
		}
		if !model.ValidName(s.Name) {
			continue
		}
		s.Host = peer.Name // a peer lists its own agents only
		// A peer's opaque local ID may collide with one on this machine. Roster
		// selection must always route back to the authenticated peer by name.
		s.ID = s.Name + "@" + peer.Name
		s.Rooms = cloneRooms(s.Rooms)
		s.State, s.Title, s.Project, s.Role = clean(s.State, 40), clean(s.Title, 200), clean(s.Project, 80), clean(s.Role, 80)
		s.Tier, s.Cwd, s.Adapter, s.ConversationID = clean(s.Tier, 40), clean(s.Cwd, 4096), clean(s.Adapter, 40), clean(s.ConversationID, 256)
		st.Sessions = append(st.Sessions, s)
	}
	c.status[peer.Name] = st
}

func cloneRooms(rooms []string) []string {
	out := make([]string, 0, min(len(rooms), 100))
	for _, room := range rooms {
		if len(out) == 100 {
			break
		}
		if len(room) <= 512 {
			out = append(out, room)
		}
	}
	return out
}

// clean clamps a peer's string and drops control and format characters: it is display text.
func clean(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
	if r := []rune(s); len(r) > n {
		s = string(r[:n])
	}
	return s
}

// Names lists the configured peers, sorted.
func (c *Client) Names() []string {
	c.peersMu.RLock()
	defer c.peersMu.RUnlock()
	out := make([]string, 0, len(c.peers))
	for n := range c.peers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Call sends one request to a configured peer with its pair token (the owner PIN sync).
func (c *Client) Call(ctx context.Context, host, method, path string, body, out any) error {
	peer, ok := c.lookup(host)
	if !ok {
		return Refused{0, "unknown peer " + clean(host, 40)}
	}
	_, err := c.do(ctx, peer, method, path, body, out, sendTimeout)
	return err
}

// RotateWith generates a fresh pair token and pushes it to host, which must sort after this
// machine's own name (DESIGN.md "Python fleet interop": the lower-sorting name generates and
// rotates; host's own POST /api/peers/pair-token enforces the same rule from its side, so this
// is the one half of the rule Go could not do until now - go-shells-s, 4 Nov, bc #247). The new
// token is stored locally (Tokens().Store, keeping the old one valid for the usual overlap)
// ONLY after host's own fingerprint confirms it received exactly that token: a mismatch or
// network failure leaves the old token the one still in use on both sides, never a token only
// one side believes is current. Returns the new token's fingerprint.
func (c *Client) RotateWith(ctx context.Context, host string) (string, error) {
	edit := MutationMutex(c.tokens.path)
	edit.Lock()
	peer, ok := c.lookup(host)
	original := c.tokens.For(peer.Name)
	generation := c.tokens.Generation(peer.Name)
	edit.Unlock()
	if !ok {
		return "", Refused{0, "unknown peer " + clean(host, 40)}
	}
	if !(c.machine < peer.Name) {
		return "", Refused{0, peer.Name + " generates the pair token it shares with " + c.machine + ", not the other way round"}
	}
	fresh := NewPairToken()
	var res struct {
		Fingerprint string `json:"fingerprint"`
	}
	if _, err := c.doWithToken(ctx, peer, http.MethodPost, "/api/peers/pair-token", map[string]string{"token": fresh}, &res, sendTimeout, original); err != nil {
		return "", err
	}
	want := Fingerprint(fresh)
	if res.Fingerprint != want {
		return "", Refused{0, peer.Name + " confirmed a different token than the one sent: nothing stored locally"}
	}
	edit.Lock()
	defer edit.Unlock()
	current, known := c.lookup(peer.Name)
	if !known || current != peer {
		return "", ErrStalePair
	}
	fp, err := c.tokens.StoreIfCurrentLocked(peer.Name, fresh, "rotated-by-"+c.machine, original, generation)
	if err == ErrStalePair {
		return "", err
	}
	if err != nil {
		// peer.Name already has the new token (it just confirmed storing it); saving our own
		// copy failed (a disk error, ctx cancelled after the peer replied). The two sides now
		// disagree, which only keeps working until the old token's rotationOverlap runs out -
		// rotate again before then (go-reviewer-o4, 4 Nov).
		c.log.Error("p2p: rotated with a peer but failed to save our own copy of the new token - rotate again before the old one's overlap ends", "peer", peer.Name, "err", err)
		return "", err
	}
	return fp, nil
}
