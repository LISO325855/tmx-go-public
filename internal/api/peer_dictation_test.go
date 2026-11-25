package api

import (
	"context"
	"github.com/blackcode-ch/tmx-go/internal/peers"
	"net/http/httptest"
	"testing"
)

func TestPeerDictationBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, peer, behalf, kind string
		status                   int
	}{
		{"unpaired", "", "operator", "application/json", 403},
		{"agent", "hosta", "agent:worker", "application/json", 403},
		{"device", "hosta", "device:phone", "application/json", 403},
		{"file upload", "hosta", "operator", "audio/wav", 415},
		{"operator", "hosta", "operator", "application/json", 501},
		{"daemon", "hosta", "daemon", "application/json", 501},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/peer/dictate", nil)
			req = req.WithContext(context.WithValue(req.Context(), peerCtxKey{}, peerCaller{name: tc.peer}))
			req.Header.Set(peers.HeaderOnBehalf, tc.behalf)
			req.Header.Set("Content-Type", tc.kind)
			rec := httptest.NewRecorder()
			(&Server{}).peerDictate(rec, req)
			if rec.Code != tc.status {
				t.Fatal(rec.Code, rec.Body.String())
			}
			if tc.status == 501 && req.Header.Get(transcriptionLocalOnly) != "true" {
				t.Fatal("must prohibit forwarding")
			}
		})
	}
}
