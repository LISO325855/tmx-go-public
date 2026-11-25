package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/gate"
	"github.com/blackcode-ch/tmx-go/internal/tailscale"
)

// Connect a phone (phone.go, phonecode.go). User story: as the owner, I click Connect a phone
// on my computer, point my phone's camera at the code, type my PIN, and I am in: at home and
// away, first try, without editing a config file or running a Tailscale command.

const phoneTailnetName = "box.example.ts.net"

// fakeTailscale is a Tailscale CLI that remembers what it serves.
type fakeTailscale struct {
	installed bool
	state     string         // BackendState
	name      string         // this machine's MagicDNS name ("" = MagicDNS off)
	phone     bool           // a phone is online on the tailnet
	served    map[int]string // HTTPS port -> target
	tcp       []int          // ports forwarded as plain TCP (not in the Web list)
	statusErr string         // what `serve status` prints instead of answering
	serveErr  string         // what `serve` prints instead of working
	calls     []string
	clobbered []int // ports tmx asked to serve on although something was served there
}

func newFakeTailscale() *fakeTailscale {
	return &fakeTailscale{installed: true, state: "Running", name: phoneTailnetName, phone: true, served: map[int]string{}}
}

var serveArgs = regexp.MustCompile(`^serve --bg --yes --https=(\d+) (\S+)$`)

func (f *fakeTailscale) run(_ context.Context, _ string, args []string, _ func([]byte) bool) ([]byte, []byte, error) {
	line := strings.Join(args, " ")
	f.calls = append(f.calls, line)
	switch {
	case line == "status --json":
		peers := `{}`
		if f.phone {
			peers = `{"nodekey:p":{"HostName":"pixel","DNSName":"pixel.example.ts.net.","OS":"android","Online":true}}`
		}
		dns := ""
		if f.name != "" {
			dns = f.name + "."
		}
		return []byte(fmt.Sprintf(`{"BackendState":%q,"Self":{"DNSName":%q},"Peer":%s}`, f.state, dns, peers)), nil, nil
	case line == "serve status --json":
		if f.statusErr != "" {
			return nil, []byte(f.statusErr), errors.New("exit status 1")
		}
		web, tcp := map[string]any{}, map[string]any{}
		for port, target := range f.served {
			web[fmt.Sprintf("%s:%d", f.name, port)] = map[string]any{"Handlers": map[string]any{"/": map[string]string{"Proxy": target}}}
			tcp[fmt.Sprint(port)] = map[string]bool{"HTTPS": true}
		}
		for _, port := range f.tcp {
			tcp[fmt.Sprint(port)] = map[string]string{"TCPForward": "127.0.0.1:22"}
		}
		b, _ := json.Marshal(map[string]any{"TCP": tcp, "Web": web})
		return b, nil, nil
	}
	if m := serveArgs.FindStringSubmatch(line); m != nil {
		if f.serveErr != "" {
			return nil, []byte(f.serveErr), errors.New("exit status 1")
		}
		var port int
		fmt.Sscan(m[1], &port)
		if _, used := f.served[port]; used || slices.Contains(f.tcp, port) {
			f.clobbered = append(f.clobbered, port) // the real CLI would replace what is there
		}
		f.served[port] = m[2]
		return nil, nil, nil
	}
	return nil, []byte("unknown command"), errors.New("exit status 1")
}

func (f *fakeTailscale) client() *tailscale.Client {
	if !f.installed {
		return &tailscale.Client{Run: f.run}
	}
	return &tailscale.Client{Path: "tailscale", Run: f.run}
}

// phoneFixture is a device fixture with a fake Tailscale and a self-check that answers.
func phoneFixture(t *testing.T) (*deviceFixture, *fakeTailscale) {
	t.Helper()
	f := newDeviceFixture(t)
	ts := newFakeTailscale()
	f.s.SetTailscale(ts.client)
	// In every test: tmx only ever adds a route on a port nothing is served on.
	t.Cleanup(func() {
		if len(ts.clobbered) > 0 {
			t.Errorf("tmx asked Tailscale to serve on ports already in use: %v", ts.clobbered)
		}
	})
	f.s.SetTailscaleProbe(func(context.Context, string) error { return nil })
	f.s.pairing.now = func() time.Time { return f.now }
	return f, ts
}

// ownerPost is a request from the owner's UI at the computer; when the owner gate asks, the
// fake authenticator confirms and the request is sent again with the confirmation.
func (f *deviceFixture) ownerPost(t *testing.T, path string, body any) (w *httptest.ResponseRecorder, asked bool) {
	t.Helper()
	w = f.request(t, "POST", path, "localhost", body, nil, true)
	if w.Code != http.StatusPreconditionRequired {
		return w, false
	}
	var v struct{ Confirmation gate.Confirmation }
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if c, _ := f.s.gate.Wait(ctx, v.Confirmation.ID); c.State != gate.StateConfirmed {
		t.Fatal("fake did not confirm")
	}
	return f.request(t, "POST", path+"?confirmation="+v.Confirmation.ID, "localhost", body, nil, true), true
}

// connect clicks Connect a phone and returns the code's link parts.
func (f *deviceFixture) connect(t *testing.T) (host, code string, asked bool) {
	t.Helper()
	w, asked := f.ownerPost(t, "/api/phone/connect", map[string]string{"via": "tailscale"})
	if w.Code != http.StatusCreated {
		t.Fatalf("connect: %d %s", w.Code, w.Body)
	}
	var c PhoneConnection
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	if c.Via != "tailscale" || c.Pairing.URL != "https://"+c.Pairing.Host+"/#pair="+c.Pairing.Code || len(c.Pairing.Code) != scanCodeLen {
		t.Fatalf("connection: %+v", c)
	}
	return c.Pairing.Host, c.Pairing.Code, asked
}

func (f *deviceFixture) scan(t *testing.T, host, code, pin string) *httptest.ResponseRecorder {
	t.Helper()
	return f.request(t, "POST", "/api/pair/scan", host, map[string]string{"code": code, "name": "Pixel · Chrome", "pin": pin}, nil, false)
}

func phoneProblemOf(t *testing.T, w *httptest.ResponseRecorder) phoneProblem {
	t.Helper()
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d want 409: %s", w.Code, w.Body)
	}
	var p phoneProblem
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPhoneConnectSetsUpTailscaleAndPairsWithOneScan(t *testing.T) {
	f, ts := phoneFixture(t)

	// Without an owner PIN the phone could never prove the owner: the dialog asks for one first.
	w, _ := f.ownerPost(t, "/api/phone/connect", map[string]string{"via": "tailscale"})
	if p := phoneProblemOf(t, w); p.Step != "pin" {
		t.Fatalf("step %q want pin", p.Step)
	}
	if len(ts.served) != 0 {
		t.Fatal("served before the PIN exists")
	}
	f.setPIN(t, testPIN)

	// One click: tmx serves itself on the tailnet, the owner confirms the new address once,
	// and the code's link points at it.
	host, code, asked := f.connect(t)
	if host != phoneTailnetName || !asked {
		t.Fatalf("host %q asked %v", host, asked)
	}
	if ts.served[443] != "http://127.0.0.1:8787" {
		t.Fatalf("served: %v", ts.served)
	}
	if !slices.Contains(f.s.TrustedHosts(), phoneTailnetName) {
		t.Fatal("the tailnet name was not trusted")
	}
	// A second code: nothing left to set up, so nothing to confirm; the first code is void.
	_, code2, asked := f.connect(t)
	if asked || code2 == code {
		t.Fatalf("asked %v, same code %v", asked, code2 == code)
	}
	deviceResponse(t, f.scan(t, host, code, testPIN), 404)

	// The phone: the page checks the code as it opens, then sends code + PIN.
	deviceResponse(t, f.request(t, "POST", "/api/pair/scan/check", host, map[string]string{"code": code2}, nil, false), 200)
	deviceResponse(t, f.request(t, "POST", "/api/pair/scan/check", host, map[string]string{"code": "AAAAAAAAAAAA"}, nil, false), 404)
	deviceResponse(t, f.scan(t, host, code2, "000000"), 403) // a wrong PIN does not use the code up
	deviceResponse(t, f.scan(t, deviceTestHost, code2, testPIN), 404)
	w = f.scan(t, host, strings.ToLower(code2[:4])+" "+code2[4:], testPIN) // as a person types it
	v := deviceResponse(t, w, 201)
	if responseString(t, v, "name") != "Pixel · Chrome" {
		t.Fatalf("name: %s", w.Body)
	}
	cookie := responseCookie(t, w, deviceCookie)
	if cookie.Name != deviceCookie || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge <= 0 {
		t.Fatalf("cookie: %+v", cookie)
	}
	deviceResponse(t, f.request(t, "GET", "/api/read", host, nil, cookie, false), 204)
	// The PIN was just typed: the first owner action does not ask again.
	deviceResponse(t, f.request(t, "POST", "/api/protected", host, map[string]string{"a": "b"}, cookie, false), 204)
	// Single use.
	deviceResponse(t, f.scan(t, host, code2, testPIN), 404)
	// The session survives a daemon restart.
	f.s.devices = newDeviceStore(f.s.devices.path)
	deviceResponse(t, f.request(t, "GET", "/api/read", host, nil, cookie, false), 204)
}

func TestPhoneConnectCodeExpiresAndWrongCodesAreLimited(t *testing.T) {
	f, _ := phoneFixture(t)
	f.setPIN(t, testPIN)
	host, code, _ := f.connect(t)
	f.now = f.now.Add(scanCodeTTL + time.Second)
	deviceResponse(t, f.scan(t, host, code, testPIN), 404)

	host, code, _ = f.connect(t)
	for i := 0; i < scanCodeFails-1; i++ { // the expired code above was this minute's first wrong one
		deviceResponse(t, f.request(t, "POST", "/api/pair/scan/check", host, map[string]string{"code": "WRONGWRONGWR"}, nil, false), 404)
	}
	deviceResponse(t, f.scan(t, host, code, testPIN), 429) // even the right code waits
	f.now = f.now.Add(time.Minute + time.Second)
	deviceResponse(t, f.scan(t, host, code, testPIN), 201)

	// Closing the dialog voids the code.
	host, code, _ = f.connect(t)
	if w := f.request(t, "DELETE", "/api/phone/connect", "localhost", nil, nil, true); w.Code != 204 {
		t.Fatalf("cancel: %d", w.Code)
	}
	deviceResponse(t, f.scan(t, host, code, testPIN), 404)
}

func TestPhoneConnectUsesAFreePortAndAnAddressAlreadyServed(t *testing.T) {
	f, ts := phoneFixture(t)
	f.setPIN(t, testPIN)
	// Another install already answers on 443 of this machine's name: tmx takes the next port,
	// and the phone's session cookie carries that port so the two installs do not sign each
	// other out (a cookie belongs to a host name, never to a port).
	ts.served[443] = "http://127.0.0.1:8777"
	host, code, asked := f.connect(t)
	if host != phoneTailnetName+":8443" || !asked || ts.served[443] != "http://127.0.0.1:8777" {
		t.Fatalf("host %q asked %v served %v", host, asked, ts.served)
	}
	w := f.scan(t, host, code, testPIN)
	deviceResponse(t, w, 201)
	cookie := responseCookie(t, w, deviceCookie)
	if cookie.Name != deviceCookie+"-8443" {
		t.Fatalf("cookie name %q", cookie.Name)
	}
	r := httptest.NewRequest("GET", "http://"+host+"/api/read", nil)
	r.RemoteAddr = "127.0.0.1:4321"
	r.Header.Set("X-Forwarded-Proto", "https")
	r.AddCookie(&http.Cookie{Name: deviceCookie, Value: strings.Repeat("f", 64)}) // the other install's session
	r.AddCookie(cookie)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, r)
	if rec.Code != 204 {
		t.Fatalf("with another install's cookie: %d %s", rec.Code, rec.Body)
	}
	// A session paired before cookies were named per port still works.
	old := &http.Cookie{Name: deviceCookie, Value: cookie.Value}
	deviceResponse(t, f.request(t, "GET", "/api/read", host, nil, old, false), 204)

	// An address someone set up by hand (any port) and already trusted: used as it is, no
	// new serve, nothing to confirm.
	f2, ts2 := phoneFixture(t)
	f2.setPIN(t, testPIN)
	ts2.name = "trial.example.ts.net"
	ts2.served[8443] = "http://127.0.0.1:8787"
	host, _, asked = f2.connect(t)
	if host != deviceTestHost || asked {
		t.Fatalf("host %q asked %v", host, asked)
	}
	for _, c := range ts2.calls {
		if strings.HasPrefix(c, "serve --bg") {
			t.Fatalf("served again: %v", ts2.calls)
		}
	}
}

func TestPhoneStatusNamesTheMissingStep(t *testing.T) {
	old := tsCheckFail
	tsCheckFail = time.Millisecond
	t.Cleanup(func() { tsCheckFail = old })
	status := func(f *deviceFixture, setup bool) PhoneStatus {
		path := "/api/phone"
		if setup {
			path += "?setup=1"
		}
		w := f.request(t, "GET", path, "localhost", nil, nil, true)
		if w.Code != 200 {
			t.Fatalf("status: %d %s", w.Code, w.Body)
		}
		var st PhoneStatus
		if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
			t.Fatal(err)
		}
		return st
	}
	f, ts := phoneFixture(t)
	f.setPIN(t, testPIN)

	ts.installed = false
	if st := status(f, true); st.Tailscale.Step != "install" || st.Tailscale.Installed || st.Tailscale.Link == "" {
		t.Fatalf("not installed: %+v", st.Tailscale)
	}
	ts.installed, ts.state = true, "NeedsLogin"
	if st := status(f, true); st.Tailscale.Step != "login" {
		t.Fatalf("signed out: %+v", st.Tailscale)
	}
	ts.state, ts.name = "Running", ""
	if st := status(f, true); st.Tailscale.Step != "magicdns" || st.Tailscale.Link != tailscale.AdminDNS {
		t.Fatalf("no MagicDNS: %+v", st.Tailscale)
	}
	ts.name, ts.serveErr = phoneTailnetName, "Serve is not enabled on your tailnet.\nTo enable, visit:\n\n  https://login.tailscale.com/f/serve?node=abc\n"
	if st := status(f, true); st.Tailscale.Step != "enable" || !strings.Contains(st.Tailscale.Link, "/f/serve") {
		t.Fatalf("serve off: %+v", st.Tailscale)
	}
	// A plain read changes nothing on the machine.
	ts.serveErr = ""
	if st := status(f, false); st.Tailscale.Serving || len(ts.served) != 0 {
		t.Fatalf("a status read served: %+v %v", st.Tailscale, ts.served)
	}
	// The guided setup serves by itself; the name is not trusted before the owner says so.
	ts.phone = false
	st := status(f, true)
	if !st.Tailscale.Serving || st.Tailscale.Trusted || st.Tailscale.Ready || st.Tailscale.PhoneOnline || st.Tailscale.URL != "https://"+phoneTailnetName {
		t.Fatalf("after setup: %+v", st.Tailscale)
	}
	if w := f.request(t, "GET", "/api/health", phoneTailnetName, nil, nil, false); w.Code != 403 {
		t.Fatalf("an untrusted name answered: %d", w.Code)
	}
	// Served and trusted but nothing answers through the name: no code is shown.
	f.s.SetTailscaleProbe(func(context.Context, string) error { return errors.New("no certificate yet") })
	w, _ := f.ownerPost(t, "/api/phone/connect", map[string]string{"via": "tailscale"})
	if p := phoneProblemOf(t, w); p.Step != "checking" {
		t.Fatalf("step %q want checking", p.Step)
	}
	if st := status(f, false); st.Tailscale.Ready || st.Tailscale.Step != "checking" || !st.Tailscale.Trusted {
		t.Fatalf("not answering: %+v", st.Tailscale)
	}
	time.Sleep(5 * time.Millisecond)
	f.s.SetTailscaleProbe(func(context.Context, string) error { return nil })
	if st := status(f, false); !st.Tailscale.Ready || st.Best != "tailscale" || !st.PINSet {
		t.Fatalf("ready: %+v", st)
	}
}

func TestPhoneConnectIsOnlyForTheOwnerAtTheComputer(t *testing.T) {
	f, _ := phoneFixture(t)
	f.setPIN(t, testPIN)
	host, code, _ := f.connect(t)
	w := f.scan(t, host, code, testPIN)
	deviceResponse(t, w, 201)
	phone := responseCookie(t, w, deviceCookie)
	// A paired phone cannot make codes for more devices, nor read or change the setup.
	body := map[string]string{"via": "tailscale"}
	deviceResponse(t, f.request(t, "POST", "/api/phone/connect", host, body, phone, false), 403)
	deviceResponse(t, f.request(t, "GET", "/api/phone", host, nil, phone, false), 403)
	deviceResponse(t, f.request(t, "POST", "/api/phone/tailscale/signin", host, nil, phone, false), 403)
	// Nor the owner token arriving through the proxy.
	deviceResponse(t, f.request(t, "POST", "/api/phone/connect", host, body, nil, true), 403)
	// Nobody without a session.
	deviceResponse(t, f.request(t, "POST", "/api/phone/connect", "localhost", body, nil, false), 401)
	// A scan needs the trusted HTTPS address.
	deviceResponse(t, f.request(t, "POST", "/api/pair/scan", "localhost", map[string]string{"code": code, "pin": testPIN}, nil, false), 403)
}

// The owner's other installs are reached through routes Tailscale already serves on this
// machine (443 is the Python tmx). tmx must never change or remove one: it only adds a route
// on a port nothing is served on, and when it cannot read what is served it changes nothing.
func TestPhoneConnectNeverTouchesAnExistingTailscaleRoute(t *testing.T) {
	f, ts := phoneFixture(t)
	f.setPIN(t, testPIN)
	serves := func() int {
		n := 0
		for _, c := range ts.calls {
			if strings.HasPrefix(c, "serve --bg") {
				n++
			}
		}
		return n
	}

	// What is served cannot be read: nothing is served, the answer says why.
	ts.statusErr = "failed to connect to local tailscaled"
	w, _ := f.ownerPost(t, "/api/phone/connect", map[string]string{"via": "tailscale"})
	if p := phoneProblemOf(t, w); p.Step != "failed" || !strings.Contains(p.Output, "tailscaled") {
		t.Fatalf("problem: %+v", p)
	}
	if w := f.request(t, "GET", "/api/phone?setup=1", "localhost", nil, nil, true); w.Code != 200 || !strings.Contains(w.Body.String(), `"step":"failed"`) {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}
	if serves() != 0 {
		t.Fatalf("served blind: %v", ts.calls)
	}
	ts.statusErr = ""

	// 443 and 8443 proxy to other installs, 10000 is a plain TCP forward: tmx takes its own
	// port number, the last one it may ask for, and leaves the rest as it was.
	ts.served[443] = "http://127.0.0.1:8777"
	ts.served[8443] = "http://127.0.0.1:8797"
	ts.tcp = []int{10000}
	host, _, _ := f.connect(t)
	if host != phoneTailnetName+":8787" || ts.served[443] != "http://127.0.0.1:8777" || ts.served[8443] != "http://127.0.0.1:8797" || serves() != 1 {
		t.Fatalf("host %q served %v calls %v", host, ts.served, ts.calls)
	}

	// Every port it may ask for is taken: it says so and serves nothing.
	f2, ts2 := phoneFixture(t)
	f2.setPIN(t, testPIN)
	ts2.served[443], ts2.served[8443], ts2.served[8787] = "http://127.0.0.1:1", "http://127.0.0.1:2", "http://127.0.0.1:3"
	ts2.tcp = []int{10000}
	w, _ = f2.ownerPost(t, "/api/phone/connect", map[string]string{"via": "tailscale"})
	if p := phoneProblemOf(t, w); p.Step != "busy" {
		t.Fatalf("problem: %+v", p)
	}
	for _, c := range ts2.calls {
		if strings.HasPrefix(c, "serve --bg") {
			t.Fatalf("served on a taken port: %v", ts2.calls)
		}
	}
}
