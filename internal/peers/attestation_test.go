package peers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
)

func TestMailAttestationCannotComeFromRelayOrJSON(t *testing.T) {
	headers := make(chan string, 8)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Get(HeaderVerifiedAgent)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if _, ok := body["VerifiedSender"]; ok {
			t.Error("internal attestation serialized")
		}
		json.NewEncoder(w).Encode(map[string]any{"id": body["id"], "state": "queued"})
	}))
	defer server.Close()
	c := New(config.Config{Profile: config.ProfileProduction, Machine: "hosta", Peers: []config.Peer{{Name: "forge", URL: server.URL}}}, config.Paths{Home: t.TempDir()}, nil)
	c.SetTransport(server.Client().Transport)
	if _, err := c.Tokens().Store("forge", NewPairToken(), "hosta"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ sender, host, verified, want string }{
		{"head", "hosta", "head", "head"},
		{"head", "hosta", "", ""},
		{"head", "hostc", "head", ""},
		{"head", "hosta", "worker", ""},
		{"operator", "hosta", "operator", ""},
	} {
		err := c.Send("forge", fleet.PeerOutbound{ID: "0123456789ab", Sender: tc.sender, SenderHost: tc.host, VerifiedSender: tc.verified, To: "head", Body: "hello"})
		if err != nil {
			t.Fatal(err)
		}
		if got := <-headers; got != tc.want {
			t.Fatalf("header %q want %q", got, tc.want)
		}
	}
	// Even a relay body claiming a verified sender cannot manufacture the header.
	status, response := c.Remote(context.Background(), "forge", "POST", "/api/messages", "agent:head@hosta", json.RawMessage(`{"id":"0123456789ab","verified_sender":"head"}`))
	if status != 200 {
		t.Fatalf("relay: %d %s", status, response)
	}
	if got := <-headers; got != "" {
		t.Fatalf("relay attestation %q", got)
	}
}
