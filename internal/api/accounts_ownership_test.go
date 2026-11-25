package api

import (
	"github.com/blackcode-ch/tmx-go/internal/accounts"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUnownedAccountIsForbidden(t *testing.T) {
	profiles, err := accounts.OpenProfiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := profiles.Add(accounts.Codex, "other"); err != nil {
		t.Fatal(err)
	}
	accts := accounts.New(accounts.Options{Dir: t.TempDir(), Profiles: profiles, Every: -1})
	defer accts.Close()
	s := &Server{accounts: accts}
	r := httptest.NewRequest("POST", "/api/accounts/codex/logout?account=other", nil)
	r.SetPathValue("harness", "codex")
	w := httptest.NewRecorder()
	s.accountLogout(w, r)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "does not own") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
