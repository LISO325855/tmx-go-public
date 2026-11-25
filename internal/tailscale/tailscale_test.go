package tailscale

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// fake answers by the command's arguments; calls records them.
type fake struct {
	answers map[string]func(stopWhen func([]byte) bool) ([]byte, []byte, error)
	calls   [][]string
}

func (f *fake) run(_ context.Context, _ string, args []string, stopWhen func([]byte) bool) ([]byte, []byte, error) {
	f.calls = append(f.calls, args)
	if a := f.answers[strings.Join(args, " ")]; a != nil {
		return a(stopWhen)
	}
	return nil, []byte("unknown command"), errors.New("exit status 1")
}

func say(out, errOut string, err error) func(func([]byte) bool) ([]byte, []byte, error) {
	return func(func([]byte) bool) ([]byte, []byte, error) { return []byte(out), []byte(errOut), err }
}

func TestStatus(t *testing.T) {
	f := &fake{answers: map[string]func(func([]byte) bool) ([]byte, []byte, error){
		"status --json": say(`{"BackendState":"Running","Self":{"DNSName":"Mac.tail-abc.ts.net."}}`, "", nil),
	}}
	c := &Client{Path: "tailscale", Run: f.run}
	st, err := c.Status(context.Background())
	if err != nil || !st.Running() || st.DNSName != "mac.tail-abc.ts.net" {
		t.Fatalf("%+v %v", st, err)
	}
	// Logged out still answers JSON; a stopped service does not.
	f.answers["status --json"] = say(`{"BackendState":"NeedsLogin","Self":{"DNSName":""}}`, "", errors.New("exit status 1"))
	if st, err := c.Status(context.Background()); err != nil || st.Running() || st.BackendState != "NeedsLogin" {
		t.Fatalf("logged out: %+v %v", st, err)
	}
	f.answers["status --json"] = say("", "failed to connect to local Tailscale service; is Tailscale running?", errors.New("exit status 1"))
	if _, err := c.Status(context.Background()); err == nil || !strings.Contains(err.Error(), "is Tailscale running") {
		t.Fatalf("not running: %v", err)
	}
}

// A phone (iPhone, iPad, Android) online on the tailnet is what makes a tailnet link safe to show:
// offline phones, computers and a missing peer list do not count.
func TestPhoneOnline(t *testing.T) {
	f := &fake{answers: map[string]func(func([]byte) bool) ([]byte, []byte, error){
		"status --json": say(`{"BackendState":"Running","Self":{"DNSName":"mac.tail-abc.ts.net."},"Peer":{
			"nodekey:a":{"HostName":"Sams-iPhone","DNSName":"sams-iphone.tail-abc.ts.net.","OS":"iOS","Online":false},
			"nodekey:b":{"HostName":"laptop","DNSName":"laptop.tail-abc.ts.net.","OS":"windows","Online":true}}}`, "", nil),
	}}
	c := &Client{Path: "tailscale", Run: f.run}
	st, err := c.Status(context.Background())
	if err != nil || len(st.Peers) != 2 || st.Peers[1].Name != "sams-iphone.tail-abc.ts.net" || !st.Peers[1].Phone() || st.PhoneOnline() {
		t.Fatalf("phone offline, laptop online: %+v %v", st, err)
	}
	f.answers["status --json"] = say(`{"BackendState":"Running","Self":{"DNSName":"mac.tail-abc.ts.net."},"Peer":{
		"nodekey:c":{"HostName":"pixel","DNSName":"","OS":"android","Online":true}}}`, "", nil)
	if st, _ := c.Status(context.Background()); !st.PhoneOnline() || st.Peers[0].Name != "pixel" {
		t.Fatalf("android online: %+v", st)
	}
	f.answers["status --json"] = say(`{"BackendState":"Running","Self":{"DNSName":"mac.tail-abc.ts.net."}}`, "", nil)
	if st, _ := c.Status(context.Background()); st.PhoneOnline() {
		t.Fatal("no peers: no phone")
	}
}

func TestServed(t *testing.T) {
	f := &fake{answers: map[string]func(func([]byte) bool) ([]byte, []byte, error){
		"serve status --json": say(`{"TCP":{"443":{"HTTPS":true},"8443":{"HTTPS":true}},"Web":{
			"mac.tail-abc.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"}}},
			"mac.tail-abc.ts.net:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:8787/"}}},
			"other.tail-abc.ts.net:10000":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:1"}}}}}`, "", nil),
	}}
	c := &Client{Path: "tailscale", Run: f.run}
	got := c.Served(context.Background(), "mac.tail-abc.ts.net")
	if len(got) != 2 || got[443] != "http://127.0.0.1:3000" || got[8443] != "http://127.0.0.1:8787" {
		t.Fatalf("%v", got)
	}
	f.answers["serve status --json"] = say("{}", "", nil)
	if got := c.Served(context.Background(), "mac.tail-abc.ts.net"); len(got) != 0 {
		t.Fatalf("nothing served: %v", got)
	}
}

func TestServe(t *testing.T) {
	const cmd = "serve --bg --yes --https=443 http://127.0.0.1:8787"
	cases := []struct {
		name     string
		answers  map[string]func(func([]byte) bool) ([]byte, []byte, error)
		step     string
		link     string
		commands int
	}{
		{name: "ok", answers: map[string]func(func([]byte) bool) ([]byte, []byte, error){cmd: say("Available within your tailnet", "", nil)}, commands: 1},
		{name: "serve not enabled: tailscale prints a link and waits", answers: map[string]func(func([]byte) bool) ([]byte, []byte, error){
			cmd: func(stop func([]byte) bool) ([]byte, []byte, error) {
				out := []byte("Serve is not enabled on your tailnet.\nTo enable, visit:\n\n         https://login.tailscale.com/f/serve?node=nABC\n")
				if !stop(out) {
					t.Error("the link should stop the command")
				}
				return out, nil, ErrStopped
			}}, step: "enable", link: "https://login.tailscale.com/f/serve?node=nABC", commands: 1},
		{name: "https certificates off", answers: map[string]func(func([]byte) bool) ([]byte, []byte, error){
			cmd: say("", "error: HTTPS cert support is not enabled/configured for your tailnet", errors.New("exit status 1"))}, step: "enable", link: AdminDNS, commands: 1},
		{name: "not the operator", answers: map[string]func(func([]byte) bool) ([]byte, []byte, error){
			cmd: say("", "Access denied: serve config denied", errors.New("exit status 1"))}, step: "denied", commands: 1},
		{name: "an older CLI without --yes", answers: map[string]func(func([]byte) bool) ([]byte, []byte, error){
			cmd: say("", "flag provided but not defined: -yes", errors.New("exit status 2")),
			"serve --bg --https=443 http://127.0.0.1:8787": say("ok", "", nil)}, commands: 2},
		{name: "anything else", answers: map[string]func(func([]byte) bool) ([]byte, []byte, error){
			cmd: say("", "\nsomething odd happened\n", errors.New("exit status 1"))}, step: "failed", commands: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{answers: tc.answers}
			err := (&Client{Path: "tailscale", Run: f.run}).Serve(context.Background(), 443, "http://127.0.0.1:8787")
			if len(f.calls) != tc.commands {
				t.Fatalf("calls: %v", f.calls)
			}
			if tc.step == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var se *ServeError
			if !errors.As(err, &se) || se.Step != tc.step || se.Link != tc.link {
				t.Fatalf("%#v", err)
			}
		})
	}
}

// The real runner stops a command once its output asks for it, and never runs a shell string.
func TestExecStopsEarly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	start := time.Now()
	out, _, err := Exec(context.Background(), "/bin/sh", []string{"-c", "echo https://login.tailscale.com/f/serve?node=x; sleep 30"},
		func(b []byte) bool { return enableURL.Match(b) })
	if !errors.Is(err, ErrStopped) || !strings.Contains(string(out), "login.tailscale.com/f/serve") {
		t.Fatalf("%q %v", out, err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("took %s", d)
	}
	out, _, err = Exec(context.Background(), "/bin/echo", []string{"a;", "b"}, nil)
	if err != nil || strings.TrimSpace(string(out)) != "a; b" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestFindPath(t *testing.T) {
	// Whatever this machine has; only that it never panics and returns a file or nothing.
	if p := findPath(); p != "" && !slices.Contains([]string{"tailscale", "tailscale.exe", "Tailscale"}, lastElem(p)) {
		t.Fatalf("odd path %q", p)
	}
}

func lastElem(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// The real runner tells the CLI to be the CLI: on macOS the app binary otherwise tries to open its
// window when a program (not a terminal) starts it, and its answer is not JSON.
func TestExecRunsAsCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script")
	}
	script := filepath.Join(t.TempDir(), "tailscale")
	body := "#!/bin/sh\nif [ \"$TAILSCALE_BE_CLI\" = 1 ]; then echo '{\"BackendState\":\"Running\",\"Self\":{\"DNSName\":\"mac.tail-abc.ts.net.\"}}'; else echo 'The Tailscale GUI failed to start' >&2; fi\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAILSCALE_BE_CLI", "0") // a stray value in tmx's own environment is replaced
	st, err := (&Client{Path: script, Run: Exec}).Status(context.Background())
	if err != nil || !st.Running() || st.DNSName != "mac.tail-abc.ts.net" {
		t.Fatalf("status: %+v %v", st, err)
	}
}

// ServedAll is what tmx reads before it asks for a port: every port anything is served on
// counts, and a status that cannot be read is an error, never "nothing served".
func TestServedAll(t *testing.T) {
	f := &fake{answers: map[string]func(func([]byte) bool) ([]byte, []byte, error){
		"serve status --json": say(`{"TCP":{"443":{"HTTPS":true},"8443":{"HTTPS":true},"2222":{"TCPForward":"127.0.0.1:22"},"9443":{"HTTPS":true}},
			"Web":{"mac.tail-abc.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:8787"}}},
			       "mac.tail-abc.ts.net:8443":{"Handlers":{"/files":{"Path":"/tmp"}}},
			       "other.tail-abc.ts.net:9443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:1"}}}}}`, "", nil),
	}}
	c := &Client{Path: "tailscale", Run: f.run}
	got, err := c.ServedAll(context.Background(), "mac.tail-abc.ts.net")
	if err != nil || len(got) != 4 || got[443] != "http://127.0.0.1:8787" || got[8443] != "(other)" || got[2222] != "(other)" || got[9443] != "(other)" {
		t.Fatalf("%v %v", got, err)
	}
	// Nothing served at all.
	for _, out := range []string{"{}", ""} {
		f.answers["serve status --json"] = say(out, "", nil)
		if got, err := c.ServedAll(context.Background(), "mac.tail-abc.ts.net"); err != nil || len(got) != 0 {
			t.Fatalf("empty %q: %v %v", out, got, err)
		}
	}
	// Cannot be read: an error.
	f.answers["serve status --json"] = say("", "failed to connect to local tailscaled", errors.New("exit status 1"))
	if _, err := c.ServedAll(context.Background(), "mac.tail-abc.ts.net"); err == nil || !strings.Contains(err.Error(), "tailscaled") {
		t.Fatalf("unreadable: %v", err)
	}
	f.answers["serve status --json"] = say("not json", "", nil)
	if _, err := c.ServedAll(context.Background(), "mac.tail-abc.ts.net"); err == nil {
		t.Fatal("garbage read as nothing served")
	}
}
