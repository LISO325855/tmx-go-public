package api_test

import (
	"net/http"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/peers"
)

// A paired machine may launch an agent into an existing conversation for itself or its
// operator, never relayed for one of its agents or a device. (Whether the conversation exists
// is the fleet's check: here it does not, so an admitted request answers 404, a refused one 403.)
func TestPeerLaunchIntoConversationOnBehalf(t *testing.T) {
	p := newPeerSetup(t)
	req := map[string]any{"name": "moved", "conversation_id": "11111111-2222-4333-8444-555555555555"}
	for behalf, want := range map[string]int{
		"":                     http.StatusNotFound,
		"daemon":               http.StatusNotFound,
		"operator":             http.StatusNotFound,
		"agent:worker":         http.StatusForbidden,
		"agent:head@hosta":    http.StatusForbidden,
		"device:phone-1":       http.StatusForbidden,
		"something-unexpected": http.StatusForbidden,
	} {
		hdr := map[string]string{}
		if behalf != "" {
			hdr[peers.HeaderOnBehalf] = behalf
		}
		if got := peerDo(t, p.url, "POST", "/api/agents", "hosta", p.pair, hdr, req, nil); got != want {
			t.Errorf("on behalf of %q: status %d, want %d", behalf, got, want)
		}
	}
	// Without a conversation id the same relayed launch is not this rule's business.
	plain := map[string]any{"name": "relayed-plain"}
	if got := peerDo(t, p.url, "POST", "/api/agents", "hosta", p.pair, map[string]string{peers.HeaderOnBehalf: "agent:worker"}, plain, nil); got == http.StatusForbidden {
		t.Errorf("a relayed launch without a conversation id was refused by the conversation rule: %d", got)
	}
}
