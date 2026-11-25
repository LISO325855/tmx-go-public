// Package peers is the Python fleet interop (DESIGN.md "Python fleet interop"): this daemon
// stays visible and sendable from the Python tmx daemons on the fleet's other machines, over
// their pair-token API. It keeps the pair tokens, hands one over to a peer whose name sorts
// after this machine's (the Python rule: the lower name generates it), reads each peer's agents
// and posts mail to them. Everything a peer returns is that machine's data, never instructions.
package peers

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
)

// rotationOverlap: after a rotation the previous token still works this long (Python OVERLAP_S).
const rotationOverlap = 10 * time.Minute

// minTokenLen: a pair token is 32 random bytes, 43 characters URL-safe base64 (Python store()).
const minTokenLen = 40

type tokenRec struct {
	CredentialGeneration string `json:"credential_generation,omitempty"`
	Token                string `json:"token"`
	OldToken             string `json:"old_token,omitempty"`
	OldUntil             int64  `json:"old_until,omitempty"` // Unix seconds
	Stored               int64  `json:"stored"`
	By                   string `json:"by,omitempty"`
}

// Tokens is secrets/peers.json: the pair token shared with each peer.
type Tokens struct {
	mu   *sync.Mutex
	path string
	now  func() time.Time
}

type storeLocks struct {
	edit       sync.Mutex
	io         sync.Mutex
	generation map[string]uint64
}

var tokenLocks sync.Map

// MutationMutex coordinates whole config/token/live-list transactions for this store.
// CLI mutations are routed through the daemon, so no second process writes alongside it.
func MutationMutex(path string) *sync.Mutex { return &locksFor(path).edit }
func locksFor(path string) *storeLocks {
	key, err := filepath.Abs(path)
	if err != nil {
		key = filepath.Clean(path)
	}
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	v, _ := tokenLocks.LoadOrStore(key, &storeLocks{generation: make(map[string]uint64)})
	return v.(*storeLocks)
}

// NewTokens returns the store at path (a missing file = nothing paired yet).
func NewTokens(path string) *Tokens {
	return &Tokens{path: path, now: time.Now, mu: &locksFor(path).io}
}

var ErrStalePair = errors.New("peer relationship changed; stale rotation refused")

// StoreIfCurrent is a compare-and-swap; an absent or replaced pair cannot be rotated.
func (t *Tokens) StoreIfCurrent(peer, token, by, expected string, generation uint64) (string, error) {
	edit := MutationMutex(t.path)
	edit.Lock()
	defer edit.Unlock()
	return t.StoreIfCurrentLocked(peer, token, by, expected, generation)
}

// StoreIfCurrentLocked requires MutationMutex(path) to be held by the caller.
func (t *Tokens) StoreIfCurrentLocked(peer, token, by, expected string, generation uint64) (string, error) {
	if expected == "" {
		return "", ErrStalePair
	}
	return t.store(peer, token, by, &expected, generation)
}

// Remove revokes both the current token and its rotation overlap, preserving other peers.
func (t *Tokens) Remove(peer string) error {
	edit := MutationMutex(t.path)
	edit.Lock()
	defer edit.Unlock()
	return t.RemoveLocked(peer)
}

// RemoveLocked requires MutationMutex(path) to be held by the caller.
func (t *Tokens) RemoveLocked(peer string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	rows, err := t.load()
	if err != nil {
		return err
	}
	delete(rows, strings.ToLower(peer))
	b, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return err
	}
	if err := config.WriteSecretFile(t.path, b); err != nil {
		return err
	}
	locksFor(t.path).generation[strings.ToLower(peer)]++
	return nil
}

// NewPairToken returns 32 random bytes, URL-safe base64 without padding (Python token_urlsafe(32)).
func NewPairToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Fingerprint is what the receiver answers to show it stored a token, without echoing it:
// 12 hex characters of its SHA-256.
func Fingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:12]
}

func (t *Tokens) load() (map[string]tokenRec, error) {
	rows := map[string]tokenRec{}
	b, err := os.ReadFile(t.path)
	if errors.Is(err, os.ErrNotExist) {
		return rows, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, fmt.Errorf("%s: %w", t.path, err)
	}
	return rows, nil
}

// For returns the pair token to send to peer ("" = not paired yet).
func (t *Tokens) For(peer string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	rows, err := t.load()
	if err != nil {
		return ""
	}
	return rows[strings.ToLower(peer)].Token
}

// Identify returns the peer whose pair token (or, during a rotation's overlap, previous token)
// matches. Every entry is compared in constant time.
func (t *Tokens) Identify(token string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	rows, err := t.load()
	if err != nil || token == "" {
		return "", false
	}
	now := t.now().Unix()
	hit := ""
	for name, r := range rows {
		ok := r.Token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(r.Token)) == 1
		old := r.OldToken != "" && r.OldUntil > now && subtle.ConstantTimeCompare([]byte(token), []byte(r.OldToken)) == 1
		if (ok || old) && hit == "" {
			hit = name
		}
	}
	return hit, hit != ""
}

// Store keeps token for peer; a different token than the current one is a rotation (the old
// one keeps working for rotationOverlap). Idempotent for the same token. Returns its fingerprint.
func (t *Tokens) Store(peer, token, by string) (string, error) {
	edit := MutationMutex(t.path)
	edit.Lock()
	defer edit.Unlock()
	return t.StoreLocked(peer, token, by)
}

// StoreLocked requires MutationMutex(path) to be held by the caller.
func (t *Tokens) StoreLocked(peer, token, by string) (string, error) {
	return t.store(peer, token, by, nil, 0)
}

func (t *Tokens) store(peer, token, by string, expected *string, generation uint64) (string, error) {
	if len(token) < minTokenLen {
		return "", errors.New("a pair token is 32 random bytes (43 characters urlsafe)")
	}
	peer = strings.ToLower(peer)
	t.mu.Lock()
	defer t.mu.Unlock()
	rows, err := t.load()
	if err != nil {
		return "", err
	}
	r := rows[peer]
	if expected != nil && (subtle.ConstantTimeCompare([]byte(r.Token), []byte(*expected)) != 1 || locksFor(t.path).generation[peer] != generation) {
		return "", ErrStalePair
	}
	if r.Token == token {
		return Fingerprint(token), nil
	}
	if r.Token != "" {
		r.OldToken, r.OldUntil = r.Token, t.now().Add(rotationOverlap).Unix()
	}
	r.Token, r.Stored, r.By = token, t.now().Unix(), by
	r.CredentialGeneration = NewPairToken()
	rows[peer] = r
	b, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return "", err
	}
	if err := config.WriteSecretFile(t.path, b); err != nil {
		return "", err
	}
	locksFor(t.path).generation[peer]++
	return Fingerprint(token), nil
}

// Generation distinguishes remove/re-import even when an owner deliberately imports
// the same token again. It is process-local: no in-flight request survives restart.
// Reads and writes use the shared store IO lock; callers snapshot it with the token
// while holding MutationMutex.
func (t *Tokens) Generation(peer string) uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return locksFor(t.path).generation[strings.ToLower(peer)]
}

// CredentialGeneration is a durable, non-secret epoch for stored mail proof.
// Legacy records derive an epoch from their high-entropy token until next rotation.
// A remove/re-import creates a new random epoch even when the token is reused.
func (t *Tokens) CredentialGeneration(peer string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	rows, err := t.load()
	if err != nil {
		return ""
	}
	r := rows[strings.ToLower(peer)]
	if r.Token == "" {
		return ""
	}
	if r.CredentialGeneration != "" {
		return r.CredentialGeneration
	}
	sum := sha256.Sum256([]byte("tmx-mail-epoch:" + r.Token))
	return hex.EncodeToString(sum[:])
}
