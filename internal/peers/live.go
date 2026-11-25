package peers

import (
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/config"
)

// Paired is used when rendering a stored, attested fleet-lead message. A removed
// or tokenless relationship must not grant authority merely because it once did.
func (c *Client) Paired(name string) bool {
	return c.Known(name) && c.tokens.For(name) != ""
}

func (c *Client) lookup(name string) (config.Peer, bool) {
	c.peersMu.RLock()
	defer c.peersMu.RUnlock()
	p, ok := c.peers[strings.ToLower(name)]
	return p, ok
}

func (c *Client) peerSnapshot() []config.Peer {
	c.peersMu.RLock()
	defer c.peersMu.RUnlock()
	out := make([]config.Peer, 0, len(c.peers))
	for _, p := range c.peers {
		out = append(out, p)
	}
	return out
}

// ReplacePeers publishes the daemon's persisted peer list without restarting its pollers.
func (c *Client) ReplacePeers(list []config.Peer) {
	edit := MutationMutex(c.tokens.path)
	edit.Lock()
	defer edit.Unlock()
	c.ReplacePeersLocked(list)
}

// ReplacePeersLocked requires the store's MutationMutex to be held.
func (c *Client) ReplacePeersLocked(list []config.Peer) {
	c.peersMu.Lock()
	defer c.peersMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	next := make(map[string]config.Peer, len(list))
	for _, p := range list {
		next[p.Name] = p
		if old, ok := c.status[p.Name]; !ok || old.URL != p.URL {
			c.status[p.Name] = Status{Name: p.Name, URL: p.URL, Sessions: []Session{}}
		}
	}
	for name := range c.status {
		if _, ok := next[name]; !ok {
			delete(c.status, name)
		}
	}
	c.peers = next
}

// ProofCurrent runs under Fleet.mu while rendering/delivering mail. It never
// takes the mutation lock (mutations take that lock before Fleet.mu).
func (c *Client) ProofCurrent(host, generation string) bool {
	return generation != "" && c.Known(host) && c.tokens.CredentialGeneration(host) == generation
}

// PeerDeliveryLock serializes the final proof check/render with rotation/removal.
// Release before runner I/O. Call before acquiring Fleet.mu, never while holding it.
func (c *Client) PeerDeliveryLock() func() {
	lock := MutationMutex(c.tokens.path)
	lock.Lock()
	return lock.Unlock
}
