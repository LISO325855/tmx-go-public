//go:build !windows

package config

import "path/filepath"

func finalAccountPath(path string) (string, error) { return filepath.EvalSymlinks(path) }

func distinctAccountVolumes(string, string, error) bool { return false }
