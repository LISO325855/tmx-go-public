package api

// Browser devices are independent of the host's bearer token. Only hashes of
// session credentials are persisted; pairing and ceremony state is short lived.
import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/gate"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/ownerpin"
)

const deviceCookie = "__Host-tmx-device"
const pairingCookie = "__Host-tmx-pair"
const deviceMediaTicket = "device-session"

type deviceSessionContext struct{}
type recoverySessionContext struct{}

const pairTTL = 2 * time.Minute
const deviceTTL = 90 * 24 * time.Hour

type ownerDevice struct {
	Recovery bool      `json:"recovery,omitempty"`
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Host     string    `json:"host"`
	Created  time.Time `json:"created"`
	Expires  time.Time `json:"expires"`
	Hash     string    `json:"session_hash,omitempty"`

	// Kind is "phone" (a phone or tablet: it locks, phonelock.go) or "computer", from the
	// browser that paired; "" = paired before this was kept, set at its next request.
	Kind string     `json:"kind,omitempty"`
	Lock deviceLock `json:"lock,omitzero"` // phonelock.go: its fingerprint passkeys
	// LAN: paired over the same Wi-Fi fallback (phonelan.go); gone when that is switched off.
	LAN bool `json:"lan,omitempty"`
}
type deviceState struct {
	UserID  []byte        `json:"user_id"`
	Devices []ownerDevice `json:"devices"`
}
type pairRequest struct {
	RecoveryUntil     time.Time
	Host, Claim, Name string
	Expires           time.Time
	Approved          bool
	Attempts          int
}
type browserCeremony struct {
	Host, Caller, Kind, Key, Device, Pair string
	Expires                               time.Time
	Confirmed                             bool
}
type deviceStore struct {
	mu         sync.Mutex
	bootstrap  *ownerBootstrap
	path       string
	state      deviceState
	pairs      map[string]*pairRequest
	ceremonies map[string]*browserCeremony
	rates      map[string][]time.Time
	active     map[string]map[string]context.CancelFunc
	pinSess    map[string]*pinSession // device ID -> its owner PIN session (ownerpin.go)
	now        func() time.Time
	err        error
}

func newDeviceStore(path string) *deviceStore {
	d := &deviceStore{path: path, pairs: map[string]*pairRequest{}, ceremonies: map[string]*browserCeremony{}, rates: map[string][]time.Time{}, active: map[string]map[string]context.CancelFunc{}, pinSess: map[string]*pinSession{}, now: time.Now}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		d.state.UserID = make([]byte, 32)
		_, d.err = rand.Read(d.state.UserID)
	} else if err != nil {
		d.err = err
	} else {
		d.err = json.Unmarshal(b, &d.state)
		if len(d.state.UserID) != 32 {
			d.err = errors.New("invalid device state")
		}
	}
	return d
}

// save rolls back failed writes so revocation/enrollment never appears successful
// without durable state. Caller holds mu.
func (d *deviceStore) save(before deviceState) error {
	b, err := json.Marshal(d.state)
	if err == nil {
		err = config.WriteSecretFile(d.path, append(b, '\n'))
	}
	if err != nil {
		d.state = before
	}
	return err
}
func (d *deviceStore) before() deviceState {
	st := d.state
	st.Devices = append([]ownerDevice(nil), st.Devices...)
	return st
}
func randomDeviceID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func deviceHash(v string) string         { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func requestHost(r *http.Request) string { return config.NormalizeTrustedHost(r.Host) }
func cookieValue(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}
func setDeviceCookie(w http.ResponseWriter, name, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: age})
}

// sessionCookieName is the device session cookie for the address a request came through. A
// cookie belongs to a host name, never to a port, and one machine often serves several tmx
// installs on ports of the same ts.net name: with one shared name, pairing at one install
// signed the phone out of the others. So an address with a port has its own cookie name; the
// plain name stays for port 443 and is still read everywhere (sessions paired before this).
func sessionCookieName(r *http.Request) string {
	if viaLAN(r) { // plain http: no __Host- prefix, a name of its own (phonelan.go)
		_, port, _ := strings.Cut(requestHost(r), ":")
		return lanCookie + "-" + port
	}
	if _, port, ok := strings.Cut(requestHost(r), ":"); ok && port != "" {
		return deviceCookie + "-" + port
	}
	return deviceCookie
}

// sessionTokens are the device session tokens a request carries, this address's own first.
func sessionTokens(r *http.Request) []string {
	var out []string
	names := []string{sessionCookieName(r), deviceCookie}
	if viaLAN(r) {
		names = names[:1]
	}
	for _, name := range names {
		if v := cookieValue(r, name); v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// setSessionCookie gives the browser its device session for this address.
func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, value string, age int) {
	if viaLAN(r) { // no https on the local network: the cookie cannot be Secure
		http.SetCookie(w, &http.Cookie{Name: sessionCookieName(r), Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: age})
		return
	}
	setDeviceCookie(w, sessionCookieName(r), value, age)
}

func (d *deviceStore) find(r *http.Request) (int, bool) {
	i, _, ok := d.findToken(r)
	return i, ok
}

// findToken also answers which of the request's tokens is the device's.
func (d *deviceStore) findToken(r *http.Request) (int, string, bool) {
	for _, tok := range sessionTokens(r) {
		hash := deviceHash(tok)
		for i, v := range d.state.Devices {
			if v.Hash == hash && v.Host == requestHost(r) && v.LAN == viaLAN(r) && d.now().Before(v.Expires) {
				return i, tok, true
			}
		}
	}
	return 0, "", false
}
func (s *Server) deviceSession(r *http.Request) (context.Context, func(), bool) {
	d := s.devices
	if d == nil || !(s.trustedProxy(r) || s.lanRequest(r)) {
		return nil, nil, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return nil, nil, false
	}
	i, ok := d.find(r)
	if !ok {
		return nil, nil, false
	}
	if d.state.Devices[i].Kind == "" { // paired before kinds were kept: what it is now
		before := d.before()
		d.state.Devices[i].Kind = deviceKind(r)
		_ = d.save(before) // not saved: asked again at the next request
	}
	v := d.state.Devices[i]
	ctx, cancel := context.WithDeadline(r.Context(), v.Expires)
	ctx = context.WithValue(ctx, deviceSessionContext{}, v.Expires)
	ctx = context.WithValue(ctx, deviceIDContext{}, v.ID) // phonelock.go
	if v.Kind == kindPhone && !v.Recovery {
		ctx = context.WithValue(ctx, lockableContext{}, true)
	}
	if v.Recovery {
		ctx = context.WithValue(ctx, recoverySessionContext{}, v.ID)
	}
	id := randomDeviceID()
	if d.active[v.ID] == nil {
		d.active[v.ID] = map[string]context.CancelFunc{}
	}
	d.active[v.ID][id] = cancel
	cleanup := func() {
		cancel()
		d.mu.Lock()
		delete(d.active[v.ID], id)
		if len(d.active[v.ID]) == 0 {
			delete(d.active, v.ID)
		}
		d.mu.Unlock()
	}
	return ctx, cleanup, true
}

// Media URLs from a paired browser carry only a public marker, never an
// independent bearer ticket that would survive device revocation. The actual
// media request must authenticate its current cookie through guard.
func devicePlayback(w http.ResponseWriter, r *http.Request) bool {
	expires, ok := r.Context().Value(deviceSessionContext{}).(time.Time)
	if !ok {
		return false
	}
	writeJSON(w, http.StatusOK, map[string]any{"ticket": deviceMediaTicket, "expires_at": expires.UnixMilli()})
	return true
}
func (d *deviceStore) limit(key string, n int) bool {
	now := d.now()
	for k, times := range d.rates {
		if len(times) == 0 || now.Sub(times[len(times)-1]) >= time.Minute {
			delete(d.rates, k)
		}
	}
	recent := d.rates[key][:0]
	for _, t := range d.rates[key] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= n {
		return false
	}
	d.rates[key] = append(recent, now)
	return true
}
func (d *deviceStore) gc() {
	for k, p := range d.pairs {
		if !d.now().Before(p.Expires) {
			delete(d.pairs, k)
		}
	}
	for k, c := range d.ceremonies {
		if !d.now().Before(c.Expires) {
			delete(d.ceremonies, k)
		}
	}
}
func (s *Server) deviceEvent(kind, id string) {
	if s.f != nil {
		s.f.Publish(model.BusEvent{Type: "device." + kind, Data: map[string]string{"id": id}, At: model.Now()})
	}
	s.log.Info("browser device event", "event", kind, "device", id)
}
func (s *Server) deviceRoutes(mux *http.ServeMux) {
	s.bootstrapRoutes(mux)
	s.pinRoutes(mux) // ownerpin.go
	mux.HandleFunc("POST /api/pair/start", s.startPair)
	mux.HandleFunc("POST /api/pair/claim", s.claimPair)
	mux.HandleFunc("POST /api/pair/pin", s.pairWithPIN) // ownerpin.go
	mux.HandleFunc("POST /api/pair/scan", s.scanPair)   // phonecode.go: the QR's code + the owner PIN
	mux.HandleFunc("POST /api/pair/scan/check", s.checkScan)
	s.phoneRoutes(mux)     // phone.go: Connect a phone
	s.phoneLockRoutes(mux) // phonelock.go
	mux.HandleFunc("POST /api/devices/approve", s.ownerOnly(s.approvePair))
	mux.HandleFunc("GET /api/devices", s.ownerOnly(s.listDevices))
	mux.HandleFunc("DELETE /api/devices/{id}", s.ownerOnly(s.revokeDevice))
	mux.HandleFunc("POST /api/gate/browser/{id}", s.ownerOnly(s.confirmWithPIN))       // ownerpin.go
	mux.HandleFunc("POST /api/gate/approvals/{id}/pin", s.ownerOnly(s.confirmWithPIN)) // Python's name for it
}
func publicPairRoute(r *http.Request) bool {
	if r.Method == "POST" && (r.URL.Path == "/api/pair/bootstrap/exchange" || r.URL.Path == "/api/pair/bootstrap/approve") {
		return true // These handlers authenticate only the restricted bootstrap grant.
	}
	return r.Method == "POST" && (r.URL.Path == "/api/pair/start" || r.URL.Path == "/api/pair/claim" || r.URL.Path == "/api/pair/pin" ||
		r.URL.Path == "/api/pair/scan" || r.URL.Path == "/api/pair/scan/check")
}
func (s *Server) browserReady(w http.ResponseWriter, r *http.Request) bool {
	if s.devices == nil || s.devices.err != nil {
		writeError(w, 503, "device pairing unavailable")
		return false
	}
	if !s.trustedProxy(r) {
		writeError(w, 403, "open the trusted HTTPS address on this device")
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	return true
}

// deviceReady is browserReady for what a paired device does, and for pairing with a
// Connect-a-phone code: the trusted HTTPS address, or the same Wi-Fi fallback while it is on
// (phonelan.go). Pairing WITHOUT a code stays https only (browserReady).
func (s *Server) deviceReady(w http.ResponseWriter, r *http.Request) bool {
	if s.devices == nil || s.devices.err != nil {
		writeError(w, 503, "device pairing unavailable")
		return false
	}
	if !s.trustedProxy(r) && !s.lanRequest(r) {
		writeError(w, 403, "open the trusted HTTPS address on this device")
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	return true
}

func (s *Server) startPair(w http.ResponseWriter, r *http.Request) {
	if !s.browserReady(w, r) {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &req) {
		return
	}
	d := s.devices
	d.mu.Lock()
	defer d.mu.Unlock()
	d.gc()
	if !d.limit("pair-start", 10) || len(d.pairs) >= 30 {
		writeError(w, 429, "too many pairing requests; wait a minute")
		return
	}
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		writeErr(w, err)
		return
	}
	code := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	claim := randomDeviceID()
	// Replace this browser's outstanding attempt; the code alone can never claim it.
	old := deviceHash(cookieValue(r, pairingCookie))
	for k, p := range d.pairs {
		if p.Claim == old && p.Host == requestHost(r) {
			delete(d.pairs, k)
		}
	}
	d.pairs[code] = &pairRequest{Host: requestHost(r), Claim: deviceHash(claim), Name: short(req.Name, 80), Expires: d.now().Add(pairTTL)}
	setDeviceCookie(w, pairingCookie, claim, 120)
	s.deviceEvent("pair_requested", "")
	writeJSON(w, 201, map[string]any{"code": code, "expires_in": 120})
}
func (d *deviceStore) pairFor(r *http.Request) (string, *pairRequest) {
	claim := cookieValue(r, pairingCookie)
	if claim == "" {
		return "", nil
	}
	hash := deviceHash(claim)
	for code, p := range d.pairs {
		if p.Claim == hash && p.Host == requestHost(r) && d.now().Before(p.Expires) {
			return code, p
		}
	}
	return "", nil
}
func (s *Server) approvePair(w http.ResponseWriter, r *http.Request) {
	if !localRequest(r) {
		writeError(w, 403, "approve new devices on the host")
		return
	}
	if !validUISession(r.Header.Get(UISessionHeader)) {
		writeError(w, 403, "approve the code from Owner devices in your signed-in UI")
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &req) {
		return
	}
	d := s.devices
	if d == nil || d.err != nil {
		writeError(w, 503, "device pairing unavailable")
		return
	}
	d.mu.Lock()
	allowed := d.limit("pair-approve", 10)
	d.mu.Unlock()
	if !allowed {
		writeError(w, 429, "too many pairing attempts")
		return
	}
	req.Code = strings.ToUpper(strings.TrimSpace(req.Code))
	reason := "pair this device"
	prompt := reason
	d.mu.Lock()
	if p := d.pairs[req.Code]; p != nil && d.now().Before(p.Expires) {
		reason = "pair a device at https://" + p.Host
		prompt = "pair the device showing code " + req.Code + " at https://" + p.Host + ". Approve only if this exact code is on your own device"
	}
	d.mu.Unlock()
	if !s.requireOwner(w, r, gate.Action{Reason: reason, PromptReason: prompt, Key: actionKey(r, req), Always: true, Fresh: true}) {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.gc()
	p := d.pairs[req.Code]
	if p == nil || p.Approved || (!localHost(r.Host) && requestHost(r) != p.Host) {
		writeError(w, 400, "invalid or expired pairing code")
		return
	}
	p.Approved = true
	s.deviceEvent("pair_approved", "")
	writeJSON(w, 200, map[string]bool{"approved": true})
}
func (s *Server) claimPair(w http.ResponseWriter, r *http.Request) {
	if !s.browserReady(w, r) {
		return
	}
	d := s.devices
	d.mu.Lock()
	defer d.mu.Unlock()
	d.gc()
	code, p := d.pairFor(r)
	if p == nil {
		writeError(w, 400, "pairing expired; start again")
		return
	}
	if !p.Approved {
		writeJSON(w, 202, map[string]bool{"pending": true})
		return
	}
	tok := randomDeviceID()
	now := d.now()
	before := d.before()
	v := ownerDevice{ID: randomDeviceID(), Name: p.Name, Host: p.Host, Hash: deviceHash(tok), Created: now, Expires: now.Add(deviceTTL), Kind: deviceKind(r)}
	if !p.RecoveryUntil.IsZero() {
		v.Recovery, v.Expires = true, p.RecoveryUntil
	}
	if len(d.state.Devices) >= 100 {
		writeError(w, 409, "remove an old device before pairing another")
		return
	}
	d.state.Devices = append(d.state.Devices, v)
	if err := d.save(before); err != nil {
		writeError(w, 503, "could not save device")
		return
	}
	delete(d.pairs, code)
	s.lock.opened(v.ID) // phonelock.go: a device just paired is open
	s.deviceEvent("paired", v.ID)
	s.setSessionCookie(w, r, tok, int(v.Expires.Sub(now).Seconds()))
	setDeviceCookie(w, pairingCookie, "", -1)
	writeJSON(w, 200, map[string]any{"id": v.ID, "recovery": v.Recovery})
}
func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	d := s.devices
	if d == nil || d.err != nil {
		writeError(w, 503, "devices unavailable")
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	list := []map[string]any{}
	for _, v := range d.state.Devices {
		list = append(list, map[string]any{"id": v.ID, "name": v.Name, "host": v.Host, "created": v.Created, "expires": v.Expires, "recovery": v.Recovery, "kind": v.Kind, "lan": v.LAN})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, list)
}

// revokeDevice needs the owner's fresh proof (audit 3 Nov): the owner PIN from a paired
// browser, the host gate on the machine. A device may always revoke itself (it only gives up
// access): that is how a restricted recovery session ends.
func (s *Server) revokeDevice(w http.ResponseWriter, r *http.Request) {
	d := s.devices
	if d == nil || d.err != nil {
		writeError(w, 503, "devices unavailable")
		return
	}
	id := r.PathValue("id")
	if s.deviceID(r) != id || localHost(r.Host) {
		if !s.requireOwner(w, r, gate.Action{Reason: "revoke a paired device", Key: actionKey(r, map[string]string{"id": id}), Always: true, Fresh: true}) {
			return
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	before := d.before()
	found := false
	for i, v := range d.state.Devices {
		if v.ID == id {
			d.state.Devices = append(d.state.Devices[:i], d.state.Devices[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		writeError(w, 404, "device not found")
		return
	}
	if err := d.save(before); err != nil {
		writeError(w, 503, "could not revoke device")
		return
	}
	for _, cancel := range d.active[id] {
		cancel()
	}
	delete(d.active, id)
	// An approved but unclaimed pair may derive from the revoked credential.
	// Conservatively cancel all short-lived pairing grants on any revocation.
	d.pairs = map[string]*pairRequest{}
	d.bootstrap = nil
	d.ceremonies = map[string]*browserCeremony{}
	delete(d.pinSess, id)
	s.lock.forget(id)
	if s.push != nil {
		if err := s.push.Unsubscribe(id); err != nil {
			s.log.Warn("revoked device push cleanup failed")
		}
	}
	s.deviceEvent("revoked", id)
	w.WriteHeader(204)
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// requireBrowserOwner: a paired browser proves the owner with the owner PIN (ownerpin.go),
// never a passkey. It passes on a confirmed PIN ceremony bound to this host, caller and
// action, or on the device's PIN session (Fresh/Always actions: a PIN in the last 5 min).
// Otherwise it answers 428 with a pending confirmation of method "pin".
func (s *Server) requireBrowserOwner(w http.ResponseWriter, r *http.Request, a gate.Action, id string) bool {
	if !s.deviceReady(w, r) {
		return false
	}
	d := s.devices
	d.mu.Lock()
	defer d.mu.Unlock()
	d.gc()
	i, ok := d.find(r)
	if !ok {
		writeError(w, 403, "pair this device first")
		return false
	}
	dev := d.state.Devices[i].ID
	if c := d.ceremonies[id]; c != nil && c.Kind == "action" && c.Host == requestHost(r) && c.Caller == gateCaller(r) && c.Key == a.Key && c.Confirmed && c.Device == dev {
		delete(d.ceremonies, id)
		return true
	}
	if d.pinSessionOK(dev, a.Fresh || a.Always) {
		return true
	}
	if !d.limit("action-ceremony", 20) {
		writeError(w, 429, "too many owner confirmations; wait a minute")
		return false
	}
	reason := a.Reason
	if a.PromptReason != "" {
		reason = a.PromptReason
	}
	cid := randomDeviceID()
	d.ceremonies[cid] = &browserCeremony{Host: requestHost(r), Caller: gateCaller(r), Kind: "action", Key: a.Key, Device: dev, Expires: d.now().Add(ownerpin.Fresh)}
	writeJSON(w, http.StatusPreconditionRequired, map[string]any{"error": "Owner confirmation needed: your owner PIN.", "id": cid,
		"confirmation": map[string]any{"id": cid, "reason": reason, "method": "pin", "method_label": "Your owner PIN", "state": "pending"}})
	return false
}

func (s *Server) initDevices() {
	if s.f != nil {
		s.devices = newDeviceStore(filepath.Join(s.f.Paths().SecretsDir(), "browser-devices.json"))
		s.pin = ownerpin.Open(filepath.Join(s.f.Paths().SecretsDir(), "owner-pin.json"))
	}
}
