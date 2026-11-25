package accounts

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/config"
)

var ErrNotOwned = errors.New("this node does not own this account; sign out from the production node or the account's own home")

// owns checks the effective credential directory before reading or changing an account.
// Resolve existing ancestors too: a redirected home must not escape the node's storage.
func (s *Service) owns(e *entry) bool {
	if s.o.AllowUserLogins {
		return true
	}
	key := map[string]string{"claude": "CLAUDE_CONFIG_DIR", "codex": "CODEX_HOME", "gemini": "GEMINI_CLI_HOME", "antigravity": "USERPROFILE"}[string(e.def.h)]
	var home string
	for _, v := range s.scope(e).env() {
		if k, val, ok := strings.Cut(v, "="); ok && strings.EqualFold(k, key) {
			home = val
		}
	}
	if home == "" {
		return false
	}
	root, err := config.ResolveAccountPath(s.o.NodeHome)
	if err != nil {
		return false
	}
	target, err := config.ResolveAccountPath(home)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Prepare only explicitly scoped homes, after ownership has been checked.
func (s *Service) prepareHome(e *entry) error {
	if !s.owns(e) {
		return ErrNotOwned
	}
	if len(e.home) == 0 {
		return nil
	}
	_, home, _ := strings.Cut(e.home[0], "=")
	if e.def.h == Claude {
		_, err := config.EnsureClaudeDir(home)
		return err
	}
	return config.MkdirSecret(home)
}
