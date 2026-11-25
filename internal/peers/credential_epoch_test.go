package peers

import (
	"github.com/blackcode-ch/tmx-go/internal/config"
	"testing"
)

func TestCredentialEpochRevokesProofAcrossRestartAndRePair(t *testing.T) {
	paths := config.Paths{Home: t.TempDir()}
	cfg := config.Config{Profile: config.ProfileProduction, Machine: "forge", Peers: []config.Peer{{Name: "hosta", URL: "https://hosta.example.ts.net"}}}
	c := New(cfg, paths, nil)
	first := NewPairToken()
	if _, err := c.Tokens().Store("hosta", first, "test"); err != nil {
		t.Fatal(err)
	}
	epoch := c.Tokens().CredentialGeneration("hosta")
	restarted := New(cfg, paths, nil)
	if !restarted.ProofCurrent("hosta", epoch) {
		t.Fatal("restart lost epoch")
	}
	if _, err := restarted.Tokens().Store("hosta", NewPairToken(), "test"); err != nil {
		t.Fatal(err)
	}
	if c.ProofCurrent("hosta", epoch) {
		t.Fatal("rotation retained old proof")
	}
	if _, ok := c.Tokens().Identify(first); !ok {
		t.Fatal("ordinary rotation overlap changed")
	}
	next := c.Tokens().CredentialGeneration("hosta")
	token := c.Tokens().For("hosta")
	if err := c.Tokens().Remove("hosta"); err != nil {
		t.Fatal(err)
	}
	if c.ProofCurrent("hosta", next) {
		t.Fatal("removed proof accepted")
	}
	if _, err := c.Tokens().Store("hosta", token, "test"); err != nil {
		t.Fatal(err)
	}
	if c.ProofCurrent("hosta", next) {
		t.Fatal("same-token re-pair restored proof")
	}
	if New(cfg, paths, nil).ProofCurrent("hosta", next) {
		t.Fatal("restart restored revoked proof")
	}
}
