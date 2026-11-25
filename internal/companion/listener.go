package companion

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var ErrListenerIdentity = errors.New("Companion listener identity could not be verified; no key was sent")

// listenerError is ErrListenerIdentity with what failed, so the owner reads why his key was
// not sent (17 Nov: the check failed silently for the installed app and for the development
// engine alike, and Go and Companion could not talk).
type listenerError struct{ reason string }

func (e *listenerError) Error() string        { return ErrListenerIdentity.Error() + ": " + e.reason }
func (e *listenerError) Is(target error) bool { return target == ErrListenerIdentity }

func refused(format string, args ...any) error {
	return &listenerError{reason: fmt.Sprintf(format, args...)}
}

// ListenerReason is the sentence of a failed listener check in err ("" if err is none).
func ListenerReason(err error) string {
	var le *listenerError
	if errors.As(err, &le) {
		return le.Error()
	}
	if errors.Is(err, ErrListenerIdentity) {
		return ErrListenerIdentity.Error()
	}
	return ""
}

// trustedPrograms are the programs, besides the installed app, that the owner accepts as
// Companion's engine (config.json companion.trusted_programs, set behind the owner gate): the
// full path of a program, or of a folder whose programs are accepted. For a development
// Companion, whose engine is a python.exe outside the app's folder.
var trustedPrograms atomic.Value // []string

// SetTrustedPrograms replaces the list; entries that are not absolute paths are dropped.
func SetTrustedPrograms(paths []string) {
	var out []string
	for _, p := range paths {
		if p = strings.TrimSpace(p); p != "" && filepath.IsAbs(p) {
			out = append(out, filepath.Clean(p))
		}
	}
	trustedPrograms.Store(out)
}

// TrustedPrograms is the list SetTrustedPrograms stored.
func TrustedPrograms() []string {
	v, _ := trustedPrograms.Load().([]string)
	return v
}

// programUnder reports whether exe is one of roots or lies inside one (a folder). On Windows
// the comparison ignores case. Both sides are expected resolved (no symlinks).
func programUnder(exe string, roots []string) bool {
	norm := func(s string) string {
		s = filepath.Clean(s)
		if runtime.GOOS == "windows" {
			s = strings.ToLower(s)
		}
		return s
	}
	exe = norm(exe)
	for _, root := range roots {
		if root == "" {
			continue
		}
		root = norm(root)
		if exe == root {
			return true
		}
		rel, err := filepath.Rel(root, exe)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return true
		}
	}
	return false
}

// verifiedClient checks the accepted socket, not a previous probe of the port.
// No proxy and no connection reuse: every key-bearing connection is checked.
// ownerSet: the owner gave Companion's address himself (config companion.url, loopback only).
func verifiedClient(ownerSet bool) *http.Client {
	tr := &http.Transport{DisableKeepAlives: true, ResponseHeaderTimeout: 120 * time.Second}
	tr.DialContext = checkedDial((&net.Dialer{Timeout: 3 * time.Second}).DialContext,
		func(c net.Conn) error { return verifyCompanionConnection(c, ownerSet) })
	return &http.Client{Transport: tr, Timeout: 135 * time.Second}
}

func checkedDial(dial func(context.Context, string, string) (net.Conn, error), verify func(net.Conn) error) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		c, err := dial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		if err = verify(c); err != nil {
			c.Close()
			if errors.Is(err, ErrListenerIdentity) {
				return nil, err // with its reason
			}
			return nil, ErrListenerIdentity
		}
		return c, nil
	}
}

// listenerFacts is what the check needs to know about the machine (the OS's part; tests give
// their own).
type listenerFacts struct {
	pidFor func(port int) int            // the process a running Companion advertises for the port (0: none)
	owns   func(pid int) (bool, error)   // the connected socket's other end belongs to that process
	exeOf  func(pid int) (string, error) // its program, resolved
	roots  func() []string               // the installed app's folders and the owner's trusted programs
}

// verifyListener decides whether tmx's key may go over a connection to port:
//
//  1. a running Companion advertises that port with its process (advertisedPID);
//  2. the connection's other end belongs to that very process;
//  3. the process is the installed Companion app or a program the owner named as trusted.
//
// When the owner gave the address himself (ownerSet; it is loopback), 3 is not asked: he said
// where his Companion is, and 1 and 2 still tie the key to the process that advertises it. So
// a development engine (a python.exe outside the app's folder) works without more from him;
// an address tmx found by itself keeps all three (tmx-lead for the owner, 17 Nov 23:51).
func verifyListener(port int, ownerSet bool, f listenerFacts) error {
	pid := f.pidFor(port)
	if pid <= 0 {
		return refused("no running Companion advertises port %d (looked for agents/engine.json and local-first-engine-state.json in %s)",
			port, strings.Join(UserDataDirs(), " and "))
	}
	owned, err := f.owns(pid)
	if err != nil {
		return refused("whose connection this is could not be read: %v", err)
	}
	if !owned {
		return refused("the program listening on port %d is not the process Companion advertises (pid %d)", port, pid)
	}
	if ownerSet {
		return nil
	}
	exe, err := f.exeOf(pid)
	if err != nil {
		return refused("the program of process %d could not be read: %v", pid, err)
	}
	if programUnder(exe, f.roots()) {
		return nil
	}
	return refused("the program on port %d is %s, which is neither the installed Companion nor a program you named as trusted; "+
		"set Companion's address yourself in Settings, or name the program (companion.trusted_programs)", port, exe)
}

// The engine's advertised PID is necessary but not sufficient: the OS must bind
// the connected socket to it and its executable must be in the installed app.
func advertisedPID(port int) int {
	dirs := UserDataDirs()
	for _, dir := range dirs {
		var engine struct {
			URL string `json:"url"`
			PID int    `json:"pid"`
		}
		if readJSON(filepath.Join(dir, "agents", "engine.json"), &engine) {
			if u, e := CheckLoopback(engine.URL); e == nil {
				_, p, e := net.SplitHostPort(u[len("http://"):])
				if e == nil && p == strconv.Itoa(port) && engine.PID > 0 {
					return engine.PID
				}
			}
		}
	}
	for _, dir := range dirs {
		var state struct {
			Backend struct {
				Port int `json:"port"`
				PID  int `json:"pid"`
			} `json:"backend"`
		}
		if readJSON(filepath.Join(dir, "local-first-engine-state.json"), &state) && state.Backend.Port == port && state.Backend.PID > 0 {
			return state.Backend.PID
		}
	}
	return 0
}
