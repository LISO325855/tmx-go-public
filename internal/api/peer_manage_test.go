package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/api"
	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	tc "github.com/blackcode-ch/tmx-go/internal/client"
	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/peers"
)

func TestPeerInviteJoinLiveAndReload(t *testing.T) {
	a, b := apitest.New(t), apitest.New(t)
	const ah = "alpha.example.ts.net"
	const bh = "beta.example.ts.net"
	makeNode := func(d *apitest.Daemon, name, host string) (*peers.Client, *httptest.Server) {
		production(t, d, name)
		paths := config.Paths{Home: d.Home}
		cfg, err := config.Load(paths)
		if err != nil {
			t.Fatal(err)
		}
		cfg.API.TrustedHosts = []string{host}
		if name == "alpha" {
			cfg.Peers = []config.Peer{{Name: "gamma", URL: "https://gamma.example.ts.net"}}
		}
		if err := config.Save(paths, cfg); err != nil {
			t.Fatal(err)
		}
		pc := peers.New(cfg, paths, nil)
		srv := httptest.NewServer(api.New(api.Options{Fleet: d.Fleet, OwnerToken: d.OwnerToken, Peers: pc, TrustedHosts: []string{host}}).Handler())
		t.Cleanup(srv.Close)
		return pc, srv
	}
	pa, sa := makeNode(a, "alpha", ah)
	pb, sb := makeNode(b, "beta", bh)
	// Substitute only transport, retaining production HTTPS URL validation. Model
	// the local tailscale HTTPS proxy's trusted host/proto metadata.
	t.Cleanup(api.SetJoinForTest(config.PeerURL, &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		base := sa.URL
		if r.URL.Host == bh {
			base = sb.URL
		}
		u, _ := url.Parse(base)
		copy.URL.Scheme, copy.URL.Host = u.Scheme, u.Host
		copy.Host = r.URL.Host
		copy.Header.Set("X-Forwarded-Proto", "https")
		return http.DefaultTransport.RoundTrip(copy)
	})}))
	keep := peers.NewPairToken()
	if _, err := pa.Tokens().Store("gamma", keep, "test"); err != nil {
		t.Fatal(err)
	}
	req := config.Peer{Name: "beta", URL: "https://" + bh}
	if status, _ := joinPost(t, sa.URL, "/api/peers/invite", a.AgentToken(t, "head"), nil, req); status != 403 {
		t.Fatalf("non-owner invite: %d", status)
	}
	status, body := joinPost(t, sa.URL, "/api/peers/invite", a.OwnerToken, nil, req)
	var invite struct {
		Code string `json:"code"`
	}
	if status != 200 || json.Unmarshal([]byte(body), &invite) != nil || invite.Code == "" {
		t.Fatalf("invite: %d", status)
	}
	proxy := map[string]string{"Host": ah, "X-Forwarded-Proto": "https"}
	if status, _ := joinPost(t, sa.URL, "/api/machines/join/accept", "", proxy, map[string]string{"code": invite.Code, "name": "other", "url": "https://" + bh}); status != 403 {
		t.Fatalf("unbound machine: %d", status)
	}
	join := map[string]string{"code": invite.Code, "url": "https://" + ah}
	if status, _ := joinPost(t, sb.URL, "/api/peers/join", b.AgentToken(t, "head"), nil, join); status != 403 {
		t.Fatalf("non-owner join: %d", status)
	}
	if status, _ := joinPost(t, sb.URL, "/api/peers/join", b.OwnerToken, nil, join); status != 200 {
		t.Fatalf("join: %d", status)
	}
	token := pa.Tokens().For("beta")
	if len(token) != 43 || token != pb.Tokens().For("alpha") || !pa.Known("beta") || !pb.Known("alpha") {
		t.Fatal("pairing not published live on both sides")
	}
	if status, _ := joinPost(t, sa.URL, "/api/machines/join/accept", "", proxy, map[string]string{"code": invite.Code, "name": "beta", "url": "https://" + bh}); status != 403 {
		t.Fatalf("replay: %d", status)
	}
	// Reconstruct both clients from disk as daemon startup does.
	for _, node := range []struct {
		d    *apitest.Daemon
		peer string
	}{{a, "beta"}, {b, "alpha"}} {
		paths := config.Paths{Home: node.d.Home}
		cfg, err := config.Load(paths)
		if err != nil {
			t.Fatal(err)
		}
		pc := peers.New(cfg, paths, nil)
		if !pc.Known(node.peer) || pc.Tokens().For(node.peer) != token {
			t.Fatal("pairing lost on reload")
		}
	}
	cx := context.Background()
	owner := tc.New(sa.URL, a.OwnerToken)
	agent := tc.New(sa.URL, a.AgentToken(t, "head"))
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		path := "/api/peers"
		if method == http.MethodDelete {
			path += "/beta"
		}
		if err := agent.Do(cx, method, path, nil, nil); err == nil {
			t.Fatal("non-owner management accepted")
		}
	}
	var list struct {
		Peers []config.Peer `json:"peers"`
	}
	if err := owner.Get(cx, "/api/peers", &list); err != nil || len(list.Peers) != 2 || list.Peers[0].Name != "beta" {
		t.Fatal("list failed")
	}
	if err := owner.Do(cx, http.MethodDelete, "/api/peers/beta", nil, nil); err != nil {
		t.Fatal(err)
	}
	if pa.Known("beta") || pa.Tokens().For("beta") != "" || pa.Tokens().For("gamma") != keep {
		t.Fatal("remove failed or changed another peer")
	}
	cfg, err := config.Load(config.Paths{Home: a.Home})
	if err != nil || len(cfg.Peers) != 1 || cfg.Peers[0].Name != "gamma" {
		t.Fatal("remove not persisted")
	}
}
