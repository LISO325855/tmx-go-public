package api

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"

	"github.com/blackcode-ch/tmx-go/internal/gate"
)

// The phone lock (phonelock.go). User story: as the owner, when I put my phone down, tmx on it
// locks by itself after a while; I open it again with my fingerprint or my owner PIN, and I am
// never locked out for good while I am away from my computer.

const lockTestAfter = 30 * time.Second
const androidUA = "Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36"
const laptopUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"

// lockedPhone is a paired phone on a daemon with the lock on.
type lockedPhone struct {
	*deviceFixture
	host   string
	cookie *http.Cookie
}

func newLockedPhone(t *testing.T) *lockedPhone {
	t.Helper()
	f, _ := phoneFixture(t)
	f.setPIN(t, testPIN)
	f.s.SetLockClock(func() time.Time { return f.now })
	f.s.SetPhoneLock(true, lockTestAfter)
	f.ua = androidUA
	host, code, _ := f.connect(t)
	w := f.scan(t, host, code, testPIN)
	deviceResponse(t, w, 201)
	return &lockedPhone{deviceFixture: f, host: host, cookie: responseCookie(t, w, deviceCookie)}
}

// phone is a request from the phone's browser.
func (p *lockedPhone) phone(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return p.request(t, method, path, p.host, body, p.cookie, false)
}

func (p *lockedPhone) status(t *testing.T) LockStatus {
	t.Helper()
	w := p.phone(t, "GET", "/api/devices/lock", nil)
	if w.Code != 200 {
		t.Fatalf("lock status: %d %s", w.Code, w.Body)
	}
	var st LockStatus
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func (p *lockedPhone) wantLocked(t *testing.T, want bool) {
	t.Helper()
	code := 204
	if want {
		code = http.StatusLocked
	}
	if w := p.phone(t, "GET", "/api/read", nil); w.Code != code {
		t.Fatalf("GET /api/read: %d want %d: %s", w.Code, code, w.Body)
	}
	if st := p.status(t); st.Locked != want {
		t.Fatalf("status.locked %v want %v", st.Locked, want)
	}
}

func (p *lockedPhone) pass(d time.Duration) {
	p.now = p.now.Add(d)
	p.s.SweepLocks()
}

func TestPhoneLockLocksInTheBackgroundAndThePINOpensIt(t *testing.T) {
	p := newLockedPhone(t)
	// Just paired: open.
	if st := p.status(t); !st.Device || !st.Enabled || st.Locked || !st.PINSet || st.AfterSeconds != 30 || !st.PasskeyHere || st.Passkey || st.HeartbeatMS != 10000 {
		t.Fatalf("after pairing: %+v", st)
	}
	p.wantLocked(t, false)

	// Away for less than "Lock after", then back: still open.
	deviceResponse(t, p.phone(t, "POST", "/api/devices/lock/hidden", nil), 204)
	p.pass(lockTestAfter - time.Second)
	deviceResponse(t, p.phone(t, "POST", "/api/devices/lock/alive", nil), 200)
	p.wantLocked(t, false)

	// An open request of the phone (a socket) ends the moment it locks.
	live := httptest.NewRequest("GET", "http://"+p.host+"/api/read", nil)
	live.RemoteAddr = "127.0.0.1:42"
	live.Header.Set("X-Forwarded-Proto", "https")
	live.AddCookie(p.cookie)
	ctx, cleanup, ok := p.s.deviceSession(live)
	if !ok {
		t.Fatal("no device session")
	}
	defer cleanup()

	// Away for longer: locked. Everything but the lock's own routes answers 423.
	deviceResponse(t, p.phone(t, "POST", "/api/devices/lock/hidden", nil), 204)
	p.pass(lockTestAfter + time.Second)
	if ctx.Err() == nil {
		t.Fatal("the phone's open request did not end when it locked")
	}
	p.wantLocked(t, true)
	w := p.phone(t, "POST", "/api/protected", map[string]string{"a": "b"})
	if v := deviceResponse(t, w, http.StatusLocked); string(v["locked"]) != "true" {
		t.Fatalf("423 body: %s", w.Body)
	}
	deviceResponse(t, p.phone(t, "POST", "/api/devices/lock/alive", nil), 200) // coming back does not open it
	p.wantLocked(t, true)

	// The computer itself is never locked, and has no lock of its own.
	deviceResponse(t, p.request(t, "GET", "/api/read", "localhost", nil, nil, true), 204)
	deviceResponse(t, p.request(t, "POST", "/api/devices/lock", "localhost", nil, nil, true), 403)
	deviceResponse(t, p.request(t, "POST", "/api/devices/lock/unlock", "localhost", map[string]string{"pin": testPIN}, nil, true), 403)

	// A wrong PIN keeps it locked; the right one opens it, and counts as the PIN for owner actions.
	deviceResponse(t, p.phone(t, "POST", "/api/devices/lock/unlock", map[string]string{"pin": "000000"}), 403)
	p.wantLocked(t, true)
	w = p.phone(t, "POST", "/api/devices/lock/unlock", map[string]string{"pin": testPIN})
	if v := deviceResponse(t, w, 200); string(v["locked"]) != "false" {
		t.Fatalf("unlock: %s", w.Body)
	}
	p.wantLocked(t, false)
	deviceResponse(t, p.phone(t, "POST", "/api/protected", map[string]string{"a": "b"}), 204)

	// Lock now.
	w = p.phone(t, "POST", "/api/devices/lock", nil)
	if v := deviceResponse(t, w, 200); string(v["locked"]) != "true" {
		t.Fatalf("lock now: %s", w.Body)
	}
	p.wantLocked(t, true)
}

// Wrong PINs make the phone wait, as the owner PIN does everywhere; they never sign it out, so
// the owner is not locked out for good while away.
func TestPhoneLockWrongPINsWaitButNeverSignOut(t *testing.T) {
	p := newLockedPhone(t)
	deviceResponse(t, p.phone(t, "POST", "/api/devices/lock", nil), 200)
	waited := false
	for i := 0; i < 25; i++ {
		w := p.phone(t, "POST", "/api/devices/lock/unlock", map[string]string{"pin": "000000"})
		switch w.Code {
		case 403:
		case 429:
			waited = true
			if st := p.status(t); st.RetryAfterMS <= 0 {
				t.Fatalf("no wait told: %+v", st)
			}
			p.now = p.now.Add(2 * time.Hour) // the wait is over
		default:
			t.Fatalf("wrong PIN %d: %d %s", i, w.Code, w.Body)
		}
	}
	if !waited {
		t.Fatal("25 wrong PINs never had to wait")
	}
	p.now = p.now.Add(2 * time.Hour)
	// Still paired, still locked, and the right PIN still opens it.
	p.wantLocked(t, true)
	deviceResponse(t, p.phone(t, "POST", "/api/devices/lock/unlock", map[string]string{"pin": testPIN}), 200)
	p.wantLocked(t, false)
}

func TestPhoneLockBackstopHeartbeatAndImmediately(t *testing.T) {
	p := newLockedPhone(t)
	// The page never said it went away (phones often drop that request): no request for
	// "Lock after" locks it all the same.
	p.pass(lockTestAfter - time.Second)
	p.wantLocked(t, false) // (this request counts as a sign of life)
	p.pass(lockTestAfter + time.Second)
	p.wantLocked(t, true)
	deviceResponse(t, p.phone(t, "POST", "/api/devices/lock/unlock", map[string]string{"pin": testPIN}), 200)

	// A page in front says it is alive every 10 s: it stays open for as long as it is used.
	for i := 0; i < 30; i++ {
		p.pass(lockHeartbeat)
		deviceResponse(t, p.phone(t, "POST", "/api/devices/lock/alive", nil), 200)
	}
	p.wantLocked(t, false)

	// A short "Lock after" never locks a phone that is in front between two heartbeats.
	p.s.SetPhoneLock(true, 0)
	p.pass(lockIdleMin - time.Second)
	p.wantLocked(t, false)
	// "Immediately": leaving the page locks at once.
	deviceResponse(t, p.phone(t, "POST", "/api/devices/lock/hidden", nil), 204)
	p.wantLocked(t, true)
}

func TestPhoneLockAfterADaemonStartOffAndWithoutAPIN(t *testing.T) {
	p := newLockedPhone(t)
	// A daemon start: the lock's state is in memory, so every phone starts locked.
	p.s.lock = phoneLock{}
	p.s.SetLockClock(func() time.Time { return p.now })
	p.s.SetPhoneLock(true, lockTestAfter)
	p.s.devices = newDeviceStore(p.s.devices.path)
	p.s.devices.now = func() time.Time { return p.now }
	p.wantLocked(t, true)
	deviceResponse(t, p.phone(t, "POST", "/api/devices/lock/unlock", map[string]string{"pin": testPIN}), 200)
	p.wantLocked(t, false)

	// Switched off: nothing locks, and a locked phone is open again.
	deviceResponse(t, p.phone(t, "POST", "/api/devices/lock", nil), 200)
	p.s.SetPhoneLock(false, lockTestAfter)
	p.wantLocked(t, false)
	p.pass(time.Hour)
	p.wantLocked(t, false)
	deviceResponse(t, p.phone(t, "POST", "/api/devices/lock", nil), 409)
	if st := p.status(t); st.Enabled {
		t.Fatalf("status: %+v", st)
	}
}

func TestPhoneLockSettingIsTheOwnersAtTheComputer(t *testing.T) {
	p := newLockedPhone(t)
	w := p.request(t, "GET", "/api/phone/lock", "localhost", nil, nil, true)
	var st LockSetting
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil || w.Code != 200 || !st.Enabled || st.AfterSeconds != 30 || !st.PINSet || len(st.Choices) == 0 {
		t.Fatalf("setting: %d %s", w.Code, w.Body)
	}
	put := func(body any) *httptest.ResponseRecorder {
		w := p.request(t, "PUT", "/api/phone/lock", "localhost", body, nil, true)
		if w.Code != http.StatusPreconditionRequired {
			return w
		}
		var v struct{ Confirmation gate.Confirmation }
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if c, _ := p.s.gate.Wait(ctx, v.Confirmation.ID); c.State != gate.StateConfirmed {
			t.Fatal("fake did not confirm")
		}
		return p.request(t, "PUT", "/api/phone/lock?confirmation="+v.Confirmation.ID, "localhost", body, nil, true)
	}
	deviceResponse(t, put(map[string]any{"enabled": true, "after_seconds": 45}), 400)
	deviceResponse(t, put(map[string]any{"enabled": true, "after_seconds": 300}), 200)
	if on, after := p.s.lock.settings(); !on || after != 5*time.Minute {
		t.Fatalf("applied: %v %v", on, after)
	}
	deviceResponse(t, put(map[string]any{"enabled": false, "after_seconds": 300}), 200)
	if on, _ := p.s.lock.settings(); on {
		t.Fatal("still on")
	}
	// Not from the phone: a phone left lying around must not switch its own lock off.
	deviceResponse(t, p.phone(t, "PUT", "/api/phone/lock", map[string]any{"enabled": false, "after_seconds": 300}), 403)
	deviceResponse(t, p.phone(t, "GET", "/api/phone/lock", nil), 403)
}

// ---- the fingerprint (a passkey) ---------------------------------------------------------------

// softKey is a platform authenticator in software (ES256, attestation "none").
type softKey struct {
	priv  *ecdsa.PrivateKey
	id    []byte
	count uint32
	noUV  bool // answer without user verification
}

func newSoftKey(t *testing.T) *softKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return &softKey{priv: priv, id: id}
}

var b64 = base64.RawURLEncoding

type ceremonyOptions struct {
	PublicKey struct {
		Challenge string `json:"challenge"`
		RP        struct {
			ID string `json:"id"`
		} `json:"rp"`
		RPID             string `json:"rpId"`
		AllowCredentials []struct {
			ID string `json:"id"`
		} `json:"allowCredentials"`
		AuthenticatorSelection struct {
			Attachment string `json:"authenticatorAttachment"`
			UV         string `json:"userVerification"`
		} `json:"authenticatorSelection"`
		UV string `json:"userVerification"`
	} `json:"publicKey"`
}

func (k *softKey) authData(rpID string, attested bool) []byte {
	h := sha256.Sum256([]byte(rpID))
	flags := byte(0x01) // user present
	if !k.noUV {
		flags |= 0x04 // user verified
	}
	if attested {
		flags |= 0x40
	}
	out := append(h[:], flags)
	out = binary.BigEndian.AppendUint32(out, k.count)
	if attested {
		out = append(out, make([]byte, 16)...) // AAGUID
		out = binary.BigEndian.AppendUint16(out, uint16(len(k.id)))
		out = append(out, k.id...)
		pub := k.priv.PublicKey
		x, y := make([]byte, 32), make([]byte, 32)
		pub.X.FillBytes(x)
		pub.Y.FillBytes(y)
		cose, err := webauthncbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
		if err != nil {
			panic(err)
		}
		out = append(out, cose...)
	}
	return out
}

func clientData(typ, challenge, origin string) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": origin, "crossOrigin": false})
	return b
}

// create answers navigator.credentials.create at origin.
func (k *softKey) create(t *testing.T, opts ceremonyOptions, origin string) map[string]any {
	t.Helper()
	att, err := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": k.authData(opts.PublicKey.RP.ID, true)})
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"id": b64.EncodeToString(k.id), "rawId": b64.EncodeToString(k.id), "type": "public-key", "authenticatorAttachment": "platform",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(clientData("webauthn.create", opts.PublicKey.Challenge, origin)),
			"attestationObject": b64.EncodeToString(att),
			"transports":        []string{"internal"},
		},
		"clientExtensionResults": map[string]any{},
	}
}

// get answers navigator.credentials.get at origin for rpID.
func (k *softKey) get(t *testing.T, opts ceremonyOptions, origin, rpID string) map[string]any {
	t.Helper()
	k.count++
	ad := k.authData(rpID, false)
	cd := clientData("webauthn.get", opts.PublicKey.Challenge, origin)
	cdh := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), cdh[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, k.priv, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"id": b64.EncodeToString(k.id), "rawId": b64.EncodeToString(k.id), "type": "public-key", "authenticatorAttachment": "platform",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(cd),
			"authenticatorData": b64.EncodeToString(ad),
			"signature":         b64.EncodeToString(sig),
		},
		"clientExtensionResults": map[string]any{},
	}
}

func (p *lockedPhone) options(t *testing.T, path string) ceremonyOptions {
	t.Helper()
	w := p.phone(t, "POST", path, nil)
	if w.Code != 200 {
		t.Fatalf("%s: %d %s", path, w.Code, w.Body)
	}
	var opts ceremonyOptions
	if err := json.Unmarshal(w.Body.Bytes(), &opts); err != nil {
		t.Fatal(err)
	}
	return opts
}

func (p *lockedPhone) registerPasskey(t *testing.T, k *softKey) {
	t.Helper()
	opts := p.options(t, "/api/devices/lock/passkey/register")
	sel := opts.PublicKey.AuthenticatorSelection
	rpID, _, _ := strings.Cut(p.host, ":")
	if opts.PublicKey.RP.ID != rpID || sel.Attachment != "platform" || sel.UV != "required" || opts.PublicKey.Challenge == "" {
		t.Fatalf("creation options: %+v", opts.PublicKey)
	}
	deviceResponse(t, p.phone(t, "POST", "/api/devices/lock/passkey/register/finish", k.create(t, opts, "https://"+p.host)), 200)
	if st := p.status(t); !st.Passkey {
		t.Fatalf("after registering: %+v", st)
	}
}

// passkeyUnlock asks for a challenge and answers it at origin for rpID.
func (p *lockedPhone) passkeyUnlock(t *testing.T, k *softKey, origin, rpID string) *httptest.ResponseRecorder {
	t.Helper()
	opts := p.options(t, "/api/devices/lock/passkey/unlock")
	if opts.PublicKey.UV != "required" || len(opts.PublicKey.AllowCredentials) != 1 || opts.PublicKey.AllowCredentials[0].ID != b64.EncodeToString(k.id) {
		t.Fatalf("request options: %+v", opts.PublicKey)
	}
	return p.phone(t, "POST", "/api/devices/lock/passkey/unlock/finish", k.get(t, opts, origin, rpID))
}

// The fingerprint: a passkey made on the phone opens the lock with a fresh signature; a wrong
// origin, RP ID, a missing user verification or a replayed challenge do not. It opens the
// lock and nothing else: owner actions still ask for the owner PIN.
func TestPhoneLockFingerprint(t *testing.T) {
	p := newLockedPhone(t)
	origin := "https://" + p.host
	rpID, _, _ := strings.Cut(p.host, ":")
	k := newSoftKey(t)

	// No passkey yet: nothing to unlock with.
	deviceResponse(t, p.phone(t, "POST", "/api/devices/lock/passkey/unlock", nil), 409)
	p.registerPasskey(t, k)
	// It is kept with the device, across a daemon restart.
	p.s.devices = newDeviceStore(p.s.devices.path)
	p.s.devices.now = func() time.Time { return p.now }
	if st := p.status(t); !st.Passkey {
		t.Fatalf("after a restart: %+v", st)
	}

	lock := func() {
		t.Helper()
		deviceResponse(t, p.phone(t, "POST", "/api/devices/lock", nil), 200)
		p.wantLocked(t, true)
	}
	lock()
	// Refused: another site's origin, another RP ID, no user verification.
	deviceResponse(t, p.passkeyUnlock(t, k, "https://evil.example.com", rpID), 403)
	deviceResponse(t, p.passkeyUnlock(t, k, origin, "evil.example.com"), 403)
	k.noUV = true
	deviceResponse(t, p.passkeyUnlock(t, k, origin, rpID), 403)
	k.noUV = false
	p.wantLocked(t, true)

	// The real thing opens it.
	opts := p.options(t, "/api/devices/lock/passkey/unlock")
	answer := k.get(t, opts, origin, rpID)
	deviceResponse(t, p.phone(t, "POST", "/api/devices/lock/passkey/unlock/finish", answer), 200)
	p.wantLocked(t, false)
	// But an owner action still needs the PIN: the session's PIN proof is older than "fresh"
	// only after a while, so move on and ask.
	p.now = p.now.Add(13 * time.Hour)
	lock()
	deviceResponse(t, p.passkeyUnlock(t, k, origin, rpID), 200)
	deviceResponse(t, p.phone(t, "POST", "/api/protected", map[string]string{"a": "b"}), 428)

	// A replayed answer (its challenge is used up) does not unlock.
	lock()
	deviceResponse(t, p.phone(t, "POST", "/api/devices/lock/passkey/unlock/finish", answer), 400)
	p.wantLocked(t, true)

	// Another key does not open it.
	other := newSoftKey(t)
	opts = p.options(t, "/api/devices/lock/passkey/unlock")
	deviceResponse(t, p.phone(t, "POST", "/api/devices/lock/passkey/unlock/finish", other.get(t, opts, origin, rpID)), 403)

	// While locked, no passkey can be added or removed (else a thief adds their own finger).
	deviceResponse(t, p.phone(t, "POST", "/api/devices/lock/passkey/register", nil), http.StatusLocked)
	deviceResponse(t, p.phone(t, "DELETE", "/api/devices/lock/passkey", nil), http.StatusLocked)

	// The PIN always works too; then the passkey can be removed.
	deviceResponse(t, p.phone(t, "POST", "/api/devices/lock/unlock", map[string]string{"pin": testPIN}), 200)
	deviceResponse(t, p.phone(t, "DELETE", "/api/devices/lock/passkey", nil), 200)
	if st := p.status(t); st.Passkey {
		t.Fatalf("after removing: %+v", st)
	}
}

func TestPhoneLockChallengeFailures(t *testing.T) {
	for _, scenario := range []string{"expired_challenge", "unknown_challenge", "unknown_or_consumed_challenge", "bad_signature"} {
		t.Run(scenario, func(t *testing.T) {
			p := newLockedPhone(t)
			k := newSoftKey(t)
			p.registerPasskey(t, k)
			deviceResponse(t, p.phone(t, "POST", "/api/devices/lock", nil), 200)
			origin := "https://" + p.host
			rpID, _, _ := strings.Cut(p.host, ":")
			opts := p.options(t, "/api/devices/lock/passkey/unlock")
			answer := k.get(t, opts, origin, rpID)
			code := http.StatusBadRequest
			switch scenario {
			case "expired_challenge":
				p.now = p.now.Add(lockCeremonyTTL)
			case "unknown_challenge":
				p.options(t, "/api/devices/lock/passkey/unlock") // supersede the first challenge
				code = http.StatusForbidden
			case "unknown_or_consumed_challenge":
				deviceResponse(t, p.phone(t, "POST", "/api/devices/lock/passkey/unlock/finish", answer), 200)
				deviceResponse(t, p.phone(t, "POST", "/api/devices/lock", nil), 200)
			case "bad_signature":
				other := newSoftKey(t)
				other.id = k.id // the allowed credential ID, signed with the wrong key
				answer = other.get(t, opts, origin, rpID)
				code = http.StatusForbidden
			}
			var logs bytes.Buffer
			p.s.log = slog.New(slog.NewTextHandler(&logs, nil))
			w := p.phone(t, "POST", "/api/devices/lock/passkey/unlock/finish", answer)
			deviceResponse(t, w, code)
			p.wantLocked(t, true)
			if strings.Contains(w.Body.String(), `"code":"passkey_challenge"`) != (scenario != "bad_signature") {
				t.Fatalf("wrong retry classification: %s", w.Body)
			}
			if !strings.Contains(logs.String(), "reason="+scenario) {
				t.Fatalf("missing failure reason: %s", logs.String())
			}
			for _, secret := range []string{opts.PublicKey.Challenge, b64.EncodeToString(k.id), answer["response"].(map[string]any)["signature"].(string)} {
				if strings.Contains(logs.String(), secret) {
					t.Fatal("failure log contains assertion data")
				}
			}
			// Failed assertions consume their challenge too; only a fresh one can recover.
			deviceResponse(t, p.phone(t, "POST", "/api/devices/lock/passkey/unlock/finish", answer), 400)
			deviceResponse(t, p.passkeyUnlock(t, k, origin, rpID), 200)
			p.wantLocked(t, false)
		})
	}
}

// Only phones and tablets lock. A paired computer's browser never does, and what a device is
// is decided when it pairs: "Desktop site" on a paired phone does not take its lock away.
func TestPhoneLockIsForPhonesNotComputers(t *testing.T) {
	p := newLockedPhone(t)
	// The paired phone asks as a desktop browser: still a phone.
	p.ua = laptopUA
	deviceResponse(t, p.phone(t, "POST", "/api/devices/lock", nil), 200)
	p.wantLocked(t, true)

	// A laptop pairs: no lock, whatever the time, and no lock routes of its own.
	host, code, _ := p.connect(t)
	w := p.scan(t, host, code, testPIN)
	deviceResponse(t, w, 201)
	laptop := responseCookie(t, w, deviceCookie)
	p.pass(24 * time.Hour)
	deviceResponse(t, p.request(t, "GET", "/api/read", host, nil, laptop, false), 204)
	w = p.request(t, "GET", "/api/devices/lock", host, nil, laptop, false)
	var st LockStatus
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil || w.Code != 200 || st.Device || st.Locked {
		t.Fatalf("laptop lock status: %d %s", w.Code, w.Body)
	}
	deviceResponse(t, p.request(t, "POST", "/api/devices/lock", host, nil, laptop, false), 403)

	// A device paired before kinds were kept is what its next request says, from then on.
	p.s.devices.mu.Lock()
	for i := range p.s.devices.state.Devices {
		p.s.devices.state.Devices[i].Kind = ""
	}
	p.s.devices.mu.Unlock()
	p.ua = androidUA
	deviceResponse(t, p.request(t, "GET", "/api/devices/lock", host, nil, laptop, false), 200)
	p.ua = laptopUA
	if w := p.request(t, "GET", "/api/read", host, nil, laptop, false); w.Code != http.StatusLocked {
		t.Fatalf("a phone paired before: %d %s", w.Code, w.Body)
	}
}
