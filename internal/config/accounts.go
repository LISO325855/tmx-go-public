package config

import (
	"os"
	"path/filepath"
	"strings"
)

// UseUserAccounts allows shared user logins only on a production home, or by an
// explicit opt-in. Temporary, trial and test homes remain isolated even if a
// production config was copied into them.
func (c Config) UseUserAccounts(home string) bool {
	if c.AllowUserAccounts {
		return true
	}
	if !c.Production() || home == "" {
		return false
	}
	abs, err := ResolveAccountPath(home)
	if err != nil {
		return false
	}
	tempDir := os.TempDir()
	temp, err := ResolveAccountPath(tempDir)
	if err != nil {
		if !distinctAccountVolumes(abs, tempDir, err) {
			return false
		}
	} else {
		rel, err := filepath.Rel(temp, abs)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return false
		}
	}
	for _, part := range strings.FieldsFunc(strings.ToLower(abs), func(r rune) bool { return r == '/' || r == '\\' || r == '-' || r == '_' || r == '.' }) {
		if strings.Contains(part, "trial") || strings.Contains(part, "test") || part == "tmp" || part == "temp" {
			return false
		}
	}
	return true
}
