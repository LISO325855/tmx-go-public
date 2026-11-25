package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
)

func TestPeersImportPythonCommand(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	if _, _, code := tmx(t, "peers"); code != 2 {
		t.Fatalf("no subcommand: exit %d", code)
	}
	if _, _, code := tmx(t, "peers", "export"); code != 2 {
		t.Fatalf("unknown subcommand: exit %d", code)
	}
	// The test daemon is a trial: it never imports secrets.
	if _, _, code := tmx(t, "peers", "import-python", "--from", t.TempDir()); code != 2 {
		t.Fatalf("--from accepted: exit %d", code)
	}
	_, errs, code := tmx(t, "peers", "import-python")
	if code == 0 || !strings.Contains(errs, "production") {
		t.Fatalf("trial import: exit %d %q", code, errs)
	}
}

func TestPeerManagementCLI(t *testing.T) {
	t.Setenv(fleet.EnvToken, "")
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Error("owner token missing")
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /api/peers/invite":
			var p config.Peer
			if json.NewDecoder(r.Body).Decode(&p) != nil || p.Name != "forge" || p.URL != "https://forge.example.ts.net" {
				t.Error("invite arguments lost")
			}
			json.NewEncoder(w).Encode(map[string]string{"code": "TEST-ONLY-CODE"})
		case "POST /api/peers/join":
			var req map[string]string
			if json.NewDecoder(r.Body).Decode(&req) != nil || req["code"] != "TEST-ONLY-CODE" || req["url"] != "https://existing.example.ts.net" {
				t.Error("join arguments lost")
			}
			json.NewEncoder(w).Encode(map[string]string{"machine": "existing"})
		case "GET /api/peers":
			json.NewEncoder(w).Encode(map[string]any{"peers": []config.Peer{{Name: "forge", URL: "https://forge.example.ts.net"}}})
		case "DELETE /api/peers/forge":
			json.NewEncoder(w).Encode(map[string]bool{"removed": true})
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer stub.Close()
	useHome(t, t.TempDir(), urlPort(t, stub.URL))
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"invite", "--name", "forge", "--url", "https://forge.example.ts.net"}, "TEST-ONLY-CODE"},
		{[]string{"join", "--code", "TEST-ONLY-CODE", "--url", "https://existing.example.ts.net"}, "Paired with existing"},
		{[]string{"list"}, "forge\thttps://forge.example.ts.net"},
		{[]string{"remove", "forge"}, "Peer removed locally"},
	} {
		out, errs, code := tmx(t, append([]string{"peers"}, tc.args...)...)
		if code != 0 || !strings.Contains(out, tc.want) {
			t.Fatalf("%s failed: exit %d", tc.args[0], code)
		}
		if strings.Contains(errs, "TEST-ONLY-CODE") || tc.args[0] != "invite" && strings.Contains(out, "TEST-ONLY-CODE") {
			t.Fatal("code echoed outside invite output")
		}
	}
	t.Setenv(fleet.EnvToken, "agent-test-token")
	if _, _, code := tmx(t, "peers", "list"); code == 0 {
		t.Fatal("agent accessed owner CLI")
	}
}
