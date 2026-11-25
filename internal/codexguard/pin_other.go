//go:build !windows

package codexguard

import "os"

// Non-Windows pins only support fixture verification, not Windows admission.
func openPinned(path string, _ bool) (*os.File, error) { return os.Open(path) }

func shimGeneration(p *Pin) (string, error) { return p.SHA256, nil }
