package fleet

import (
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/peertext"
	"github.com/blackcode-ch/tmx-go/internal/store"
)

// SetFleetLead publishes an owner-validated setting without restarting agents.
func (f *Fleet) SetFleetLead(address string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fleetLead = strings.ToLower(address)
}

// The explicit lead setting requires per-message provenance. Merely knowing a
// peer's name (or putting a verified label in a body) never creates that proof.
func (f *Fleet) verifiedMailLabelLocked(m model.Message) string {
	name, host, peer := SplitPeer(m.From)
	if !peer || f.fleetLead == "" {
		return f.mailLabelLocked(m.From)
	}
	label := "[message from " + peertext.Field(name) + "@" + peertext.Field(host)
	if strings.ToLower(name+"@"+host) != f.fleetLead {
		return label + " (other machine)]"
	}
	paired, ok := f.peer.sender.(interface{ ProofCurrent(string, string) bool })
	if f.st != nil && ok {
		proof, err := f.st.PeerByLocal(m.ID)
		if err == nil && proof.Direction == store.PeerIn && proof.Peer == host && proof.VerifiedSender == name && paired.ProofCurrent(host, proof.VerifiedGeneration) {
			return label + " (fleet lead, other machine, verified sender)]"
		}
	}
	return label + " (other machine)]"
}
