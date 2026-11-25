package accounts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/hiddenexec"
)

func TestIsolatedDefaultAccounts(t *testing.T) {
	real := t.TempDir()
	t.Setenv("USERPROFILE", real)
	t.Setenv("HOME", real)
	t.Setenv("CODEX_HOME", filepath.Join(real, ".codex"))
	t.Setenv("GEMINI_CLI_HOME", real)
	if err := os.MkdirAll(filepath.Join(real, ".gemini"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, ".gemini", "google_accounts.json"), []byte(`{"active":"real@example.com"}`), 0600); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	s := New(Options{Dir: home, Every: -1})
	defer s.Close()
	for _, h := range []string{"claude", "codex", "antigravity", "gemini"} {
		e := s.entry(h, "default")
		if !s.owns(e) || len(e.home) == 0 {
			t.Fatalf("%s is not isolated", h)
		}
		for _, v := range e.home {
			if strings.Contains(v, real) {
				t.Fatalf("%s inherits real home", h)
			}
		}
	}
	sc := s.scope(s.entry("antigravity", "default"))
	if agyActiveEmail(sc) != "" || agySignedIn(sc) {
		t.Fatal("leaked user's Antigravity login")
	}
	local := filepath.Join(sc.agyHome(), ".gemini")
	if err := os.MkdirAll(local, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "google_accounts.json"), []byte(`{"active":"local@example.com"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if agyActiveEmail(sc) != "local@example.com" {
		t.Fatal("local account not visible")
	}
	e := s.entry("codex", "default")
	e.home = HomeEnv(Codex, filepath.Join(real, ".codex"))
	if _, err := s.Logout(context.Background(), "codex", ""); !errors.Is(err, ErrNotOwned) {
		t.Fatalf("logout: %v", err)
	}
	if _, err := s.Login("codex", ""); !errors.Is(err, ErrNotOwned) {
		t.Fatalf("login: %v", err)
	}
	a := s.check(e)
	if a.Connected || a.Account.Email != "" || a.SignOutReason == "" {
		t.Fatalf("unowned account: %+v", a)
	}
}
func TestAuthorizedUserAccounts(t *testing.T) {
	s := New(Options{Dir: t.TempDir(), AllowUserLogins: true, Every: -1})
	defer s.Close()
	e := s.entry("codex", "default")
	if len(e.home) != 0 || !s.owns(e) {
		t.Fatal("authorized user account unavailable")
	}
}

func TestRedirectedAccountHomeRefused(t *testing.T) {
	home := t.TempDir()
	other := t.TempDir()
	link := filepath.Join(home, "redirect")
	if err := os.Symlink(other, link); err != nil {
		if runtime.GOOS != "windows" {
			t.Skipf("symlink unavailable: %v", err)
		}
		// Directory junctions exercise Windows reparse-point containment without
		// requiring the privilege needed to create a symbolic link.
		if out, err := hiddenexec.Command("cmd.exe", "/c", "mklink", "/J", link, other).CombinedOutput(); err != nil {
			t.Fatalf("junction: %v %s", err, out)
		}
	}
	s := New(Options{Dir: home, Every: -1})
	defer s.Close()
	e := s.entry("codex", "")
	e.home = HomeEnv(Codex, filepath.Join(link, "new-account"))
	if s.owns(e) {
		t.Fatal("redirected home accepted")
	}
	if _, err := s.Logout(context.Background(), "codex", ""); !errors.Is(err, ErrNotOwned) {
		t.Fatalf("%v", err)
	}
}
func TestIsolatedProfilesMatchAccountService(t *testing.T) {
	home := t.TempDir()
	p, err := OpenProfiles(filepath.Join(home, "ai-accounts"))
	if err != nil {
		t.Fatal(err)
	}
	p.IsolateDefaults(home)
	s := New(Options{Dir: filepath.Join(home, "accounts"), NodeHome: home, Profiles: p, Every: -1})
	defer s.Close()
	for _, h := range []string{"claude", "codex", "gemini", "antigravity"} {
		e := s.entry(h, "")
		if !s.owns(e) || strings.Join(e.home, ";") != strings.Join(p.Env(e.def.h, DefaultName), ";") {
			t.Fatalf("%s defaults differ", h)
		}
	}
}
