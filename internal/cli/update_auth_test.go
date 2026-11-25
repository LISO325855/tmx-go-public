package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/peers"
)

func TestUpdateLocalHeadAuthorization(t *testing.T) {
	d := apitest.New(t)
	pc := peers.New(config.Config{Profile: config.ProfileProduction, Machine: "local", Peers: []config.Peer{{Name: "remote", URL: "https://remote.example.ts.net"}}}, config.Paths{Home: d.Home}, nil)
	pair := peers.NewPairToken()
	if _, err := pc.Tokens().Store("remote", pair, "test"); err != nil {
		t.Fatal(err)
	}
	url, _ := d.WithUpdater(t, pc)
	useHome(t, d.Home, urlPort(t, url))
	p, err := d.Fleet.CreateProject(fleet.OwnerCaller, fleet.ProjectRequest{Name: "Updates"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Fleet.LaunchAgent(fleet.OwnerCaller, fleet.LaunchRequest{Name: "worker", ProjectID: p.ID}); err != nil {
		t.Fatal(err)
	}
	if err := d.Gate.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	head := d.AgentToken(t, "head")
	routes := []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/update", nil}, {"POST", "/api/update/check", nil},
		{"POST", "/api/update/install", map[string]any{"now": true}},
		{"PUT", "/api/update", map[string]any{"auto": true}},
		{"PUT", "/api/update", map[string]any{"auto": false}},
	}
	do := func(method, path, token string, body any, headers map[string]string) int {
		t.Helper()
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, url+path, bytes.NewReader(b))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	for _, role := range []string{"head", "updates-lead", "worker", "peer", "anonymous", "proxied-head"} {
		t.Run(role, func(t *testing.T) {
			token := ""
			headers := map[string]string{}
			switch role {
			case "peer":
				headers[peers.HeaderFrom] = "remote"
				headers[peers.HeaderToken] = pair
				headers[peers.HeaderOnBehalf] = "agent:head"
			case "anonymous":
			case "proxied-head":
				token = head
				headers["X-Forwarded-For"] = "192.0.2.1"
			default:
				token = d.AgentToken(t, role)
			}
			for _, r := range routes {
				code := do(r.method, r.path, token, r.body, headers)
				if role == "head" {
					if code != 200 && code != 202 {
						t.Fatalf("%s %s: %d", r.method, r.path, code)
					}
				} else if code != 403 && code != 401 {
					t.Fatalf("%s %s accepted: %d", r.method, r.path, code)
				}
			}
		})
	}
	for _, r := range []struct {
		path string
		body any
	}{
		{"/api/update", map[string]any{"channel": "latest"}},
		{"/api/update", map[string]any{"auto": true, "channel": "stable"}},
		{"/api/update/token", map[string]any{"token": "-"}},
		{"/api/update/keys", map[string]any{}},
		{"/api/update/drill", map[string]any{"arm": true}},
	} {
		if code := do("PUT", r.path, head, r.body, nil); code != 403 {
			t.Fatalf("head owner-only %s: %d", r.path, code)
		}
	}
	t.Setenv(fleet.EnvURL, url)
	t.Setenv(fleet.EnvToken, head)
	for _, args := range [][]string{{"status"}, {"check"}, {"now"}, {"auto", "on"}, {"auto", "off"}} {
		out, errs, code := tmx(t, append([]string{"update"}, args...)...)
		if code != 0 || errs != "" {
			t.Fatalf("head CLI %v: %d %s %s", args, code, out, errs)
		}
	}
	if len(d.GateAuth.Prompts()) != 0 {
		t.Fatal("head update prompted owner")
	}
	for _, role := range []string{"updates-lead", "worker"} {
		t.Setenv(fleet.EnvToken, d.AgentToken(t, role))
		if _, _, code := tmx(t, "update", "now"); code == 0 {
			t.Fatalf("%s CLI fell back to owner", role)
		}
	}
	t.Setenv(fleet.EnvToken, "")
	if _, errs, code := tmx(t, "update", "now"); code == 0 || !strings.Contains(errs, "agent's") {
		t.Fatal("incomplete agent context fell back to owner")
	}
}
