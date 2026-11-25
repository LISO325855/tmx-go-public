package update

import (
	"fmt"

	"github.com/blackcode-ch/tmx-go/internal/config"
)

// FleetKeyID is the id of the first fleet key (and of the legacy update.fleet_public_key). Fleet
// private key material is provisioned separately and never ships with the binary.
const FleetKeyID = config.LegacyFleetKeyID

// FleetReleasePublicKey bootstraps installs that have no local pins. Explicit pins and
// rotation policy replace this trust anchor; malformed configuration never falls back.
const FleetReleasePublicKey = "6ec2frMWg6H02qJcMJWdxyJP6mSjQsojBsyW3/3/42c="

// FleetSignatureContext separates fleet signatures from signatures of raw sums.
// The purpose, repository, and tag each precede SHA256SUMS as an LF-terminated line.
const FleetSignatureContext = "tmx-fleet-release-v1"

// FleetExtraSignatureContext isolates on-demand extras from daemon releases.
const FleetExtraSignatureContext = "tmx-fleet-extra-v1"

// FleetWebAuthnContext and FleetExtraWebAuthnContext start the preimage of a webauthn signature's
// challenge (webauthnChallenge), for releases and extras.
const (
	FleetWebAuthnContext      = "tmx-fleet-release-v2"
	FleetExtraWebAuthnContext = "tmx-fleet-extra-v2"
)

// trustedKeys reads local pins, or the compiled bootstrap key for an unconfigured install. Options.Keys (base64 Ed25519
// keys under FleetKeyID, require ["ed25519"]) is an explicit dependency injection seam for
// tests, never an upstream fallback.
func (u *Updater) effectiveKeys(s config.UpdateConfig) ([]config.FleetKey, []string, string, error) {
	if u.o.Keys != nil {
		keys := []config.FleetKey{}
		for _, k := range u.o.Keys {
			keys = append(keys, config.FleetKey{ID: FleetKeyID, Type: config.KeyEd25519, Key: k})
		}
		return keys, []string{config.KeyEd25519}, "test", nil
	}
	keys, require, err := s.PinnedKeys()
	if err != nil {
		return keys, require, "configured", err
	}
	origin := "configured"
	if len(keys) == 0 && s.FleetKeys == nil && s.FleetPublicKey == "" && s.Require == nil && s.KeysSerial == 0 {
		keys = []config.FleetKey{{ID: FleetKeyID, Type: config.KeyEd25519, Key: FleetReleasePublicKey}}
		origin = "built-in"
	}
	return keys, require, origin, nil
}

func (u *Updater) trustedKeys() (keySet, error) {
	keys, require, _, err := u.effectiveKeys(u.settings())
	if err != nil {
		return keySet{}, fmt.Errorf("update keys invalid: %w", err)
	}
	ks, err := newKeySet(keys, require)
	if err != nil {
		return keySet{}, fmt.Errorf("update keys invalid: %w", err)
	}
	return ks, nil
}
