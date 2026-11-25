package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/gate"
)

// The same Wi-Fi fallback (phonelan.go). User story: as the owner, when Tailscale does not work
// on my phone I am not stuck: I switch on "same Wi-Fi" on my computer, scan the code at home
// and type my PIN. It is off until I switch it on, and off again means off.

const (
	lanIP    = "192.168.1.50"
	lanHostP = lanIP + ":8787"
)

// wifiFixture is a phone fixture whose machine has one Wi-Fi address; its "network"
// listeners are loopback ones (no test ever binds a real network address).
type wifiFixture struct {
	*deviceFixture
	mu       sync.Mutex
	listened []string
	lns      []net.Listener
	firewall error
}

func newWifiFixture(t *testing.T) *wifiFixture {
	t.Helper()
	f, _ := phoneFixture(t)
	w := &wifiFixture{deviceFixture: f}
	f.setPIN(t, testPIN)
	f.s.SetPhoneAddrs(func() []PhoneAddress {
		return []PhoneAddress{{IP: lanIP, Interface: "Wi-Fi"}, {IP: "10.0.0.7", Interface: "Ethernet"}}
	})
	f.s.lanListen = func(addr string) (net.Listener, error) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		w.mu.Lock()
		w.listened = append(w.listened, addr)
		w.lns = append(w.lns, ln)
		w.mu.Unlock()
		return ln, nil
	}
	f.s.firewallAllow = func(context.Context, int) (string, error) { return "netsh example", w.firewall }
	f.s.lanMu.Lock()
	f.s.lanHS = &http.Server{Handler: f.handler}
	f.s.lanMu.Unlock()
	t.Cleanup(func() { f.s.SetLAN(false) })
	return w
}

// lan is a request that arrives on the machine's network address ip (as a phone on the same
// Wi-Fi makes it), with host as its Host header.
func (w *wifiFixture) lan(t *testing.T, method, path, ip, host string, body any, cookie *http.Cookie, header ...string) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, "http://"+host+path, bytes.NewReader(b))
	r.RemoteAddr = "192.168.1.77:50000"
	r.Header.Set("Origin", "http://"+host)
	r.Header.Set("User-Agent", androidUA)
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, net.Addr(&net.TCPAddr{IP: net.ParseIP(ip), Port: 8787})))
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, r)
	return rec
}

// phone is the usual request of a phone on the Wi-Fi.
func (w *wifiFixture) phone(t *testing.T, method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	return w.lan(t, method, path, lanIP, lanHostP, body, cookie)
}

// put is PUT /api/phone/wifi at the computer, confirming at the owner gate when it asks.
func (w *wifiFixture) put(t *testing.T, on bool) (rec *httptest.ResponseRecorder, asked bool) {
	t.Helper()
	body := map[string]bool{"on": on}
	rec = w.request(t, "PUT", "/api/phone/wifi", "localhost", body, nil, true)
	if rec.Code != http.StatusPreconditionRequired {
		return rec, false
	}
	var v struct{ Confirmation gate.Confirmation }
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if c, _ := w.s.gate.Wait(ctx, v.Confirmation.ID); c.State != gate.StateConfirmed {
		t.Fatal("fake did not confirm")
	}
	return w.request(t, "PUT", "/api/phone/wifi?confirmation="+v.Confirmation.ID, "localhost", body, nil, true), true
}

func (w *wifiFixture) wifiCode(t *testing.T) (host, code string) {
	t.Helper()
	rec, _ := w.ownerPost(t, "/api/phone/connect", map[string]string{"via": "wifi"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("wifi code: %d %s", rec.Code, rec.Body)
	}
	var c PhoneConnection
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	if c.Via != "wifi" || c.Pairing.URL != "http://"+c.Pairing.Host+"/#pair="+c.Pairing.Code {
		t.Fatalf("connection: %+v", c)
	}
	return c.Pairing.Host, c.Pairing.Code
}

func TestSameWifiIsOffUntilTheOwnerSwitchesItOn(t *testing.T) {
	w := newWifiFixture(t)
	// Off: nothing answers on a network address, not even the health check, and no code is made.
	deviceResponse(t, w.phone(t, "GET", "/api/health", nil, nil), 403)
	deviceResponse(t, w.phone(t, "POST", "/api/pair/scan/check", map[string]string{"code": "AAAAAAAAAAAA"}, nil), 403)
	rec, _ := w.ownerPost(t, "/api/phone/connect", map[string]string{"via": "wifi"})
	if p := phoneProblemOf(t, rec); p.Step != "off" {
		t.Fatalf("step %q want off", p.Step)
	}
	if len(w.listened) != 0 {
		t.Fatalf("listened while off: %v", w.listened)
	}
	// Not from a phone, not without the owner.
	deviceResponse(t, w.request(t, "PUT", "/api/phone/wifi", deviceTestHost, map[string]bool{"on": true}, nil, true), 403)
	deviceResponse(t, w.request(t, "PUT", "/api/phone/wifi", "localhost", map[string]bool{"on": true}, nil, false), 401)

	// The owner switches it on at the computer: one confirmation, then tmx listens on the
	// machine's own network addresses.
	rec, asked := w.put(t, true)
	var st WifiStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || rec.Code != 200 || !st.On || !asked || len(st.Addresses) != 2 {
		t.Fatalf("switch on: %d %s asked=%v", rec.Code, rec.Body, asked)
	}
	slices.Sort(w.listened)
	if !slices.Equal(w.listened, []string{"10.0.0.7:8787", lanHostP}) {
		t.Fatalf("listening on %v", w.listened)
	}
	deviceResponse(t, w.phone(t, "GET", "/api/read", nil, nil), 401) // reachable now; still nothing without a session
	// On again: nothing more to confirm, nothing listened twice.
	if _, asked := w.put(t, true); asked || len(w.listened) != 2 {
		t.Fatalf("on twice: asked=%v listened=%v", asked, w.listened)
	}

	// Off: the listeners close and the network answers nothing again; no confirmation needed.
	if rec, asked := w.put(t, false); rec.Code != 200 || asked {
		t.Fatalf("switch off: %d asked=%v", rec.Code, asked)
	}
	for _, ln := range w.lns {
		if _, err := ln.Accept(); err == nil {
			t.Fatal("a network listener is still open")
		}
	}
	deviceResponse(t, w.phone(t, "GET", "/api/health", nil, nil), 403)
}

func TestSameWifiPairsWithCodeAndPINOnly(t *testing.T) {
	w := newWifiFixture(t)
	w.put(t, true)
	host, code := w.wifiCode(t)
	if host != lanHostP {
		t.Fatalf("host %q", host)
	}
	// Another of the machine's addresses can be asked for; one that is not its own cannot.
	rec, _ := w.ownerPost(t, "/api/phone/connect", map[string]string{"via": "wifi", "address": "10.0.0.7"})
	deviceResponse(t, rec, 201)
	rec, _ = w.ownerPost(t, "/api/phone/connect", map[string]string{"via": "wifi", "address": "8.8.8.8"})
	deviceResponse(t, rec, 400)
	host, code = w.wifiCode(t)

	// What the network may NOT do, even with the fallback on:
	scan := map[string]string{"code": code, "name": "Android · Chrome", "pin": testPIN}
	// a Host that is not the address it arrived on (DNS rebinding, a forged localhost);
	deviceResponse(t, w.lan(t, "POST", "/api/pair/scan", lanIP, "evil.example.com:8787", scan, nil), 403)
	deviceResponse(t, w.lan(t, "GET", "/api/health", lanIP, "localhost:8787", nil, nil), 403)
	deviceResponse(t, w.lan(t, "GET", "/api/health", lanIP, "10.0.0.7:8787", nil, nil), 403)
	// a public address of this machine;
	deviceResponse(t, w.lan(t, "GET", "/api/health", "203.0.113.9", "203.0.113.9:8787", nil, nil), 403)
	// any token: the owner's, in a header or as a socket's protocol;
	deviceResponse(t, w.lan(t, "GET", "/api/read", lanIP, lanHostP, nil, nil, "Authorization", "Bearer test-owner"), 403)
	deviceResponse(t, w.lan(t, "GET", "/api/read", lanIP, lanHostP, nil, nil, "Upgrade", "websocket", "Sec-WebSocket-Protocol", "tmx, bearer.test-owner"), 401)
	// pairing without a code, or the https-only bootstrap;
	deviceResponse(t, w.phone(t, "POST", "/api/pair/start", map[string]string{"name": "x"}, nil), 403)
	deviceResponse(t, w.phone(t, "POST", "/api/pair/pin", map[string]string{"pin": testPIN}, nil), 403)
	deviceResponse(t, w.phone(t, "POST", "/api/pair/bootstrap/exchange", map[string]string{"token": "x"}, nil), 403)
	// another site's page writing to it;
	deviceResponse(t, w.lan(t, "POST", "/api/pair/scan", lanIP, lanHostP, scan, nil, "Origin", "http://evil.example.com"), 403)
	// a code without the PIN, or made for another address.
	deviceResponse(t, w.phone(t, "POST", "/api/pair/scan", map[string]string{"code": code, "pin": "000000"}, nil), 403)
	deviceResponse(t, w.lan(t, "POST", "/api/pair/scan", "10.0.0.7", "10.0.0.7:8787", scan, nil), 404)
	deviceResponse(t, w.request(t, "POST", "/api/pair/scan", deviceTestHost, scan, nil, false), 404)

	// The phone: code + PIN, and it is in.
	deviceResponse(t, w.phone(t, "POST", "/api/pair/scan/check", map[string]string{"code": code}, nil), 200)
	rec = w.phone(t, "POST", "/api/pair/scan", scan, nil)
	deviceResponse(t, rec, 201)
	cookie := responseCookie(t, rec, lanCookie)
	if cookie.Name != lanCookie+"-8787" || cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge <= 0 {
		t.Fatalf("cookie: %+v", cookie)
	}
	deviceResponse(t, w.phone(t, "GET", "/api/read", nil, cookie), 204)
	// Owner actions work from it (the PIN was just typed), also through the PIN sheet later.
	deviceResponse(t, w.phone(t, "POST", "/api/protected", map[string]string{"a": "b"}, cookie), 204)
	// The session is this address's alone: not the https address, not another address.
	https := &http.Cookie{Name: deviceCookie + "-8443", Value: cookie.Value}
	deviceResponse(t, w.request(t, "GET", "/api/read", deviceTestHost, nil, https, false), 401)
	deviceResponse(t, w.lan(t, "GET", "/api/read", "10.0.0.7", "10.0.0.7:8787", nil, cookie), 401)
	// It locks like any phone, opens with the PIN; no fingerprint without https.
	var st LockStatus
	rec = w.phone(t, "GET", "/api/devices/lock", nil, cookie)
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || !st.Device || st.PasskeyHere {
		t.Fatalf("lock status: %s", rec.Body)
	}
	deviceResponse(t, w.phone(t, "POST", "/api/devices/lock/passkey/register", nil, cookie), 409)
	// The phone cannot make codes or switch the fallback.
	deviceResponse(t, w.phone(t, "POST", "/api/phone/connect", map[string]string{"via": "wifi"}, cookie), 403)
	deviceResponse(t, w.phone(t, "PUT", "/api/phone/wifi", map[string]bool{"on": false}, cookie), 403)

	// A phone paired over https at the same time.
	tsHost, tsCode, _ := w.connect(t)
	rec = w.scan(t, tsHost, tsCode, testPIN)
	deviceResponse(t, rec, 201)
	secure := responseCookie(t, rec, deviceCookie)

	// An open request of the Wi-Fi phone ends when the fallback is switched off; the phone is
	// removed, the https one stays.
	live := httptest.NewRequest("GET", "http://"+lanHostP+"/api/read", nil)
	live.AddCookie(cookie)
	live = live.WithContext(context.WithValue(live.Context(), http.LocalAddrContextKey, net.Addr(&net.TCPAddr{IP: net.ParseIP(lanIP), Port: 8787})))
	ctx, cleanup, ok := w.s.deviceSession(live)
	if !ok {
		t.Fatal("no session for the Wi-Fi phone")
	}
	defer cleanup()
	w.put(t, false)
	if ctx.Err() == nil {
		t.Fatal("the Wi-Fi phone's open request did not end")
	}
	deviceResponse(t, w.phone(t, "GET", "/api/read", nil, cookie), 403)
	deviceResponse(t, w.request(t, "GET", "/api/read", tsHost, nil, secure, false), 204)
	// On again: the old session does not come back.
	w.put(t, true)
	deviceResponse(t, w.phone(t, "GET", "/api/read", nil, cookie), 401)
	var list []map[string]any
	rec = w.request(t, "GET", "/api/devices", "localhost", nil, nil, true)
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 1 {
		t.Fatalf("devices: %s", rec.Body)
	}
}

func TestSameWifiFirewallAndStartup(t *testing.T) {
	w := newWifiFixture(t)
	// The firewall rule could not be added: the answer says so with the command; the fallback
	// stays on, so trying again is enough.
	w.firewall = errors.New("the firewall rule was not added")
	rec, _ := w.put(t, true)
	if p := phoneProblemOf(t, rec); p.Step != "firewall" || p.Output != "netsh example" || !w.s.LANOn() {
		t.Fatalf("firewall problem: %+v on=%v", p, w.s.LANOn())
	}
	w.firewall = nil
	if rec, asked := w.put(t, true); rec.Code != 200 || asked {
		t.Fatalf("again: %d asked=%v", rec.Code, asked)
	}

	// A daemon that starts with the fallback off keeps no device that was paired over it.
	host, code := w.wifiCode(t)
	deviceResponse(t, w.phone(t, "POST", "/api/pair/scan", map[string]string{"code": code, "pin": testPIN}, nil), 201)
	_ = host
	d := newDeviceStore(w.s.devices.path)
	if len(d.state.Devices) != 1 || !d.state.Devices[0].LAN {
		t.Fatalf("kept: %+v", d.state.Devices)
	}
	d.clearLAN()
	if len(newDeviceStore(w.s.devices.path).state.Devices) != 0 {
		t.Fatal("a device paired over the Wi-Fi survived a start with it off")
	}
}

func TestPhoneAddressesSkipVirtualAdapters(t *testing.T) {
	for _, name := range []string{"Tailscale", "tailscale0", "vEthernet (WSL)", "vEthernet (Default Switch)", "VMware Network Adapter VMnet8", "VirtualBox Host-Only Network", "docker0", "utun3", "Bluetooth Network Connection", "wg0"} {
		if !virtualIface(name) {
			t.Errorf("%q should be skipped", name)
		}
	}
	for _, name := range []string{"Wi-Fi", "WLAN", "Ethernet", "Ethernet 2", "en0", "eth0", "wlp3s0"} {
		if virtualIface(name) {
			t.Errorf("%q should be kept", name)
		}
	}
	if !(ifaceRank("en0") < ifaceRank("Wi-Fi") && ifaceRank("Wi-Fi") < ifaceRank("Ethernet") && ifaceRank("WLAN") < ifaceRank("Ethernet") && ifaceRank("Ethernet") < ifaceRank("other")) {
		t.Fatal("Wi-Fi comes before Ethernet, both before the rest")
	}
	for _, a := range phoneAddrs() {
		if ip := net.ParseIP(a.IP); ip == nil || !ip.IsPrivate() || strings.Contains(a.IP, ":") {
			t.Fatalf("not a private IPv4 address: %+v", a)
		}
	}
}
