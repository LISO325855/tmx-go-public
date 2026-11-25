package api

// The phone lock (config.json "phone_lock"; docs/PHONE-PAIRING.md). A paired device's page
// that goes to the background (or is opened again) is locked until the owner unlocks it with
// the owner PIN, or with the fingerprint / Face ID (a WebAuthn passkey made on that phone).
// The daemon enforces it: while a device is locked its session gets 423 on every route but
// the lock's own, and its open requests and sockets end the moment it locks.
//
// Ported from Mustneer's v0.1.20 phonelock.go onto our device records (devices.go). Two
// differences, both so that the owner is never locked out away from the computer:
//   - the PIN is the ONE owner PIN (internal/ownerpin), not a second PIN per phone. Its wrong
//     tries wait as everywhere else (ownerpin's backoff, counted per device);
//   - wrong PINs never sign the device out.
//
// What locks a device (only when the owner PIN is set and the setting is on):
//   - its page says it went to the background (POST /api/devices/lock/hidden, sent with
//     keepalive) and does not say it is back (POST /api/devices/lock/alive) within "Lock
//     after"; "Immediately" locks at once;
//   - the backstop, because phones often drop that last request: no request from the device
//     for "Lock after" (at least lockIdleMin). A visible page says it is alive every
//     lockHeartbeat;
//   - a daemon start: every device starts locked (the state is in memory);
//   - POST /api/devices/lock (lock now).
//
// Never locked: the owner token (the desktop app, a browser on 127.0.0.1, the CLI), agents,
// Companion, a paired COMPUTER's browser (only phones and tablets lock: deviceKind), a
// restricted recovery session (it can do nothing anyway), and every device while no owner PIN
// is set (there is no secret to ask for).
//
// Passkeys: a WebAuthn platform credential made on the phone, verified here
// (github.com/go-webauthn/webauthn: challenge, origin, RP ID, user verification, signature,
// sign count). It opens the lock and nothing else: owner actions keep asking for the owner
// PIN. RP ID = the address's host name; a passkey is bound to the origin it was made on, so a
// device has at most one per address. WebAuthn needs a secure context: https only.

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/gate"
)

const (
	lockIdleMin     = 20 * time.Second // the backstop never locks sooner after the last request
	lockHeartbeat   = 10 * time.Second // a visible page says it is alive this often
	lockSweepEvery  = time.Second      // how often locks that came due end their sockets
	lockCeremonyTTL = 2 * time.Minute  // a WebAuthn challenge
	lockMaxBody     = 64 << 10         // a WebAuthn answer
)

type deviceIDContext struct{}
type lockableContext struct{}

const (
	kindPhone    = "phone"
	kindComputer = "computer"
)

// deviceKind is what the browser of a request runs on: a phone or tablet locks, a computer's
// browser does not (a laptop's tab in the background is not a phone left on a table). It is
// kept with the device when it pairs, so "Desktop site" on a paired phone changes nothing.
func deviceKind(r *http.Request) string {
	ua := strings.ToLower(r.UserAgent())
	for _, mark := range []string{"android", "iphone", "ipad", "ipod", "mobile"} {
		if strings.Contains(ua, mark) {
			return kindPhone
		}
	}
	return kindComputer
}

// lockable reports whether the request is a paired phone's (the only thing that locks).
func lockable(r *http.Request) bool {
	ok, _ := r.Context().Value(lockableContext{}).(bool)
	return ok
}

// sessionDevice is the paired device a request's session belongs to ("": not a device).
func sessionDevice(r *http.Request) string {
	id, _ := r.Context().Value(deviceIDContext{}).(string)
	return id
}

// deviceLock is what browser-devices.json keeps of a device's lock.
type deviceLock struct {
	Passkeys []passkey `json:"passkeys,omitempty"` // at most one per origin
}

// passkey is a WebAuthn credential of a device, for the one origin it was made on.
type passkey struct {
	Origin     string              `json:"origin"` // https://<trusted host>
	Credential webauthn.Credential `json:"credential"`
	CreatedAt  int64               `json:"created_at"`
}

func (l deviceLock) passkeysFor(origin string) []webauthn.Credential {
	var out []webauthn.Credential
	for _, p := range l.Passkeys {
		if p.Origin == origin {
			out = append(out, p.Credential)
		}
	}
	return out
}

// lockOf is a copy of a device's lock record and its name; ok false: no such device.
func (d *deviceStore) lockOf(id string) (deviceLock, string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	i := slices.IndexFunc(d.state.Devices, func(v ownerDevice) bool { return v.ID == id })
	if i < 0 {
		return deviceLock{}, "", false
	}
	l := d.state.Devices[i].Lock
	l.Passkeys = slices.Clone(l.Passkeys)
	return l, d.state.Devices[i].Name, true
}

// updateLock changes a device's lock record and writes the file; ok false: no such device.
func (d *deviceStore) updateLock(id string, fn func(*deviceLock)) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	i := slices.IndexFunc(d.state.Devices, func(v ownerDevice) bool { return v.ID == id })
	if i < 0 {
		return false, nil
	}
	before := d.before()
	next := d.state.Devices[i].Lock
	next.Passkeys = slices.Clone(next.Passkeys)
	fn(&next)
	d.state.Devices[i].Lock = next
	return true, d.save(before)
}

// endDevice ends every request and socket a device has open.
func (d *deviceStore) endDevice(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, cancel := range d.active[id] {
		cancel()
	}
}

// ---- the lock's state in memory ---------------------------------------------------------------

// phoneLock is the setting and every device's lock state now (memory only: a restart locks
// every device).
type phoneLock struct {
	mu    sync.Mutex
	now   func() time.Time // nil = time.Now (tests set a clock)
	on    bool
	after time.Duration
	runs  map[string]*lockRun
}

type lockRun struct {
	open   bool      // unlocked
	seen   time.Time // its last request while open
	hidden time.Time // its page said it went to the background; zero = in front
	reg    *ceremony // a passkey being made
	login  *ceremony // a passkey unlock under way
}

// ceremony is one WebAuthn challenge the daemon gave out.
type ceremony struct {
	session webauthn.SessionData
	origin  string
	expires time.Time
}

func (p *phoneLock) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

func (p *phoneLock) set(on bool, after time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.on, p.after = on, after
}

func (p *phoneLock) settings() (bool, time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.on, p.after
}

// runLocked is a device's state, a locked one when there is none yet.
func (p *phoneLock) runLocked(id string) *lockRun {
	if p.runs == nil {
		p.runs = map[string]*lockRun{}
	}
	r := p.runs[id]
	if r == nil {
		r = &lockRun{}
		p.runs[id] = r
	}
	return r
}

// dueLocked reports whether an open device must lock now.
func (p *phoneLock) dueLocked(r *lockRun, now time.Time) bool {
	if !r.open {
		return false
	}
	if !r.hidden.IsZero() && now.Sub(r.hidden) >= p.after {
		return true
	}
	return now.Sub(r.seen) >= max(p.after, lockIdleMin)
}

// check reports whether the device is locked now (locking it when that came due: changed).
// hasPIN: the owner PIN is set; without one nothing locks.
func (p *phoneLock) check(id string, hasPIN bool) (locked, changed bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.on || !hasPIN {
		return false, false
	}
	r := p.runLocked(id)
	if p.dueLocked(r, p.clock()) {
		r.open, r.hidden = false, time.Time{}
		changed = true
	}
	return !r.open, changed
}

// touch notes a request from an open device (the backstop counts from the last one).
func (p *phoneLock) touch(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r := p.runs[id]; r != nil && r.open {
		r.seen = p.clock()
	}
}

// opened unlocks a device (the right PIN or passkey, a device just paired).
func (p *phoneLock) opened(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.runLocked(id)
	r.open, r.seen, r.hidden = true, p.clock(), time.Time{}
}

// lockNow locks a device at once; changed: it was open.
func (p *phoneLock) lockNow(id string) (changed bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.runLocked(id)
	changed = r.open
	r.open, r.hidden = false, time.Time{}
	return changed
}

// hide notes that the device's page went to the background; changed: "Immediately" locked it.
func (p *phoneLock) hide(id string, hasPIN bool) (changed bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.runs[id]
	if r == nil || !r.open {
		return false
	}
	if r.hidden.IsZero() {
		r.hidden = p.clock()
	}
	if p.on && hasPIN && p.after <= 0 {
		r.open, r.hidden = false, time.Time{}
		return true
	}
	return false
}

// alive: the device's page is in front (and says so every lockHeartbeat). It does not unlock
// a locked device; check runs first.
func (p *phoneLock) alive(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r := p.runs[id]; r != nil && r.open {
		r.hidden, r.seen = time.Time{}, p.clock()
	}
}

func (p *phoneLock) forget(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.runs, id)
}

// sweep locks every open device whose time came and returns them.
func (p *phoneLock) sweep(hasPIN bool) []string {
	p.mu.Lock()
	open := []string{}
	for id, r := range p.runs {
		if r.open {
			open = append(open, id)
		}
	}
	p.mu.Unlock()
	var locked []string
	for _, id := range open {
		if _, changed := p.check(id, hasPIN); changed {
			locked = append(locked, id)
		}
	}
	return locked
}

// setCeremony keeps a device's WebAuthn challenge (one of each kind; a new one replaces it).
func (p *phoneLock) setCeremony(id string, login bool, s webauthn.SessionData, origin string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.runLocked(id)
	c := &ceremony{session: s, origin: origin, expires: p.clock().Add(lockCeremonyTTL)}
	if login {
		r.login = c
	} else {
		r.reg = c
	}
}

// takeCeremony uses a device's challenge up: nil when there is none for origin, or it expired.
func (p *phoneLock) takeCeremony(id string, login bool, origin string) *ceremony {
	c, _ := p.takeCeremonyReason(id, login, origin)
	return c
}

func (p *phoneLock) takeCeremonyReason(id string, login bool, origin string) (*ceremony, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.runs[id]
	if r == nil {
		return nil, "unknown_or_consumed_challenge"
	}
	c := r.reg
	if login {
		c, r.login = r.login, nil
	} else {
		r.reg = nil
	}
	if c == nil {
		return nil, "unknown_or_consumed_challenge"
	}
	if c.origin != origin {
		return nil, "wrong_origin"
	}
	if !p.clock().Before(c.expires) {
		return nil, "expired_challenge"
	}
	return c, ""
}

// ---- the server's side ------------------------------------------------------------------------

// SetPhoneLock changes the phone lock setting now.
func (s *Server) SetPhoneLock(on bool, after time.Duration) { s.lock.set(on, after) }

// SetLockClock replaces the phone lock's clock (tests).
func (s *Server) SetLockClock(now func() time.Time) {
	s.lock.mu.Lock()
	defer s.lock.mu.Unlock()
	s.lock.now = now
}

func (s *Server) pinIsSet() bool { return s.pin != nil && s.pin.IsSet() }

// SweepLocks locks every device whose time came and ends its sockets (lockLoop does it every
// second; tests call it after moving their clock).
func (s *Server) SweepLocks() {
	if s.devices == nil {
		return
	}
	for _, id := range s.lock.sweep(s.pinIsSet()) {
		s.log.Info("phone lock: device locked", "id", id, "why", "time")
		s.devices.endDevice(id)
	}
}

func (s *Server) lockLoop(done <-chan struct{}) {
	t := time.NewTicker(lockSweepEvery)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			s.SweepLocks()
		}
	}
}

// deviceLocked reports whether a device is locked now; one that just locked has its requests
// and sockets ended.
func (s *Server) deviceLocked(id string) bool {
	if s.devices == nil || id == "" {
		return false
	}
	locked, changed := s.lock.check(id, s.pinIsSet())
	if changed {
		s.log.Info("phone lock: device locked", "id", id, "why", "time")
		s.devices.endDevice(id)
	}
	return locked
}

// lockFree is what a locked device may still ask: the lock's own routes.
func lockFree(r *http.Request) bool {
	p := r.URL.Path
	return p == "/api/devices/lock" || strings.HasPrefix(p, "/api/devices/lock/")
}

// refuseLocked answers a locked device's request (guard).
func refuseLocked(w http.ResponseWriter) {
	writeJSON(w, http.StatusLocked, map[string]any{
		"error":  "tmx is locked on this phone. Unlock it with your PIN or your fingerprint.",
		"locked": true,
	})
}

// guardLock is the guard's check for a device session: false = the device is locked and the
// answer is written. A request from an open device counts for the backstop.
func (s *Server) guardLock(w http.ResponseWriter, r *http.Request) bool {
	id := sessionDevice(r)
	if id == "" || !lockable(r) {
		return true
	}
	if s.deviceLocked(id) {
		if lockFree(r) {
			return true
		}
		refuseLocked(w)
		return false
	}
	s.lock.touch(id)
	return true
}

// LockStatus is GET /api/devices/lock and what the lock's routes answer.
type LockStatus struct {
	Device       bool  `json:"device"` // the asker is a paired device (nothing else locks)
	Enabled      bool  `json:"enabled"`
	AfterSeconds int   `json:"after_seconds"`
	Locked       bool  `json:"locked"`
	PINSet       bool  `json:"pin_set"`          // the owner PIN exists (without it nothing locks)
	Passkey      bool  `json:"passkey"`          // this address has a passkey (fingerprint, Face ID)
	PasskeyHere  bool  `json:"passkey_possible"` // this address can have one (https)
	RetryAfterMS int64 `json:"retry_after_ms,omitempty"`
	HeartbeatMS  int64 `json:"heartbeat_ms"`
}

func (s *Server) lockStatus(r *http.Request) LockStatus {
	on, after := s.lock.settings()
	st := LockStatus{Enabled: on, AfterSeconds: int(after / time.Second), PINSet: s.pinIsSet(), HeartbeatMS: lockHeartbeat.Milliseconds()}
	id := sessionDevice(r)
	if id == "" || s.devices == nil || !lockable(r) {
		return st
	}
	st.Device = true
	l, _, _ := s.devices.lockOf(id)
	origin, _, here := s.passkeyOrigin(r)
	st.Locked = s.deviceLocked(id)
	st.Passkey = here && len(l.passkeysFor(origin)) > 0
	st.PasskeyHere = here
	if s.pin != nil {
		st.RetryAfterMS = s.pin.Wait(pinSource(r, id)).Milliseconds()
	}
	return st
}

// passkeyOrigin is the origin and RP ID a passkey of this request's page has: only through a
// trusted HTTPS host (WebAuthn needs a secure context, which http://<LAN> is not).
func (s *Server) passkeyOrigin(r *http.Request) (origin, rpID string, ok bool) {
	if !s.trustedProxy(r) {
		return "", "", false
	}
	h := requestHost(r)
	rpID = h
	if i := strings.LastIndexByte(h, ':'); i >= 0 {
		rpID = h[:i]
	}
	return "https://" + h, rpID, true
}

func (s *Server) phoneLockRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/devices/lock", s.ownerOnly(s.getLock))
	mux.HandleFunc("POST /api/devices/lock", s.ownerOnly(s.deviceOnly(s.lockDeviceNow)))
	mux.HandleFunc("POST /api/devices/lock/hidden", s.ownerOnly(s.deviceOnly(s.lockHidden)))
	mux.HandleFunc("POST /api/devices/lock/alive", s.ownerOnly(s.deviceOnly(s.lockAlive)))
	mux.HandleFunc("POST /api/devices/lock/unlock", s.ownerOnly(s.deviceOnly(s.unlockWithPIN)))
	mux.HandleFunc("POST /api/devices/lock/passkey/register", s.ownerOnly(s.deviceOnly(s.beginPasskey)))
	mux.HandleFunc("POST /api/devices/lock/passkey/register/finish", s.ownerOnly(s.deviceOnly(s.finishPasskey)))
	mux.HandleFunc("DELETE /api/devices/lock/passkey", s.ownerOnly(s.deviceOnly(s.removePasskey)))
	mux.HandleFunc("POST /api/devices/lock/passkey/unlock", s.ownerOnly(s.deviceOnly(s.beginPasskeyUnlock)))
	mux.HandleFunc("POST /api/devices/lock/passkey/unlock/finish", s.ownerOnly(s.deviceOnly(s.finishPasskeyUnlock)))
	// The setting itself: the owner at the computer.
	mux.HandleFunc("GET /api/phone/lock", s.atComputer(s.getLockSetting))
	mux.HandleFunc("PUT /api/phone/lock", s.atComputer(s.putLockSetting))
}

// deviceOnly: the lock is a paired device's own; the owner token (this computer) has none.
func (s *Server) deviceOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if sessionDevice(r) == "" || s.devices == nil || !lockable(r) {
			writeError(w, http.StatusForbidden, "only a paired phone has a lock; a computer is never locked")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		h(w, r)
	}
}

func (s *Server) getLock(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.lockStatus(r))
}

// lockDeviceNow: POST /api/devices/lock (the device locks at once).
func (s *Server) lockDeviceNow(w http.ResponseWriter, r *http.Request) {
	id := sessionDevice(r)
	if on, _ := s.lock.settings(); !on || !s.pinIsSet() {
		writeError(w, http.StatusConflict, "the phone lock is switched off on your computer")
		return
	}
	if s.lock.lockNow(id) {
		s.log.Info("phone lock: device locked", "id", id, "why", "asked")
		s.devices.endDevice(id)
	}
	writeJSON(w, http.StatusOK, s.lockStatus(r))
}

// lockHidden: POST /api/devices/lock/hidden: the page went to the background (keepalive).
func (s *Server) lockHidden(w http.ResponseWriter, r *http.Request) {
	id := sessionDevice(r)
	if s.lock.hide(id, s.pinIsSet()) {
		s.log.Info("phone lock: device locked", "id", id, "why", "background")
		s.devices.endDevice(id)
	}
	w.WriteHeader(http.StatusNoContent)
}

// lockAlive: POST /api/devices/lock/alive: the page is in front (back from the background, or
// its heartbeat) → the status, locked when its time came while it was away.
func (s *Server) lockAlive(w http.ResponseWriter, r *http.Request) {
	id := sessionDevice(r)
	if !s.deviceLocked(id) {
		s.lock.alive(id)
	}
	writeJSON(w, http.StatusOK, s.lockStatus(r))
}

// unlockWithPIN: POST /api/devices/lock/unlock {pin}: the owner PIN opens the lock. A wrong
// one waits as the owner PIN does everywhere (403, then 429 with wait_s).
func (s *Server) unlockWithPIN(w http.ResponseWriter, r *http.Request) {
	var req pinBody
	if !decode(w, r, &req) {
		return
	}
	id := sessionDevice(r)
	if !s.verifyPIN(w, pinSource(r, id), req.PIN) {
		return
	}
	s.devices.mu.Lock()
	s.devices.pinProven(id) // the PIN was just typed: it also counts for owner actions
	s.devices.mu.Unlock()
	s.lock.opened(id)
	s.log.Info("phone lock: unlocked", "id", id, "with", "pin")
	writeJSON(w, http.StatusOK, s.lockStatus(r))
}

// ---- the setting (at the computer) -------------------------------------------------------------

// LockSetting is GET/PUT /api/phone/lock.
type LockSetting struct {
	Enabled      bool  `json:"enabled"`
	AfterSeconds int   `json:"after_seconds"`
	Choices      []int `json:"choices,omitempty"` // the "Lock after" lengths, in seconds
	PINSet       bool  `json:"pin_set"`           // without an owner PIN nothing locks
}

func (s *Server) lockSetting() LockSetting {
	on, after := s.lock.settings()
	return LockSetting{Enabled: on, AfterSeconds: int(after / time.Second), Choices: config.PhoneLockAfters, PINSet: s.pinIsSet()}
}

func (s *Server) getLockSetting(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.lockSetting())
}

// putLockSetting: PUT /api/phone/lock {enabled, after_seconds}, behind the owner gate (it
// decides how long a phone left lying around stays open).
func (s *Server) putLockSetting(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled      bool `json:"enabled"`
		AfterSeconds int  `json:"after_seconds"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !slices.Contains(config.PhoneLockAfters, req.AfterSeconds) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("after_seconds must be one of %v", config.PhoneLockAfters))
		return
	}
	if !s.requireOwner(w, r, gate.Action{Reason: "change the phone lock", Key: actionKey(r, req)}) {
		return
	}
	if s.f != nil {
		c, err := config.Load(s.f.Paths())
		if err != nil {
			writeErr(w, err)
			return
		}
		c.PhoneLock = &config.PhoneLockConfig{Off: !req.Enabled, AfterSeconds: &req.AfterSeconds}
		if err := config.Save(s.f.Paths(), c); err != nil {
			writeErr(w, err)
			return
		}
	}
	s.lock.set(req.Enabled, time.Duration(req.AfterSeconds)*time.Second)
	s.log.Info("phone lock: setting changed", "on", req.Enabled, "after_s", req.AfterSeconds)
	writeJSON(w, http.StatusOK, s.lockSetting())
}

// ---- passkeys (WebAuthn) -----------------------------------------------------------------------

// lockUser is a paired device as WebAuthn's user: its passkeys for one origin.
type lockUser struct {
	id, name string
	creds    []webauthn.Credential
}

func (u lockUser) WebAuthnID() []byte                         { return []byte(u.id) }
func (u lockUser) WebAuthnName() string                       { return u.name }
func (u lockUser) WebAuthnDisplayName() string                { return u.name }
func (u lockUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

// relyingParty is tmx as WebAuthn's relying party at one address.
func relyingParty(origin, rpID string) (*webauthn.WebAuthn, error) {
	return webauthn.New(&webauthn.Config{RPID: rpID, RPDisplayName: "tmx", RPOrigins: []string{origin}})
}

// passkeyCtx is what every passkey route needs: the device, its lock, the origin and the
// relying party.
type passkeyCtx struct {
	id     string
	lock   deviceLock
	origin string
	rp     *webauthn.WebAuthn
	user   lockUser
}

func (s *Server) passkeyCtx(w http.ResponseWriter, r *http.Request) (passkeyCtx, bool) {
	id := sessionDevice(r)
	l, name, ok := s.devices.lockOf(id)
	if !ok {
		writeError(w, http.StatusUnauthorized, "this phone was removed from tmx")
		return passkeyCtx{}, false
	}
	origin, rpID, here := s.passkeyOrigin(r)
	if !here {
		writeError(w, http.StatusConflict, "the fingerprint works on the https address, not on the Wi-Fi address")
		return passkeyCtx{}, false
	}
	rp, err := relyingParty(origin, rpID)
	if err != nil {
		writeErr(w, err)
		return passkeyCtx{}, false
	}
	if name == "" {
		name = "tmx"
	}
	return passkeyCtx{id: id, lock: l, origin: origin, rp: rp, user: lockUser{id: id, name: name, creds: l.passkeysFor(origin)}}, true
}

// beginPasskey: POST /api/devices/lock/passkey/register → the options for
// navigator.credentials.create ({publicKey}): a platform authenticator with user
// verification, no attestation. Only while unlocked (guard).
func (s *Server) beginPasskey(w http.ResponseWriter, r *http.Request) {
	pc, ok := s.passkeyCtx(w, r)
	if !ok {
		return
	}
	if s.deviceLocked(pc.id) {
		refuseLocked(w)
		return
	}
	creation, session, err := pc.rp.BeginRegistration(pc.user,
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			AuthenticatorAttachment: protocol.Platform,
			ResidentKey:             protocol.ResidentKeyRequirementDiscouraged,
			UserVerification:        protocol.VerificationRequired,
		}),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation))
	if err != nil {
		writeErr(w, err)
		return
	}
	s.lock.setCeremony(pc.id, false, *session, pc.origin)
	writeJSON(w, http.StatusOK, creation)
}

func readLockBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, lockMaxBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "the answer is too large or could not be read")
		return nil, false
	}
	return b, true
}

// finishPasskey: POST /api/devices/lock/passkey/register/finish {the PublicKeyCredential as
// JSON} → keeps the passkey for this address (replacing one made here before).
func (s *Server) finishPasskey(w http.ResponseWriter, r *http.Request) {
	pc, ok := s.passkeyCtx(w, r)
	if !ok {
		return
	}
	body, ok := readLockBody(w, r)
	if !ok {
		return
	}
	if s.deviceLocked(pc.id) {
		refuseLocked(w)
		return
	}
	c := s.lock.takeCeremony(pc.id, false, pc.origin)
	if c == nil {
		writeError(w, http.StatusBadRequest, "this took too long; try again")
		return
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "the fingerprint could not be set up: "+webauthnReason(err))
		return
	}
	cred, err := pc.rp.CreateCredential(pc.user, c.session, parsed)
	if err != nil {
		writeError(w, http.StatusBadRequest, "the fingerprint could not be set up: "+webauthnReason(err))
		return
	}
	found, err := s.devices.updateLock(pc.id, func(l *deviceLock) {
		l.Passkeys = slices.DeleteFunc(l.Passkeys, func(p passkey) bool { return p.Origin == pc.origin })
		l.Passkeys = append(l.Passkeys, passkey{Origin: pc.origin, Credential: *cred, CreatedAt: time.Now().UnixMilli()})
	})
	switch {
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "could not save the fingerprint")
		return
	case !found:
		writeError(w, http.StatusUnauthorized, "this phone was removed from tmx")
		return
	}
	s.log.Info("phone lock: passkey added", "id", pc.id, "origin", pc.origin)
	writeJSON(w, http.StatusOK, s.lockStatus(r))
}

// removePasskey: DELETE /api/devices/lock/passkey: no more fingerprint at this address. Only
// while unlocked.
func (s *Server) removePasskey(w http.ResponseWriter, r *http.Request) {
	pc, ok := s.passkeyCtx(w, r)
	if !ok {
		return
	}
	if s.deviceLocked(pc.id) {
		refuseLocked(w)
		return
	}
	if _, err := s.devices.updateLock(pc.id, func(l *deviceLock) {
		l.Passkeys = slices.DeleteFunc(l.Passkeys, func(p passkey) bool { return p.Origin == pc.origin })
	}); err != nil {
		writeError(w, http.StatusServiceUnavailable, "could not remove the fingerprint")
		return
	}
	writeJSON(w, http.StatusOK, s.lockStatus(r))
}

// beginPasskeyUnlock: POST /api/devices/lock/passkey/unlock → the options for
// navigator.credentials.get ({publicKey}): this address's passkey, user verification required.
func (s *Server) beginPasskeyUnlock(w http.ResponseWriter, r *http.Request) {
	pc, ok := s.passkeyCtx(w, r)
	if !ok {
		s.log.Warn("phone lock: unlock failed", "with", "passkey", "reason", "invalid_context")
		return
	}
	if len(pc.user.creds) == 0 {
		s.log.Warn("phone lock: unlock failed", "with", "passkey", "reason", "no_passkey")
		writeError(w, http.StatusConflict, "the fingerprint is not set up for this address; use your PIN")
		return
	}
	assertion, session, err := pc.rp.BeginLogin(pc.user, webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		s.log.Warn("phone lock: unlock failed", "with", "passkey", "reason", "challenge_creation_failed")
		writeErr(w, err)
		return
	}
	s.lock.setCeremony(pc.id, true, *session, pc.origin)
	writeJSON(w, http.StatusOK, assertion)
}

// finishPasskeyUnlock: POST /api/devices/lock/passkey/unlock/finish {the assertion as JSON} →
// unlocked.
func (s *Server) finishPasskeyUnlock(w http.ResponseWriter, r *http.Request) {
	// Log only fixed categories: WebAuthn errors may carry assertion/credential data.
	reason := "invalid_context"
	defer func() {
		if reason != "" {
			s.log.Warn("phone lock: unlock failed", "with", "passkey", "reason", reason)
		}
	}()
	pc, ok := s.passkeyCtx(w, r)
	if !ok {
		return
	}
	body, ok := readLockBody(w, r)
	if !ok {
		reason = "unreadable_assertion"
		return
	}
	c, why := s.lock.takeCeremonyReason(pc.id, true, pc.origin)
	if c == nil {
		reason = why
		if why == "wrong_origin" {
			writeError(w, http.StatusForbidden, "the fingerprint did not unlock tmx")
		} else {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "this took too long; try again", "code": "passkey_challenge"})
		}
		return
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(body)
	if err != nil {
		reason = "malformed_assertion"
		writeError(w, http.StatusForbidden, "the fingerprint did not unlock tmx: "+webauthnReason(err))
		return
	}
	cred, err := pc.rp.ValidateLogin(pc.user, c.session, parsed)
	if err != nil {
		reason = passkeyUnlockFailure(err)
		if reason == "unknown_challenge" || reason == "expired_challenge" {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "the fingerprint challenge is no longer valid; try again", "code": "passkey_challenge"})
			return
		}
		writeError(w, http.StatusForbidden, "the fingerprint did not unlock tmx: "+webauthnReason(err))
		return
	}
	if cred.Authenticator.CloneWarning {
		reason = "sign_count_regressed"
		s.log.Warn("phone lock: a passkey's sign count went back; refused", "id", pc.id, "origin", pc.origin)
		writeError(w, http.StatusForbidden, "this fingerprint key may have been copied; use your PIN")
		return
	}
	if _, err := s.devices.updateLock(pc.id, func(l *deviceLock) {
		for i := range l.Passkeys {
			if l.Passkeys[i].Origin == pc.origin && string(l.Passkeys[i].Credential.ID) == string(cred.ID) {
				l.Passkeys[i].Credential.Authenticator.SignCount = cred.Authenticator.SignCount
				l.Passkeys[i].Credential.Flags = cred.Flags
			}
		}
	}); err != nil {
		s.log.Warn("phone lock: cannot note the passkey's use", "err", err)
	}
	s.lock.opened(pc.id)
	reason = ""
	s.log.Info("phone lock: unlocked", "id", pc.id, "with", "passkey")
	writeJSON(w, http.StatusOK, s.lockStatus(r))
}

// Return only fixed labels, never an error's debug data or a credential identifier.
func passkeyUnlockFailure(err error) string {
	var pe *protocol.Error
	if errors.As(err, &pe) {
		if pe.Details == "Session has Expired" {
			return "expired_challenge"
		}
		if pe.Details == "Error validating challenge" {
			return "unknown_challenge"
		}
		if pe.Type == protocol.ErrAssertionSignature.Type {
			return "bad_signature"
		}
	}
	return "assertion_validation_failed"
}

// webauthnReason is the short reason a WebAuthn answer was refused.
func webauthnReason(err error) string {
	var pe *protocol.Error
	if errors.As(err, &pe) && pe.Details != "" {
		return pe.Details
	}
	return err.Error()
}
