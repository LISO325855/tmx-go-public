package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/peers"
	"github.com/blackcode-ch/tmx-go/internal/toolbox"
	"github.com/blackcode-ch/tmx-go/internal/toolbox/toolboxtest"
)

func toolboxBody(t *testing.T, base, token, path string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", base+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func toolboxByID(t *testing.T, l toolbox.List, id string) toolbox.Resource {
	t.Helper()
	r, ok := toolbox.Find(l.Resources, id)
	if !ok {
		t.Fatalf("no resource %q in the answer", id)
	}
	return r
}

// noSecretShapes: no string value of the answer is a number, an address or a key.
func noSecretShapes(t *testing.T, body string) {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	var walk func(any)
	walk = func(x any) {
		switch x := x.(type) {
		case string:
			if shape := toolbox.SecretShape(x); shape != "" {
				t.Errorf("the answer holds %s: %q", shape, x)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(v)
}

// A node that runs none of the declared services (the test box) shows the declared list:
// the same to the owner and to an agent.
func TestToolboxDeclaredList(t *testing.T) {
	toolboxtest.Install(t)
	d := apitest.New(t)
	owner := client{t: t, base: d.URL, token: d.OwnerToken}
	agent := client{t: t, base: d.URL, token: d.AgentToken(t, "head")}

	var list toolbox.List
	if status := owner.do("GET", "/api/toolbox", nil, &list); status != 200 {
		t.Fatal(status)
	}
	if list.Machine != "testbox" || len(list.Resources) != toolboxtest.Lines || list.UpdatedAt == 0 {
		t.Fatalf("machine %q, %d resources, updated %d", list.Machine, len(list.Resources), list.UpdatedAt)
	}
	for _, r := range list.Resources {
		if r.Source != toolbox.SourceDeclared || r.Health != nil {
			t.Errorf("%s: source %q health %+v on a node that measures nothing", r.ID, r.Source, r.Health)
		}
	}
	// By kind, the lines that need attention first.
	if first := list.Resources[0]; first.Kind != toolbox.KindCommunication {
		t.Errorf("first line %s is %s", first.ID, first.Kind)
	}

	var forAgent toolbox.List
	if status := agent.do("GET", "/api/toolbox", nil, &forAgent); status != 200 {
		t.Fatal(status)
	}
	a, _ := json.Marshal(list.Resources)
	b, _ := json.Marshal(forAgent.Resources)
	if string(a) != string(b) {
		t.Error("an agent must see the same list as the owner")
	}

	var devices toolbox.List
	if status := agent.do("GET", "/api/toolbox?kind=device", nil, &devices); status != 200 || len(devices.Resources) != 2 {
		t.Fatalf("devices: %d, %d lines", status, len(devices.Resources))
	}
	var planned toolbox.List
	if status := agent.do("GET", "/api/toolbox?state=planned", nil, &planned); status != 200 || len(planned.Resources) != 2 {
		t.Fatalf("planned: %d, %d lines", status, len(planned.Resources))
	}
	if status := agent.do("GET", "/api/toolbox?kind=gadget", nil, nil); status != 400 {
		t.Fatalf("unknown kind: %d", status)
	}

	var one toolbox.Resource
	if status := agent.do("GET", "/api/toolbox/ai-accounts", nil, &one); status != 200 || one.Rule.Text != "Hard work on the strong model, easy work on the cheap one." {
		t.Fatalf("%d %+v", status, one.Rule)
	}
	if status := agent.do("GET", "/api/toolbox/nothing", nil, nil); status != 404 {
		t.Fatalf("unknown id: %d", status)
	}

	// Nobody else reads it; nothing here is written.
	if status, _ := toolboxBody(t, d.URL, "", "/api/toolbox"); status != 401 && status != 403 {
		t.Fatalf("without a token: %d", status)
	}
	for _, m := range []string{"POST", "PUT", "DELETE"} {
		if status := owner.do(m, "/api/toolbox/ai-accounts", map[string]string{"state": "works"}, nil); status != 405 && status != 404 {
			t.Fatalf("%s: %d", m, status)
		}
	}

	status, body := toolboxBody(t, d.URL, d.OwnerToken, "/api/toolbox")
	if status != 200 {
		t.Fatal(status)
	}
	noSecretShapes(t, body)
}

// On the node that runs them, WhatsApp, the AI accounts and the Companion link
// are measured, and a holder on another machine is looked up in the fleet list. The linked
// number never leaves the node.
func TestToolboxLiveLines(t *testing.T) {
	toolboxtest.Install(t)
	d := apitest.New(t)
	d.EnableWhatsApp(t)
	d.LinkWhatsApp(t)

	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"host": toolboxtest.BoxB, "sessions": []map[string]any{
			{"name": "sender", "host": toolboxtest.BoxB, "state": "idle", "rooms": []string{}},
			{"name": "assistant-lead", "host": toolboxtest.BoxB, "state": "exited", "rooms": []string{}},
		}})
	}))
	t.Cleanup(peer.Close)
	cfg := config.Config{Profile: config.ProfileProduction, Machine: "Node-A", Peers: []config.Peer{{Name: toolboxtest.BoxB, URL: peer.URL}}}
	pc := peers.New(cfg, config.Paths{Home: d.Home}, nil)
	pc.SetTransport(peer.Client().Transport)
	if _, err := pc.Tokens().Store(toolboxtest.BoxB, peers.NewPairToken(), "test"); err != nil {
		t.Fatal(err)
	}
	pc.PollAll(context.Background())
	base := d.WithPeers(t, pc)

	status, body := toolboxBody(t, base, d.AgentToken(t, "head"), "/api/toolbox")
	if status != 200 {
		t.Fatalf("%d %s", status, body)
	}
	if strings.Contains(body, "41790000000") || strings.Contains(body, "s.whatsapp.net") {
		t.Fatal("the linked WhatsApp number left the node")
	}
	noSecretShapes(t, body)
	var list toolbox.List
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	if list.Machine != toolboxtest.Node {
		t.Fatalf("machine %q", list.Machine)
	}

	wa := toolboxByID(t, list, "chat-node")
	if wa.Source != toolbox.SourceLive || wa.State != toolbox.StateWorks || wa.Health == nil || wa.Health.Level != toolbox.HealthGood {
		t.Errorf("WhatsApp: source %q state %q health %+v", wa.Source, wa.State, wa.Health)
	}
	// No harness is installed in the test daemon: the line says so instead of "works".
	acc := toolboxByID(t, list, "ai-accounts")
	if acc.Source != toolbox.SourceLive || acc.Health == nil || acc.Health.Level != toolbox.HealthDown {
		t.Errorf("AI accounts: source %q health %+v", acc.Source, acc.Health)
	}
	// Companion: this node has no key yet; the secrets are named with stored or missing.
	comp := toolboxByID(t, list, "assistant")
	if comp.Source != toolbox.SourceLive || comp.Health == nil || comp.Health.Level != toolbox.HealthWarning || len(comp.Secrets) != 2 {
		t.Fatalf("Companion: source %q health %+v secrets %+v", comp.Source, comp.Health, comp.Secrets)
	}
	for _, s := range comp.Secrets {
		if s.Stored == nil {
			t.Errorf("secret %q: stored or missing not said", s.Name)
		}
	}
	// Holders on the other machine, from the fleet list.
	if h := toolboxByID(t, list, "chat-browser").Health; h == nil || h.Level != toolbox.HealthGood {
		t.Errorf("holder up: %+v", h)
	}
	if h := toolboxByID(t, list, "connector").Health; h == nil || h.Level != toolbox.HealthDown {
		t.Errorf("holder stopped: %+v", h)
	}
	// The third machine is not a peer of this node: nothing is claimed about its holders.
	if h := toolboxByID(t, list, "fleet-phone").Health; h != nil {
		t.Errorf("unknown machine: %+v", h)
	}

	var down toolbox.List
	c := client{t: t, base: base, token: d.OwnerToken}
	if status := c.do("GET", "/api/toolbox?state=down", nil, &down); status != 200 || len(down.Resources) < 2 {
		t.Fatalf("down: %d, %d lines", status, len(down.Resources))
	}
}
