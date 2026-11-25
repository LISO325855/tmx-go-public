package codexguard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/blackcode-ch/tmx-go/internal/config"
)

// SyncOwnerAuth copies only auth.json into the isolated home, never the owner's
// config or plugins. Called at each launch so a deleted owner login is not
// silently replaced by a stale isolated credential. Contents are never logged.
// Runtime auth in this directory is owner state, not project/source content.
func SyncOwnerAuth(sourceHome, destHome string) error {
	if !filepath.IsAbs(sourceHome) || !filepath.IsAbs(destHome) {
		return fmt.Errorf("Codex authentication homes must be absolute")
	}
	path := filepath.Join(sourceHome, "auth.json")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		// The owner's login is gone (e.g. signed out, switched accounts): never leave the
		// isolated copy behind looking like a still-valid login.
		_ = DeleteOwnerAuth(destHome)
		return fmt.Errorf("Codex owner file login is unavailable; sign in before starting this agent")
	}
	b, err := os.ReadFile(path)
	if err != nil || !json.Valid(b) {
		return fmt.Errorf("Codex owner login could not be read")
	}
	if err := config.WriteSecretFile(filepath.Join(destHome, "auth.json"), b); err != nil {
		return fmt.Errorf("cannot install isolated Codex login: %w", err)
	}
	return nil
}

// DeleteOwnerAuth removes the isolated copy of the owner's Codex login. Called
// when an agent stops or is shelved, so a credential copy never lingers for an
// agent that is not currently running; SyncOwnerAuth makes a fresh one at the
// next launch or resume. Missing is not an error.
func DeleteOwnerAuth(destHome string) error {
	if !filepath.IsAbs(destHome) {
		return fmt.Errorf("Codex authentication home must be absolute")
	}
	if err := os.Remove(filepath.Join(destHome, "auth.json")); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot remove isolated Codex login")
	}
	return nil
}
