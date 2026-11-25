package api_test

import (
	"fmt"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/peers"
	"github.com/blackcode-ch/tmx-go/internal/store"
)

func TestPeerLeadAttestationBoundary(t *testing.T) {
	s := newPeerSetup(t)
	db, err := store.Open((config.Paths{Home: s.d.Home}).DB())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i, tc := range []struct {
		name, sender, host, header, behalf, token, jsonProof string
		status                                               int
		want                                                 string
	}{
		{"own agent", "head", "hosta", "head", "daemon", s.pair, "", 201, "head"},
		{"spoofed agent", "head", "hosta", "worker", "daemon", s.pair, "", 201, ""},
		{"relay", "head", "hosta", "head", "agent:head@hosta", s.pair, "", 201, ""},
		{"body claim", "head", "hosta", "", "daemon", s.pair, "head", 201, ""},
		{"other host", "head", "hostc", "head", "daemon", s.pair, "", 403, ""},
		{"unpaired", "head", "hosta", "head", "daemon", "", "", 401, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := fmt.Sprintf("%012x", i+1)
			msg := incoming(id, "test", "head")
			msg["sender"] = tc.sender
			msg["sender_host"] = tc.host
			msg["verified_sender"] = tc.jsonProof
			status := peerDo(t, s.url, "POST", "/api/messages/incoming", "hosta", tc.token, map[string]string{peers.HeaderVerifiedAgent: tc.header, peers.HeaderOnBehalf: tc.behalf}, msg, nil)
			if status != tc.status {
				t.Fatalf("status %d want %d", status, tc.status)
			}
			proof, err := db.PeerByWire("hosta", id)
			if tc.status == 201 {
				if err != nil || proof.VerifiedSender != tc.want {
					t.Fatalf("proof %+v %v", proof, err)
				}
			} else if err == nil {
				t.Fatal("refused mail stored")
			}
		})
	}
}

func TestFleetLeadConfigOwnerAndPairing(t *testing.T) {
	s := newPeerSetup(t)
	paths := config.Paths{Home: s.d.Home}
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Profile = config.ProfileProduction
	cfg.Machine = "hostb"
	cfg.Peers = []config.Peer{{Name: "hosta", URL: "https://hosta.example.ts.net"}}
	if err := config.Save(paths, cfg); err != nil {
		t.Fatal(err)
	}
	owner := client{t, s.url, s.d.OwnerToken}
	agent := client{t, s.url, s.d.AgentToken(t, "head")}
	endpoint := "/api/config/fleet-lead"
	request := map[string]string{"fleet_lead": "head@hosta"}
	if code := agent.do("PUT", endpoint, request, nil); code != 403 {
		t.Fatalf("agent: %d", code)
	}
	if code := agent.do("GET", endpoint, nil, nil); code != 403 {
		t.Fatalf("agent read: %d", code)
	}
	if code := owner.do("PUT", endpoint, map[string]string{"fleet_lead": "head@hostc"}, nil); code != 409 {
		t.Fatalf("unpaired: %d", code)
	}
	if code := owner.do("PUT", endpoint, request, nil); code != 200 {
		t.Fatalf("owner: %d", code)
	}
	cfg, err = config.Load(paths)
	if err != nil || cfg.FleetLead != "head@hosta" {
		t.Fatalf("persisted setting %q %v", cfg.FleetLead, err)
	}
	if code := owner.do("DELETE", "/api/peers/hosta", nil, nil); code != 200 {
		t.Fatalf("remove: %d", code)
	}
	cfg, err = config.Load(paths)
	if err != nil || cfg.FleetLead != "head@hosta" {
		t.Fatalf("removed lead %q %v", cfg.FleetLead, err)
	}
}
