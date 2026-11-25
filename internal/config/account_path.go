package config

import (
	"errors"
	"os"
	"path/filepath"
)

// ResolveAccountPath resolves an account path, including the existing ancestors
// of a home which has not yet been created. Windows junctions are resolved too.
func ResolveAccountPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var tail []string
	for {
		if _, err := os.Lstat(abs); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", os.ErrNotExist
		}
		tail = append(tail, filepath.Base(abs))
		abs = parent
	}
	abs, err = finalAccountPath(abs)
	if err != nil {
		return "", err
	}
	for i := len(tail) - 1; i >= 0; i-- {
		abs = filepath.Join(abs, tail[i])
	}
	return abs, nil
}
