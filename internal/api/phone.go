package api

// Connect a phone (docs/PHONE-PAIRING.md): one button at the computer, then a QR code the
// phone's camera opens. Ported from Mustneer's v0.1.20 phone.go onto our pairing (the HTTPS
// address, a one-time code and the owner PIN; cookie device sessions, devices.go).
//
// The way that works at home AND away is Tailscale. tmx runs the Tailscale CLI itself: it
// checks the node is signed in, serves the daemon's loopback port over HTTPS on the tailnet
// (`tailscale serve`), adds that name to api.trusted_hosts (behind the owner gate) and shows
// https://<name>/#pair=<code>. When something is missing it answers which step (install,
// start, login, magicdns, enable, …), so the dialog can guide the owner through it. A code is
// only shown for a name tmx itself got an answer through (selfCheck).
//
// The same Wi-Fi way (plain http, off by default) is phonelan.go.
//
// Every route here is the owner's at the computer: a paired phone, an agent or Companion
// cannot make codes or change how the machine is reached.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/gate"
	"github.com/blackcode-ch/tmx-go/internal/tailscale"
)

// PhoneAddress is one of this machine's private IPv4 addresses a phone on the same network
// can reach, best first.
type PhoneAddress struct {
	IP        string `json:"ip"`
	Interface string `json:"interface"`
}

// PhoneStatus is GET /api/phone: what each way needs.
type PhoneStatus struct {
	Port      int             `json:"port"`
	PINSet    bool            `json:"pin_set"` // the owner PIN the phone will ask for exists
	Wifi      WifiStatus      `json:"wifi"`
	Tailscale TailscaleStatus `json:"tailscale"`
	// Best is the way a code should use now (bestWay).
	Best string `json:"best"`
}

// WifiStatus: the same Wi-Fi fallback and the addresses a phone could use.
type WifiStatus struct {
	On        bool           `json:"on"` // the fallback is switched on for this install
	Addresses []PhoneAddress `json:"addresses"`
}

// TailscaleStatus: how far the Tailscale way is set up.
type TailscaleStatus struct {
	Installed bool   `json:"installed"`
	State     string `json:"state,omitempty"` // tailscale's BackendState ("Running", "NeedsLogin", …); "" = not reachable
	Host      string `json:"host,omitempty"`  // this machine's tailnet name
	Serving   bool   `json:"serving"`         // tailscale serve forwards an HTTPS port to tmx
	Trusted   bool   `json:"trusted"`         // and that name is a trusted host
	Ready     bool   `json:"ready"`           // all of it, and tmx answered through it just now
	URL       string `json:"url,omitempty"`   // https://<host>[:port] once serving
	Step      string `json:"step,omitempty"`  // the first thing still to do (see phoneProblem)
	Error     string `json:"error,omitempty"`
	Link      string `json:"link,omitempty"` // where that step is done
	// PhoneOnline: a phone or tablet is online on the tailnet (it can open the tailnet name).
	PhoneOnline bool `json:"phone_online"`
}

// bestWay is the way a code uses. Tailscale whenever it works (it is the only way with
// HTTPS, so the only one with dictation, notifications and the fingerprint lock); the same
// Wi-Fi only when the owner switched it on and Tailscale is not ready or no phone is on it.
func bestWay(st PhoneStatus) string {
	if st.Wifi.On && len(st.Wifi.Addresses) > 0 && !(st.Tailscale.Ready && st.Tailscale.PhoneOnline) {
		return "wifi"
	}
	return "tailscale"
}

// Pairing is the code POST /api/phone/connect shows.
type Pairing struct {
	Code      string `json:"code"`
	Host      string `json:"host"`       // what the phone reaches tmx through
	URL       string `json:"url"`        // what the QR code holds: https://<host>/#pair=<code>
	ExpiresAt int64  `json:"expires_at"` // Unix ms
}

// PhoneConnection is POST /api/phone/connect's answer.
type PhoneConnection struct {
	Via     string  `json:"via"` // "tailscale" or "wifi"
	Pairing Pairing `json:"pairing"`
}

// phoneProblem is a 409 telling the UI which setup step is missing.
type phoneProblem struct {
	Error  string `json:"error"`
	Step   string `json:"step"`             // pin, install, start, login, magicdns, enable, denied, failed, busy, checking, network, off
	Link   string `json:"link,omitempty"`   // where to fix it
	Output string `json:"output,omitempty"` // what tailscale said
}

// servePorts are the HTTPS ports tmx may ask Tailscale to serve on, in order: 443 unless the
// owner serves something else there.
var servePorts = []int{443, 8443, 10000}

// SetTailscale replaces how the Tailscale CLI is found (tests: a fake runner).
func (s *Server) SetTailscale(find func() *tailscale.Client) { s.tsFind = find }

// SetTailscaleProbe replaces how tmx checks it answers through its tailnet name (tests).
func (s *Server) SetTailscaleProbe(fn func(ctx context.Context, url string) error) { s.tsProbe = fn }

func (s *Server) tsClient() *tailscale.Client {
	if s.tsFind != nil {
		return s.tsFind()
	}
	return tailscale.Find()
}

func (s *Server) phoneRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/phone", s.atComputer(s.getPhone))
	mux.HandleFunc("POST /api/phone/connect", s.atComputer(s.connectPhone))
	mux.HandleFunc("DELETE /api/phone/connect", s.atComputer(s.cancelPhoneCode))
	mux.HandleFunc("POST /api/phone/tailscale/signin", s.atComputer(s.tailscaleSignIn))
	s.phoneLANRoutes(mux) // phonelan.go
}

// atComputer wraps a handler only the owner at this machine may call: the owner's own token
// on a direct loopback request. Not a paired device (it comes through the proxy), not an
// agent, not Companion.
func (s *Server) atComputer(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := caller(r)
		if !ok || !c.Owner || c.Companion || c.AgentID != "" || !localRequest(r) {
			writeError(w, http.StatusForbidden, "do this in tmx on the computer itself")
			return
		}
		h(w, r)
	}
}

// TrustedHosts are the HTTPS names set now.
func (s *Server) TrustedHosts() []string {
	s.trustedMu.RLock()
	defer s.trustedMu.RUnlock()
	return slices.Clone(s.trustedHosts)
}

func (s *Server) trusts(name string) bool {
	if !config.ValidTrustedHost(name) {
		return false
	}
	name = config.NormalizeTrustedHost(name)
	for _, h := range s.TrustedHosts() {
		if config.ValidTrustedHost(h) && config.NormalizeTrustedHost(h) == name {
			return true
		}
	}
	return false
}

// addTrustedHost saves one more trusted host (the caller has the owner's confirmation) and
// applies it at once.
func (s *Server) addTrustedHost(name string) error {
	if !config.ValidTrustedHost(name) {
		return fmt.Errorf("%q is not a valid HTTPS name", name)
	}
	name = config.NormalizeTrustedHost(name)
	if s.trusts(name) {
		return nil
	}
	if s.f != nil {
		c, err := config.Load(s.f.Paths())
		if err != nil {
			return err
		}
		if !slices.Contains(c.API.TrustedHosts, name) {
			c.API.TrustedHosts = append(c.API.TrustedHosts, name)
		}
		if err := config.Save(s.f.Paths(), c); err != nil {
			return err
		}
	}
	s.trustedMu.Lock()
	s.trustedHosts = append(s.trustedHosts, name)
	s.trustedMu.Unlock()
	s.log.Info("phone connect: trusted host added", "host", name)
	return nil
}

// TailscaleSignIn is POST /api/phone/tailscale/signin's answer: the app was opened, or the
// page to sign in on (the UI opens it); neither = nothing to do.
type TailscaleSignIn struct {
	Opened bool   `json:"opened,omitempty"`
	Link   string `json:"link,omitempty"`
}

// tailscaleSignIn: the guided setup's "Sign in" button. Not running: open the app (macOS);
// signed out: `tailscale login`, answering the sign-in page it prints.
func (s *Server) tailscaleSignIn(w http.ResponseWriter, r *http.Request) {
	set := s.tailscaleSetup(r.Context())
	if set.problem == nil {
		writeJSON(w, http.StatusOK, TailscaleSignIn{})
		return
	}
	switch set.problem.Step {
	case "install":
		writeJSON(w, http.StatusConflict, set.problem)
		return
	case "start":
		opened, err := set.ts.OpenApp(r.Context())
		if err != nil {
			writeJSON(w, http.StatusConflict, phoneProblem{Step: "start", Error: "Open the Tailscale app on this computer", Output: err.Error()})
			return
		}
		if opened {
			writeJSON(w, http.StatusOK, TailscaleSignIn{Opened: true})
			return
		}
		fallthrough
	case "login":
		link, err := set.ts.Login(r.Context())
		if err != nil {
			writeJSON(w, http.StatusConflict, phoneProblem{Step: set.problem.Step, Error: "Open the Tailscale app on this computer and sign in", Output: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, TailscaleSignIn{Link: link})
		return
	}
	writeJSON(w, http.StatusOK, TailscaleSignIn{})
}

func (s *Server) getPhone(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.phoneStatus(r.Context(), r.URL.Query().Get("setup") == "1"))
}

func (s *Server) phoneStatus(ctx context.Context, setup bool) PhoneStatus {
	st := PhoneStatus{
		Port:      s.port,
		PINSet:    s.pin != nil && s.pin.IsSet(),
		Wifi:      s.wifiStatus(),
		Tailscale: s.tailscaleStatus(ctx, setup),
	}
	st.Best = bestWay(st)
	return st
}

// tsSetup is where the Tailscale way stands: the problem (nil = none), the client, the name
// and the HTTPS port tmx is (or would be) served on.
type tsSetup struct {
	problem *phoneProblem
	ts      *tailscale.Client
	host    string
	port    int  // the HTTPS port tmx is served on, or the free one it would use
	serving bool // already served there
	phone   bool // a phone is online on the tailnet
}

// tsCheckCache remembers the last self-check through the tailnet name: a success for a
// while, a failure only briefly (the first HTTPS certificate takes a few seconds).
type tsCheckCache struct {
	mu  sync.Mutex
	url string
	at  time.Time
	err error
}

const tsCheckOK = 30 * time.Second

// tsCheckFail is a variable so tests need not wait.
var tsCheckFail = 3 * time.Second

// selfCheck fetches url/api/health through Tailscale (as a phone would) and checks that this
// daemon answers. It catches what `tailscale serve status` cannot: HTTPS certificates that do
// not work, MagicDNS that does not resolve, a proxy that is gone.
func (s *Server) selfCheck(ctx context.Context, url string) error {
	s.tsCheck.mu.Lock()
	if s.tsCheck.url == url && !s.tsCheck.at.IsZero() {
		age := time.Since(s.tsCheck.at)
		if s.tsCheck.err == nil && age < tsCheckOK || s.tsCheck.err != nil && age < tsCheckFail {
			err := s.tsCheck.err
			s.tsCheck.mu.Unlock()
			return err
		}
	}
	s.tsCheck.mu.Unlock()
	probe := s.tsProbe
	if probe == nil {
		probe = s.probeHealth
	}
	err := probe(ctx, url)
	s.tsCheck.mu.Lock()
	s.tsCheck.url, s.tsCheck.at, s.tsCheck.err = url, time.Now(), err
	s.tsCheck.mu.Unlock()
	return err
}

// forgetSelfCheck drops the remembered self-check (the setup changed).
func (s *Server) forgetSelfCheck() {
	s.tsCheck.mu.Lock()
	s.tsCheck.at = time.Time{}
	s.tsCheck.mu.Unlock()
}

// probeHealth is the real self-check: GET <url>/api/health must answer this machine's name.
func (s *Server) probeHealth(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/api/health", nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var h struct {
		OK      bool   `json:"ok"`
		Machine string `json:"machine"`
	}
	if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&h) != nil || !h.OK {
		return fmt.Errorf("%s answered %s", url, res.Status)
	}
	if s.f != nil && h.Machine != s.f.Machine() {
		return fmt.Errorf("%s is another machine (%s)", url, h.Machine)
	}
	return nil
}

func (s *Server) tailscaleSetup(ctx context.Context) tsSetup {
	ts := s.tsClient()
	if ts.Path == "" {
		return tsSetup{ts: ts, problem: &phoneProblem{Step: "install", Error: "Tailscale is not installed on this computer", Link: "https://tailscale.com/download"}}
	}
	st, err := ts.Status(ctx)
	if err != nil {
		return tsSetup{ts: ts, problem: &phoneProblem{Step: "start", Error: "Tailscale is installed but not running: open it and connect", Output: err.Error()}}
	}
	switch {
	case st.BackendState == "NeedsLogin" || st.BackendState == "NeedsMachineAuth":
		return tsSetup{ts: ts, problem: &phoneProblem{Step: "login", Error: "Tailscale is not signed in on this computer: open it and sign in"}}
	case !st.Running():
		return tsSetup{ts: ts, problem: &phoneProblem{Step: "start", Error: "Tailscale is not connected (" + st.BackendState + "): open it and connect"}}
	case st.DNSName == "":
		return tsSetup{ts: ts, problem: &phoneProblem{Step: "magicdns", Error: "MagicDNS is off for your tailnet: turn it on, with HTTPS certificates", Link: tailscale.AdminDNS}}
	}
	target := "http://127.0.0.1:" + strconv.Itoa(s.port)
	phone := st.PhoneOnline()
	// tmx only ever ADDS a route on a port nothing is served on: `tailscale serve` on a port in
	// use replaces what is there (the owner's other installs). So when what is served cannot
	// be read, nothing is changed.
	served, err := ts.ServedAll(ctx, st.DNSName)
	if err != nil {
		return tsSetup{ts: ts, host: st.DNSName, phone: phone, problem: &phoneProblem{Step: "failed", Error: "tmx could not read what Tailscale already serves on this computer, so it changed nothing", Output: err.Error()}}
	}
	// Already served somewhere (also on a port someone set up by hand): use that, a name that
	// is already trusted first, then the lowest port.
	var mine []int
	for p, t := range served {
		if t == target {
			mine = append(mine, p)
		}
	}
	sort.Ints(mine)
	sort.SliceStable(mine, func(i, j int) bool {
		return s.trusts(trustedName(st.DNSName, mine[i])) && !s.trusts(trustedName(st.DNSName, mine[j]))
	})
	if len(mine) > 0 {
		return tsSetup{ts: ts, host: st.DNSName, port: mine[0], serving: true, phone: phone}
	}
	for _, p := range append(slices.Clone(servePorts), s.port) {
		if _, used := served[p]; !used && p > 0 {
			return tsSetup{ts: ts, host: st.DNSName, port: p, phone: phone}
		}
	}
	return tsSetup{ts: ts, host: st.DNSName, phone: phone, problem: &phoneProblem{Step: "busy", Error: "Tailscale already serves other things on HTTPS ports 443, 8443 and 10000 of this computer"}}
}

// trustedName is the trusted host for a tailnet name served on an HTTPS port.
func trustedName(host string, port int) string {
	if port == 443 {
		return host
	}
	return host + ":" + strconv.Itoa(port)
}

// ensureServed runs `tailscale serve` for tmx when it is not served yet. That alone opens
// nothing: a request through the tailnet name is refused until the name is a trusted host,
// which is behind the owner gate. So the guided setup may try it whenever it checks again.
func (s *Server) ensureServed(ctx context.Context, set *tsSetup) *phoneProblem {
	if set.problem != nil || set.serving {
		return set.problem
	}
	err := set.ts.Serve(ctx, set.port, "http://127.0.0.1:"+strconv.Itoa(s.port))
	var se *tailscale.ServeError
	switch {
	case errors.As(err, &se):
		p := &phoneProblem{Step: se.Step, Error: se.Error(), Link: se.Link, Output: se.Output}
		if se.Step == "denied" {
			p.Error = "Tailscale did not let tmx set it up; run this once in a terminal: sudo tailscale set --operator=$USER"
		}
		return p
	case err != nil:
		return &phoneProblem{Step: "failed", Error: err.Error()}
	}
	set.serving = true
	s.log.Info("phone connect: tailscale serve", "url", "https://"+trustedName(set.host, set.port))
	return nil
}

// tailscaleStatus is the Tailscale way's state; setup also serves tmx when that is the next
// step (the guided setup's checks; a plain status read changes nothing).
func (s *Server) tailscaleStatus(ctx context.Context, setup bool) TailscaleStatus {
	set := s.tailscaleSetup(ctx)
	problem := set.problem
	if setup {
		problem = s.ensureServed(ctx, &set)
	}
	st := TailscaleStatus{Installed: set.ts.Path != "", Host: set.host, Serving: set.serving, PhoneOnline: set.phone}
	switch {
	case set.problem == nil:
		st.State = "Running"
	case set.problem.Step == "login":
		st.State = "NeedsLogin"
	case set.problem.Step != "install" && set.problem.Step != "start":
		st.State = "Running"
	}
	if problem != nil {
		st.Step, st.Error, st.Link = problem.Step, problem.Error, problem.Link
	}
	if set.serving {
		name := trustedName(set.host, set.port)
		st.URL = "https://" + name
		st.Trusted = s.trusts(name)
	}
	st.Ready = problem == nil && st.Serving && st.Trusted
	if st.Ready {
		// Served and trusted is not yet "works": a phone must get an answer through the name.
		if err := s.selfCheck(ctx, st.URL); err != nil {
			st.Ready = false
			st.Step, st.Error = "checking", "tmx does not answer at "+st.URL+" yet: "+err.Error()
		}
	}
	return st
}

// connectRequest is POST /api/phone/connect's body; the owner gate's proof is bound to it.
type connectRequest struct {
	Via     string `json:"via"`
	Address string `json:"address,omitempty"`
}

// connectPhone: POST /api/phone/connect {via: "tailscale"|"wifi", address?} → 201
// {via, pairing}; a setup step still missing is a 409 {error, step, link?}.
func (s *Server) connectPhone(w http.ResponseWriter, r *http.Request) {
	var req connectRequest
	if !decode(w, r, &req) {
		return
	}
	if s.devices == nil || s.devices.err != nil {
		writeError(w, http.StatusServiceUnavailable, "device pairing unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if s.pin == nil || !s.pin.IsSet() {
		// The phone proves the owner with the PIN: without one the code could never pair.
		writeJSON(w, http.StatusConflict, phoneProblem{Step: "pin", Error: "Choose your owner PIN first: your phone asks for it when it connects"})
		return
	}
	switch req.Via {
	case "", "tailscale":
		s.connectTailscale(w, r, req)
	case "wifi":
		s.connectWifi(w, r, req) // phonelan.go
	default:
		writeError(w, http.StatusBadRequest, `via must be "tailscale" or "wifi"`)
	}
}

func (s *Server) connectTailscale(w http.ResponseWriter, r *http.Request, req connectRequest) {
	set := s.tailscaleSetup(r.Context())
	if p := s.ensureServed(r.Context(), &set); p != nil {
		writeJSON(w, http.StatusConflict, p)
		return
	}
	name := trustedName(set.host, set.port)
	if !s.trusts(name) {
		if !s.requireOwner(w, r, gate.Action{Reason: "let your phone reach tmx through Tailscale (" + name + ")", Key: actionKey(r, req)}) {
			return
		}
		if err := s.addTrustedHost(name); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		s.forgetSelfCheck()
	}
	// A code only for a name that answers: the first HTTPS certificate can take a few seconds.
	url := "https://" + name
	var err error
	for i := 0; i < 6; i++ {
		if err = s.selfCheck(r.Context(), url); err == nil || r.Context().Err() != nil {
			break
		}
		select {
		case <-r.Context().Done():
		case <-time.After(tsCheckFail):
		}
	}
	if err != nil {
		writeJSON(w, http.StatusConflict, phoneProblem{Step: "checking", Error: "tmx does not answer at " + url + " yet: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, PhoneConnection{Via: "tailscale", Pairing: s.issuePairing(url, name)})
}

// issuePairing makes a new code (replacing the one shown before) for a phone that opens base.
func (s *Server) issuePairing(base, host string) Pairing {
	host = config.NormalizeTrustedHost(host)
	code, exp := s.pairing.issue(host)
	s.log.Info("phone connect: code shown", "host", host, "expires", exp.Format(time.TimeOnly))
	return Pairing{Code: code, Host: host, ExpiresAt: exp.UnixMilli(), URL: base + "/#pair=" + code}
}

// cancelPhoneCode: DELETE /api/phone/connect: the code shown stops working (the dialog closed).
func (s *Server) cancelPhoneCode(w http.ResponseWriter, r *http.Request) {
	s.pairing.cancel()
	w.WriteHeader(http.StatusNoContent)
}
