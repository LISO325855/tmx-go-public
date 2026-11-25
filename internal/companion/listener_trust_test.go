package companion

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A program is accepted when it is one of the roots or lies inside one; a look-alike folder
// beside a root, or the root's parent, is not.
func TestProgramUnder(t *testing.T) {
	base := t.TempDir()
	app := filepath.Join(base, "Programs", "Companion")
	dev := filepath.Join(base, "dev", ".venv", "Scripts", "python.exe")
	for _, tc := range []struct {
		exe   string
		roots []string
		want  bool
	}{
		{filepath.Join(app, "resources", "engine", "engine.exe"), []string{app}, true},
		{filepath.Join(app, "Companion.exe"), []string{app}, true},
		{dev, []string{app}, false},
		// named by the owner: the program itself, or its folder
		{dev, []string{app, dev}, true},
		{dev, []string{app, filepath.Join(base, "dev")}, true},
		{filepath.Join(base, "Programs", "CompanionEvil", "x.exe"), []string{app}, false},
		{filepath.Join(base, "Programs", "x.exe"), []string{app}, false},
		{app, []string{app}, true},
		{dev, nil, false},
		{dev, []string{""}, false},
	} {
		if got := programUnder(tc.exe, tc.roots); got != tc.want {
			t.Errorf("%s under %v: %v, want %v", tc.exe, tc.roots, got, tc.want)
		}
	}
	if runtime.GOOS == "windows" && !programUnder(strings.ToUpper(dev), []string{strings.ToLower(dev)}) {
		t.Error("on Windows the comparison must ignore case")
	}
}

// Only full paths are kept as trusted programs.
func TestSetTrustedPrograms(t *testing.T) {
	t.Cleanup(func() { SetTrustedPrograms(nil) })
	abs := filepath.Join(t.TempDir(), "python.exe")
	SetTrustedPrograms([]string{"python.exe", "  ", abs, "..\\x"})
	if got := TrustedPrograms(); len(got) != 1 || got[0] != abs {
		t.Fatalf("trusted programs %q, want only %q", got, abs)
	}
	SetTrustedPrograms(nil)
	if got := TrustedPrograms(); len(got) != 0 {
		t.Fatalf("after clearing: %q", got)
	}
}

// A failed check is not silent: the error still is ErrListenerIdentity and says what failed,
// through the dial and into the sentence the owner reads.
func TestListenerRefusalSaysWhy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	why := refused("the program on port %d is %s, which is not trusted", 8011, `C:\dev\python.exe`)
	dial := checkedDial((&net.Dialer{}).DialContext, func(net.Conn) error { return why })
	_, err = dial(context.Background(), "tcp", ln.Addr().String())
	if !errors.Is(err, ErrListenerIdentity) {
		t.Fatalf("the refusal is not ErrListenerIdentity: %v", err)
	}
	if got := ListenerReason(err); !strings.Contains(got, "no key was sent") || !strings.Contains(got, `C:\dev\python.exe`) {
		t.Fatalf("the reason: %q", got)
	}
	te := transportError(err, "GET")
	var ce *Error
	if !errors.As(te, &ce) || !strings.Contains(ce.Msg, "which is not trusted") {
		t.Fatalf("what the owner reads: %v", te)
	}
	// A check that fails without a reason still refuses, with the plain sentence.
	dial = checkedDial((&net.Dialer{}).DialContext, func(net.Conn) error { return errors.New("boom") })
	if _, err = dial(context.Background(), "tcp", ln.Addr().String()); !errors.Is(err, ErrListenerIdentity) || ListenerReason(err) != ErrListenerIdentity.Error() {
		t.Fatalf("a check without a reason: %v", err)
	}
	if ListenerReason(errors.New("other")) != "" {
		t.Fatal("a reason for an unrelated error")
	}
}

// The three steps of the check, and the owner's own address: with an address the owner set, a
// development engine (a python.exe outside the app) is accepted once Companion advertises the
// port and the connection belongs to that process; an address tmx found keeps the program
// check and says which program it refused; neither is accepted without the advert or when
// the connection belongs to another process.
func TestVerifyListener(t *testing.T) {
	base := t.TempDir()
	app := filepath.Join(base, "Programs", "Companion")
	appExe := filepath.Join(app, "resources", "engine.exe")
	devExe := filepath.Join(base, "dev", ".venv", "Scripts", "python.exe")
	facts := func(advertised int, owned bool, exe string, trusted ...string) listenerFacts {
		return listenerFacts{
			pidFor: func(port int) int {
				if port == 8011 {
					return advertised
				}
				return 0
			},
			owns:  func(pid int) (bool, error) { return owned && pid == advertised, nil },
			exeOf: func(int) (string, error) { return exe, nil },
			roots: func() []string { return append([]string{app}, trusted...) },
		}
	}
	for _, tc := range []struct {
		name     string
		port     int
		ownerSet bool
		f        listenerFacts
		refusal  string // "" = accepted
	}{
		{"the installed app, found by tmx", 8011, false, facts(700, true, appExe), ""},
		{"the dev engine, address set by the owner", 8011, true, facts(700, true, devExe), ""},
		{"the dev engine, found by tmx", 8011, false, facts(700, true, devExe), "python.exe, which is neither the installed Companion"},
		{"the dev engine, found by tmx, named as trusted", 8011, false, facts(700, true, devExe, devExe), ""},
		{"no advert for the port, owner-set", 9999, true, facts(700, true, devExe), "no running Companion advertises port 9999"},
		{"no advert for the port, found", 9999, false, facts(700, true, appExe), "no running Companion advertises port 9999"},
		{"another process holds the connection, owner-set", 8011, true, facts(700, false, devExe), "is not the process Companion advertises (pid 700)"},
		{"another process holds the connection, the app", 8011, false, facts(700, false, appExe), "is not the process Companion advertises (pid 700)"},
	} {
		err := verifyListener(tc.port, tc.ownerSet, tc.f)
		switch {
		case tc.refusal == "" && err != nil:
			t.Errorf("%s: refused: %v", tc.name, err)
		case tc.refusal != "" && (err == nil || !errors.Is(err, ErrListenerIdentity) || !strings.Contains(err.Error(), tc.refusal)):
			t.Errorf("%s: %v, want a refusal saying %q", tc.name, err, tc.refusal)
		}
	}
	unreadable := facts(700, true, devExe)
	unreadable.owns = func(int) (bool, error) { return false, errors.New("no table") }
	if err := verifyListener(8011, true, unreadable); !errors.Is(err, ErrListenerIdentity) {
		t.Errorf("an unreadable connection list was accepted: %v", err)
	}
}
