// Package tailscale drives the Tailscale CLI on this machine for "Connect a phone" (DESIGN.md
// "API", phone connect): it reads the node's state and name, and serves the daemon's loopback port
// over HTTPS on the tailnet (`tailscale serve`). Commands run with a timeout and never through a
// shell; tests inject a Runner.
package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/hiddenexec"
)

// Runner runs the CLI at path with args. stopWhen, if not nil, is called with everything the
// command printed so far (stdout and stderr together); true stops the command early (its output
// up to then is returned with ErrStopped).
type Runner func(ctx context.Context, path string, args []string, stopWhen func([]byte) bool) (stdout, stderr []byte, err error)

// ErrStopped: stopWhen ended the command.
var ErrStopped = errors.New("stopped early")

// Timeout bounds every command.
const Timeout = 20 * time.Second

// Client is the CLI found on this machine ("" Path: not installed).
type Client struct {
	Path string
	Run  Runner
}

// EnvCLI overrides where the Tailscale CLI is looked for.
const EnvCLI = "TMXG_TAILSCALE_CLI"

// Find looks for the CLI where installers put it, then on PATH.
func Find() *Client {
	return &Client{Path: findPath(), Run: Exec}
}

func findPath() string {
	// TMXG_TAILSCALE_CLI names the CLI when it is somewhere unusual (and in the browser e2e).
	if p := os.Getenv(EnvCLI); p != "" {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
		return ""
	}
	var cands []string
	switch runtime.GOOS {
	case "darwin":
		cands = []string{"/Applications/Tailscale.app/Contents/MacOS/Tailscale", "/opt/homebrew/bin/tailscale", "/usr/local/bin/tailscale"}
	case "windows":
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
			if d := os.Getenv(env); d != "" {
				cands = append(cands, filepath.Join(d, "Tailscale", "tailscale.exe"))
			}
		}
	default:
		cands = []string{"/usr/bin/tailscale", "/usr/local/bin/tailscale", "/usr/sbin/tailscale", "/snap/bin/tailscale"}
	}
	for _, c := range cands {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p
	}
	return ""
}

// Exec is the real Runner.
func Exec(ctx context.Context, path string, args []string, stopWhen func([]byte) bool) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	var mu sync.Mutex
	var all, out, errb bytes.Buffer
	stopped := false
	sink := func(b *bytes.Buffer) writerFunc {
		return func(p []byte) (int, error) {
			mu.Lock()
			defer mu.Unlock()
			b.Write(p)
			all.Write(p)
			if stopWhen != nil && !stopped && stopWhen(all.Bytes()) {
				stopped = true
				cancel()
			}
			return len(p), nil
		}
	}
	cmd := hiddenexec.CommandContext(ctx, path, args...) // no console window on Windows
	cmd.Env = cliEnv(os.Environ())
	cmd.Stdout, cmd.Stderr = sink(&out), sink(&errb)
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	mu.Lock()
	defer mu.Unlock()
	if stopped {
		err = ErrStopped
	} else if ctx.Err() != nil && err != nil {
		err = fmt.Errorf("tailscale did not answer within %s", Timeout)
	}
	return bytes.Clone(out.Bytes()), bytes.Clone(errb.Bytes()), err
}

// cliEnv is the environment the CLI runs with: tmx's, plus TAILSCALE_BE_CLI=1. On macOS the CLI
// is the app's own binary (Tailscale.app/Contents/MacOS/Tailscale); started by a program rather
// than from a terminal or Tailscale's /usr/local/bin/tailscale wrapper, it tries to open its window
// instead ("The Tailscale GUI failed to start", not JSON), which read as "not running" although
// Tailscale was connected. The variable makes it answer as the CLI; other builds ignore it.
// (Mustneer's v0.1.21, #345.)
func cliEnv(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if !strings.HasPrefix(e, "TAILSCALE_BE_CLI=") {
			out = append(out, e)
		}
	}
	return append(out, "TAILSCALE_BE_CLI=1")
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// Status is what tmx needs from `tailscale status --json`.
type Status struct {
	BackendState string // "Running", "NeedsLogin", "Stopped", "Starting", "NoState", …
	DNSName      string // this machine's MagicDNS name, without the final dot ("" without MagicDNS)
	Peers        []Peer // the other devices of the tailnet this node sees
}

// Peer is another device on the tailnet.
type Peer struct {
	Name   string // its MagicDNS name, without the final dot (else its host name)
	OS     string // "iOS" (iPhone and iPad), "android", "macOS", "windows", "linux", …
	Online bool
}

// Phone reports whether the peer is a phone or tablet.
func (p Peer) Phone() bool {
	return strings.EqualFold(p.OS, "iOS") || strings.EqualFold(p.OS, "android")
}

// PhoneOnline reports whether a phone or tablet is online on the tailnet right now: one that can
// open this machine's tailnet name. Without one, a tailnet link would be a dead end on the phone.
func (s Status) PhoneOnline() bool {
	for _, p := range s.Peers {
		if p.Online && p.Phone() {
			return true
		}
	}
	return false
}

// Running reports whether the node is logged in and connected.
func (s Status) Running() bool { return s.BackendState == "Running" }

// Status reads the node's state. An error means the CLI could not talk to Tailscale (the app or
// service is not running).
func (c *Client) Status(ctx context.Context) (Status, error) {
	out, errOut, err := c.Run(ctx, c.Path, []string{"status", "--json"}, nil)
	var raw struct {
		BackendState string
		Self         *struct{ DNSName string }
		Peer         map[string]struct {
			HostName string
			DNSName  string
			OS       string
			Online   bool
		}
	}
	if jerr := json.Unmarshal(out, &raw); jerr != nil || raw.BackendState == "" {
		if err == nil {
			err = errors.New("unexpected answer from tailscale status")
		}
		return Status{}, fmt.Errorf("%w: %s", err, firstLine(errOut, out))
	}
	st := Status{BackendState: raw.BackendState}
	if raw.Self != nil {
		st.DNSName = strings.TrimSuffix(strings.ToLower(raw.Self.DNSName), ".")
	}
	for _, p := range raw.Peer {
		name := strings.TrimSuffix(strings.ToLower(p.DNSName), ".")
		if name == "" {
			name = p.HostName
		}
		st.Peers = append(st.Peers, Peer{Name: name, OS: p.OS, Online: p.Online})
	}
	slices.SortFunc(st.Peers, func(a, b Peer) int { return strings.Compare(a.Name, b.Name) })
	return st, nil
}

// Served maps each HTTPS port this node serves on its own name to the target of "/" there
// (e.g. 443 -> "http://127.0.0.1:8787"). Errors read as nothing served.
func (c *Client) Served(ctx context.Context, host string) map[int]string {
	out, _, err := c.Run(ctx, c.Path, []string{"serve", "status", "--json"}, nil)
	served := map[int]string{}
	if err != nil {
		return served
	}
	var raw struct {
		Web map[string]struct {
			Handlers map[string]struct{ Proxy string }
		}
	}
	if json.Unmarshal(out, &raw) != nil {
		return served
	}
	for hp, web := range raw.Web {
		h, p, ok := strings.Cut(hp, ":")
		port, perr := strconv.Atoi(p)
		if !ok || perr != nil || !strings.EqualFold(strings.TrimSuffix(h, "."), host) {
			continue
		}
		served[port] = strings.TrimSuffix(web.Handlers["/"].Proxy, "/")
		if served[port] == "" {
			served[port] = "(other)" // something else (files, a path) is served there
		}
	}
	return served
}

// ServedAll is Served for deciding which port tmx may ask for: every port this node serves
// ANYTHING on (HTTPS for any name, a TCP forward, a funnel) is in the map, "(other)" when it is
// not a plain proxy on host's "/". An error means what is served could not be read: the
// caller must then change nothing, since `tailscale serve` on a port in use replaces what
// is there.
func (c *Client) ServedAll(ctx context.Context, host string) (map[int]string, error) {
	out, errOut, err := c.Run(ctx, c.Path, []string{"serve", "status", "--json"}, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, firstLine(errOut, out))
	}
	var raw struct {
		TCP map[string]json.RawMessage
		Web map[string]struct {
			Handlers map[string]struct{ Proxy string }
		}
	}
	if text := strings.TrimSpace(string(out)); text != "" {
		if err := json.Unmarshal(out, &raw); err != nil {
			return nil, fmt.Errorf("unexpected answer from tailscale serve status: %w", err)
		}
	}
	served := map[int]string{}
	for p := range raw.TCP {
		port, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("unexpected port %q in tailscale serve status", p)
		}
		served[port] = "(other)"
	}
	for hp, web := range raw.Web {
		h, p, ok := strings.Cut(hp, ":")
		port, perr := strconv.Atoi(p)
		if !ok || perr != nil {
			return nil, fmt.Errorf("unexpected address %q in tailscale serve status", hp)
		}
		served[port] = "(other)"
		if target := strings.TrimSuffix(web.Handlers["/"].Proxy, "/"); target != "" && strings.EqualFold(strings.TrimSuffix(h, "."), host) {
			served[port] = target
		}
	}
	return served, nil
}

// ServeError is a `tailscale serve` that did not work, sorted into what the owner must do.
type ServeError struct {
	Step   string // "enable": Serve/HTTPS is off for the tailnet (Link enables it); "denied": no permission; "failed": anything else
	Link   string // where to fix it
	Output string // what tailscale said (first lines)
}

func (e *ServeError) Error() string {
	switch e.Step {
	case "enable":
		return "HTTPS (Tailscale Serve) is not enabled for your tailnet yet"
	case "denied":
		return "Tailscale did not let tmx serve: " + e.Output
	}
	return "tailscale serve failed: " + e.Output
}

// AdminDNS is where HTTPS certificates are turned on for a tailnet.
const AdminDNS = "https://login.tailscale.com/admin/dns"

var enableURL = regexp.MustCompile(`https://login\.tailscale\.com/f/[^\s"']+`)

// Serve makes https://<name>:<port> on the tailnet proxy to target, in the background (it stays
// across restarts of Tailscale). When the tailnet must enable Serve first, tailscale prints a link
// and waits: the command is stopped there and the link returned.
func (c *Client) Serve(ctx context.Context, port int, target string) error {
	args := []string{"serve", "--bg", "--yes", "--https=" + strconv.Itoa(port), target}
	out, errOut, err := c.Run(ctx, c.Path, args, func(seen []byte) bool { return enableURL.Match(seen) })
	text := string(out) + "\n" + string(errOut)
	if m := enableURL.FindString(text); m != "" {
		return &ServeError{Step: "enable", Link: m, Output: firstLine(errOut, out)}
	}
	if err == nil {
		return nil
	}
	low := strings.ToLower(text)
	switch {
	case strings.Contains(low, "flag provided but not defined: -yes"):
		// An older CLI without --yes: the same without it.
		args = []string{"serve", "--bg", "--https=" + strconv.Itoa(port), target}
		if out, errOut, err = c.Run(ctx, c.Path, args, func(seen []byte) bool { return enableURL.Match(seen) }); err == nil {
			return nil
		}
		text = string(out) + "\n" + string(errOut)
		if m := enableURL.FindString(text); m != "" {
			return &ServeError{Step: "enable", Link: m, Output: firstLine(errOut, out)}
		}
		low = strings.ToLower(text)
	}
	switch {
	case strings.Contains(low, "https") && (strings.Contains(low, "not enabled") || strings.Contains(low, "disabled") || strings.Contains(low, "cert")),
		strings.Contains(low, "serve is not enabled"):
		return &ServeError{Step: "enable", Link: AdminDNS, Output: firstLine(errOut, out)}
	case strings.Contains(low, "access denied") || strings.Contains(low, "permission denied") || strings.Contains(low, "operator"):
		return &ServeError{Step: "denied", Output: firstLine(errOut, out)}
	}
	msg := firstLine(errOut, out)
	if msg == "" {
		msg = err.Error()
	}
	return &ServeError{Step: "failed", Output: msg}
}

var loginURL = regexp.MustCompile(`https://login\.tailscale\.com/a/[^\s"']+`)

// Login starts signing this machine in: it returns the page to sign in on, as soon as tailscale
// prints it ("" when it finished without one: already signed in).
func (c *Client) Login(ctx context.Context) (string, error) {
	out, errOut, err := c.Run(ctx, c.Path, []string{"login"}, func(seen []byte) bool { return loginURL.Match(seen) })
	if m := loginURL.FindString(string(out) + "\n" + string(errOut)); m != "" {
		return m, nil
	}
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, firstLine(errOut, out))
	}
	return "", nil
}

// OpenApp starts the Tailscale app (macOS: the menu bar app, which also starts its service).
// Elsewhere the service runs by itself; false = nothing to open here.
func (c *Client) OpenApp(ctx context.Context) (bool, error) {
	if runtime.GOOS != "darwin" {
		return false, nil
	}
	_, errOut, err := c.Run(ctx, "/usr/bin/open", []string{"-a", "Tailscale"}, nil)
	if err != nil {
		return false, fmt.Errorf("%w: %s", err, firstLine(errOut))
	}
	return true, nil
}

// firstLine is the first non-empty line of the first output that has one.
func firstLine(outs ...[]byte) string {
	for _, o := range outs {
		for _, l := range strings.Split(string(o), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				if len(l) > 300 {
					l = l[:300] + "…"
				}
				return l
			}
		}
	}
	return ""
}
