package codexguard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/blackcode-ch/tmx-go/internal/config"
)

// Refresh verified Microsoft system targets and pin newly discovered tools.
func refreshSystemManifest(path string, next Manifest, verify func(string) error) (bool, error) {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var old Manifest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if !json.Valid(body) || dec.Decode(&old) != nil {
		return false, fmt.Errorf("invalid managed Codex manifest")
	}
	if old.Codex != next.Codex || old.Guard != next.Guard {
		return false, nil
	}
	pins := []*Pin{}
	defer func() {
		for _, p := range pins {
			p.Close()
		}
	}()
	check := func(before, after Target) bool {
		if before == after {
			return true
		}
		if before.Path != after.Path || before.SHA256 == "" || after.SHA256 == "" {
			return false
		}
		p, err := OpenPin(after.Path)
		if err != nil {
			return false
		}
		pins = append(pins, p)
		return p.SHA256 == after.SHA256 && verify(after.Path) == nil
	}
	if !check(old.Shell, next.Shell) {
		return false, nil
	}
	for name, previous := range old.Targets {
		if target, ok := next.Targets[name]; !ok || !check(previous, target) {
			return false, nil
		}
	}
	changed := len(pins)
	added := []string{}
	for name, target := range next.Targets {
		if _, exists := old.Targets[name]; exists {
			continue
		}
		p, err := OpenPin(target.Path)
		if err != nil {
			return false, err
		}
		pins = append(pins, p)
		if p.SHA256 != target.SHA256 {
			return false, fmt.Errorf("managed Codex file changed: %s (expected SHA-256 %s, actual SHA-256 %s)", target.Path, target.SHA256, p.SHA256)
		}
		added = append(added, name)
	}
	if len(pins) == 0 {
		return false, nil
	}
	data, err := json.Marshal(next)
	if err != nil {
		return false, err
	}
	if err := config.WriteSecretFile(path, data); err != nil {
		return false, err
	}
	slog.Info("refreshed Codex guard manifest", "manifest", path, "verified_system_updates", changed, "added_targets", added)
	return true, nil
}
