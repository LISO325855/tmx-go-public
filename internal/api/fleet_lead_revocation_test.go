package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/api"
	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/peers"
	"github.com/blackcode-ch/tmx-go/internal/store"
)

func TestOverlapCredentialCannotMintLeadProof(t *testing.T) {
	s := newPeerSetup(t)
	current := peers.NewPairToken()
	if _, err := s.pc.Tokens().Store("hosta", current, "test"); err != nil {
		t.Fatal(err)
	}
	msg := incoming("0123456789ab", "hello", "head")
	hdr := map[string]string{peers.HeaderVerifiedAgent: "tmx-lead"}
	if code := peerDo(t, s.url, "POST", "/api/messages/incoming", "hosta", s.pair, hdr, msg, nil); code != 409 {
		t.Fatalf("overlap minted proof: %d", code)
	}
	if code := peerDo(t, s.url, "POST", "/api/messages/incoming", "hosta", current, hdr, msg, nil); code != 201 {
		t.Fatalf("current: %d", code)
	}
	db, err := store.Open((config.Paths{Home: s.d.Home}).DB())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	proof, err := db.PeerByWire("hosta", "0123456789ab")
	if err != nil || proof.VerifiedGeneration == "" || proof.VerifiedGeneration != s.pc.Tokens().CredentialGeneration("hosta") {
		t.Fatalf("proof epoch missing: %v", err)
	}
}

func TestLeadProofRefusedWhenPairRotatesAfterAuthentication(t *testing.T) {
	s := newPeerSetup(t)
	h := api.New(api.Options{Fleet: s.d.Fleet, OwnerToken: s.d.OwnerToken, Peers: s.pc}).Handler()
	raw, _ := json.Marshal(incoming("0123456789ab", "hello", "head"))
	body := &pausedRotationBody{entered: make(chan struct{}), resume: make(chan struct{}), Reader: bytes.NewReader(raw)}
	req := httptest.NewRequest(http.MethodPost, "http://localhost/api/messages/incoming", body)
	req.Header.Set(peers.HeaderFrom, "hosta")
	req.Header.Set(peers.HeaderToken, s.pair)
	req.Header.Set(peers.HeaderOnBehalf, "daemon")
	req.Header.Set(peers.HeaderVerifiedAgent, "tmx-lead")
	out := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { h.ServeHTTP(out, req); close(done) }()
	select {
	case <-body.entered:
	case <-done:
		t.Fatalf("early response %d", out.Code)
	case <-time.After(5 * time.Second):
		close(body.resume)
		t.Fatal("decode never entered")
	}
	if _, err := s.pc.Tokens().Store("hosta", peers.NewPairToken(), "test"); err != nil {
		close(body.resume)
		t.Fatal(err)
	}
	close(body.resume)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("request hung")
	}
	if out.Code != 409 {
		t.Fatalf("stale proof response: %d", out.Code)
	}
	db, err := store.Open((config.Paths{Home: s.d.Home}).DB())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.PeerByWire("hosta", "0123456789ab"); err == nil {
		t.Fatal("stale proof committed")
	}
}

// Stall the actual SQLite commit, then verify the peer transaction remains
// locked throughout it. This catches releasing the lock after validation.
func TestLeadProofCommitHoldsPeerTransaction(t *testing.T) {
	s := newPeerSetup(t)
	h := api.New(api.Options{Fleet: s.d.Fleet, OwnerToken: s.d.OwnerToken, Peers: s.pc}).Handler()
	paths := config.Paths{Home: s.d.Home}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(paths.DB()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	raw, _ := json.Marshal(incoming("0123456789ac", "hello", "head"))
	req := httptest.NewRequest(http.MethodPost, "http://localhost/api/messages/incoming", bytes.NewReader(raw))
	req.Header.Set(peers.HeaderFrom, "hosta")
	req.Header.Set(peers.HeaderToken, s.pair)
	req.Header.Set(peers.HeaderOnBehalf, "daemon")
	req.Header.Set(peers.HeaderVerifiedAgent, "tmx-lead")
	out := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { h.ServeHTTP(out, req); close(done) }()
	lock := peers.MutationMutex(paths.PeersFile())
	held := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if lock.TryLock() {
			lock.Unlock()
		} else {
			held = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if held {
		time.Sleep(30 * time.Millisecond)
		if lock.TryLock() {
			lock.Unlock()
			held = false
		}
	}
	if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("receipt did not finish")
	}
	if !held {
		t.Fatal("peer transaction released before durable commit")
	}
	if out.Code != 201 {
		t.Fatalf("receipt: %d", out.Code)
	}
}
