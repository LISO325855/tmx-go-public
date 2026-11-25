package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexSandboxPreflight(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	bwrap := filepath.Join(dir, "bwrap")
	if err := os.WriteFile(bwrap, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := checkCodexSandbox(); err != nil {
		t.Fatalf("working Bubblewrap: %v", err)
	}
	if err := os.WriteFile(bwrap, []byte("#!/bin/sh\necho 'bwrap: setting up uid map: Permission denied' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := checkCodexSandbox(); err == nil || !strings.HasPrefix(err.Error(), "Codex sandbox cannot start:") {
		t.Fatalf("failed Bubblewrap: %v", err)
	}
}
