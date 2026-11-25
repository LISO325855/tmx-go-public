package store

import (
	"path/filepath"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

func TestPeerAttestationSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tmx.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	m := model.Message{ID: "attested", From: "fleet:head@hosta", To: "local", Body: "hello", State: model.MsgQueued, CreatedAt: 1}
	p := PeerMessage{LocalID: m.ID, Peer: "hosta", WireID: "0123456789ab", Direction: PeerIn, VerifiedSender: "head", VerifiedGeneration: "durable-epoch"}
	if err := s.PutPeerMessage(m, p); err != nil {
		s.Close()
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.PeerByLocal(m.ID)
	if err != nil || got.VerifiedSender != "head" || got.VerifiedGeneration != "durable-epoch" {
		t.Fatalf("proof %+v %v", got, err)
	}
}
