package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/peers"
)

// peerInvite binds the one-time capability to the exact new machine the owner selected.
func (s *Server) peerInvite(w http.ResponseWriter, r *http.Request) {
	var p config.Peer
	if !decodeMax(w, r, &p, 4096) {
		return
	}
	p.Name = strings.ToLower(strings.TrimSpace(p.Name))
	url, err := config.PeerURL(p.URL)
	if err != nil || !config.ValidHostName(p.Name) {
		writeError(w, 400, "a machine name and HTTPS peer URL are required")
		return
	}
	p.URL = url
	if !s.RequireOwner(w, r, "invite a new fleet peer", p) {
		return
	}
	s.peerEditMu.Lock()
	defer s.peerEditMu.Unlock()
	c, why := s.joinConfig()
	if why != "" {
		writeError(w, 409, why)
		return
	}
	if p.Name == strings.ToLower(c.Machine) || inFleet(c, s.pairTokens(), p.Name) || addressInFleet(c, p.URL) {
		writeError(w, 409, "that machine name or address is already in the fleet")
		return
	}
	code, expires, err := s.join.issue(time.Now())
	if err != nil {
		writeErr(w, err)
		return
	}
	s.join.mu.Lock()
	s.join.bound = p
	s.join.mu.Unlock()
	s.log.Info("peer invite issued", "peer", p.Name, "expires", expires)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"code": code, "expires_at": expires.UnixMilli()})
}

// Removal revokes only this local relationship. The other machine can remove its
// stale entry separately; no remote owner's settings are changed implicitly.
func (s *Server) peerRemove(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(r.PathValue("name"))
	if !config.ValidHostName(name) {
		writeError(w, 400, "invalid peer name")
		return
	}
	if !s.RequireOwner(w, r, "remove a fleet peer", name) {
		return
	}
	s.peerEditMu.Lock()
	defer s.peerEditMu.Unlock()
	c, err := config.Load(s.f.Paths())
	if err != nil {
		writeErr(w, err)
		return
	}
	before := append([]config.Peer(nil), c.Peers...)
	next := make([]config.Peer, 0, len(before))
	for _, p := range before {
		if p.Name != name {
			next = append(next, p)
		}
	}
	c.Peers = next
	// Keep the explicit selection as a fail-closed tombstone: unpairing must
	// never restore legacy address-only authority for queued mail.
	if err := config.Save(s.f.Paths(), c); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.pairTokens().RemoveLocked(name); err != nil {
		c.Peers = before
		if undo := config.Save(s.f.Paths(), c); undo != nil {
			s.log.Error("peer removal rollback failed", "peer", name)
		}
		writeErr(w, err)
		return
	}
	if s.peers != nil {
		s.peers.ReplacePeersLocked(next)
	}
	s.f.SetFleetLead(c.FleetLead)
	s.log.Info("peer removed", "peer", name)
	writeJSON(w, 200, map[string]bool{"removed": true})
}

// Import is daemon-owned so the CLI cannot race its writes from another process.
func (s *Server) peerImportPython(w http.ResponseWriter, r *http.Request) {
	if !s.RequireOwner(w, r, "import this machine's Python peer credentials", nil) {
		return
	}
	dir, err := peers.PythonHome()
	if err != nil {
		writeErr(w, err)
		return
	}
	rows, err := peers.ImportPythonCurrent(s.f.Paths(), dir)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.log.Info("Python peer import completed")
	writeJSON(w, http.StatusOK, rows)
}
