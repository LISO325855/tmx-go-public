package api

// One-scan phone pairing (docs/PHONE-PAIRING.md). The owner clicks "Connect a phone" at the
// computer: POST /api/phone/connect (phone.go) makes a one-time code, shown as a QR of
// https://<trusted host>/#pair=<code>. The phone's camera opens it; the page sends the code,
// the phone's name and the owner PIN in ONE request (POST /api/pair/scan, no session yet) and
// gets its own device session cookie (devices.go). The code stays in the URL's fragment, which
// a browser never sends, and it is not enough alone: the owner PIN is checked every time.
//
// One code at a time (a new one replaces it), 10 minutes, single use, in memory only and only
// as a SHA-256, bound to the address it was made for. Wrong codes are rate limited for everyone
// together (the proxy makes every phone look like 127.0.0.1). The PIN is only checked after
// the code, so nobody without the code on the owner's screen can run up the PIN's wrong tries.
//
// Ported from Mustneer's v0.1.20 pairing.go onto our device store and owner PIN.

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	scanCodeTTL   = 10 * time.Minute
	scanCodeFails = 10 // wrong codes a minute; more answer 429 until the minute is over
	scanCodeLen   = 12 // base32 characters: 60 random bits
)

var (
	errScanCodeUnknown = errors.New("this code expired or was already used; click Connect a phone on your computer again")
	errScanCodeRate    = errors.New("too many wrong codes; wait a minute")
)

// pairingCodes is the code shown now and the count of wrong ones.
type pairingCodes struct {
	now func() time.Time // nil = time.Now

	mu        sync.Mutex
	hash      [32]byte // SHA-256 of the code; zero = none
	host      string   // the address the code was made for (normalized)
	expires   time.Time
	failStart time.Time
	fails     int
}

func (p *pairingCodes) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

// issue makes a new code for host, replacing the one shown before.
func (p *pairingCodes) issue(host string) (code string, expires time.Time) {
	code = rand.Text()[:scanCodeLen]
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hash = sha256.Sum256([]byte(code))
	p.host = host
	p.expires = p.clock().Add(scanCodeTTL)
	return code, p.expires
}

func (p *pairingCodes) cancel() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hash, p.host, p.expires = [32]byte{}, "", time.Time{}
}

// matchLocked reports whether code is the current, unexpired code for host.
func (p *pairingCodes) matchLocked(code, host string) bool {
	h := sha256.Sum256([]byte(code))
	return p.hash != [32]byte{} && p.clock().Before(p.expires) && subtle.ConstantTimeCompare(h[:], p.hash[:]) == 1 && host == p.host
}

// check tells whether code would pair at host, without using it up; a wrong code counts
// against the limit.
func (p *pairingCodes) check(code, host string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock()
	if now.Sub(p.failStart) >= time.Minute {
		p.failStart, p.fails = now, 0
	}
	if p.fails >= scanCodeFails {
		return errScanCodeRate
	}
	if !p.matchLocked(code, host) {
		p.fails++
		return errScanCodeUnknown
	}
	return nil
}

// take uses the code up: true for exactly one caller while it is valid.
func (p *pairingCodes) take(code, host string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.matchLocked(code, host) {
		return false
	}
	p.hash, p.host, p.expires = [32]byte{}, "", time.Time{}
	return true
}

// cleanCode accepts what a person may type: lowercase, spaces, dashes.
func cleanCode(s string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' {
			return -1
		}
		return r
	}, strings.ToUpper(strings.TrimSpace(s)))
}

// scanRequest is POST /api/pair/scan's body.
type scanRequest struct {
	Code string `json:"code"`
	Name string `json:"name"`
	PIN  string `json:"pin"`
}

// openScanCode checks the code for this address; false: the answer is written.
func (s *Server) openScanCode(w http.ResponseWriter, r *http.Request, code string) bool {
	switch err := s.pairing.check(code, requestHost(r)); {
	case errors.Is(err, errScanCodeRate):
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, err.Error())
		return false
	case err != nil:
		writeError(w, http.StatusNotFound, err.Error())
		return false
	}
	return true
}

// checkScan: POST /api/pair/scan/check {code} → 200 {ok, pin_set}. The phone's page asks this
// as it opens, so an expired code says so before the owner types a PIN.
func (s *Server) checkScan(w http.ResponseWriter, r *http.Request) {
	if !s.deviceReady(w, r) {
		return
	}
	var req scanRequest
	if !decode(w, r, &req) || !s.openScanCode(w, r, cleanCode(req.Code)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true, "pin_set": s.pin != nil && s.pin.IsSet()})
}

// scanPair: POST /api/pair/scan {code, name, pin} → 201 {id, name} and the device cookie.
// The code, then the owner PIN; both right = this browser is a paired device.
func (s *Server) scanPair(w http.ResponseWriter, r *http.Request) {
	if !s.deviceReady(w, r) {
		return
	}
	var req scanRequest
	if !decode(w, r, &req) {
		return
	}
	code, host := cleanCode(req.Code), requestHost(r)
	d := s.devices
	d.mu.Lock()
	allowed := d.limit("pair-scan", 30)
	d.mu.Unlock()
	if !allowed {
		writeError(w, http.StatusTooManyRequests, "too many pairing attempts; wait a minute")
		return
	}
	if !s.openScanCode(w, r, code) {
		return
	}
	if !s.verifyPIN(w, pinSource(r, ""), req.PIN) {
		return
	}
	name := short(req.Name, 80)
	if name == "" {
		name = "Phone"
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.state.Devices) >= 100 {
		writeError(w, http.StatusConflict, "remove an old device before pairing another")
		return
	}
	if !s.pairing.take(code, host) { // used by a request racing this one, or expired meanwhile
		writeError(w, http.StatusNotFound, errScanCodeUnknown.Error())
		return
	}
	tok := randomDeviceID()
	now := d.now()
	before := d.before()
	v := ownerDevice{ID: randomDeviceID(), Name: name, Host: host, Hash: deviceHash(tok), Created: now, Expires: now.Add(deviceTTL), Kind: deviceKind(r), LAN: viaLAN(r)}
	d.state.Devices = append(d.state.Devices, v)
	if err := d.save(before); err != nil {
		writeError(w, http.StatusServiceUnavailable, "could not save device")
		return
	}
	d.pinProven(v.ID)   // the PIN was just typed: no second prompt for the first owner action
	s.lock.opened(v.ID) // phonelock.go: a device just paired is open
	s.deviceEvent("paired", v.ID)
	s.setSessionCookie(w, r, tok, int(v.Expires.Sub(now).Seconds()))
	writeJSON(w, http.StatusCreated, map[string]string{"id": v.ID, "name": v.Name})
}
