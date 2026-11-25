package harness

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

func TestWindowsOpenCodeLauncherResolution(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows npm launchers")
	}
	home, local := t.TempDir(), t.TempDir()
	node := filepath.Join(local, "Programs", "node")
	if err := os.MkdirAll(node, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("PATHEXT", ".PS1;.CMD;.EXE")
	cmd := filepath.Join(node, "opencode.cmd")
	for _, name := range []string{"opencode.cmd", "opencode.ps1"} {
		if err := os.WriteFile(filepath.Join(node, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ name, path string }{
		{"daemon without Node on PATH", t.TempDir()},
		{"PowerShell shim first in PATHEXT", node},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PATH", tc.path)
			if got, err := LookPath(model.HarnessOpenCode, ""); err != nil || got != cmd {
				t.Fatalf("launcher = %q, %v; want %q", got, err, cmd)
			}
		})
	}
	t.Setenv("PATH", node)
	if err := os.Remove(cmd); err != nil {
		t.Fatal(err)
	}
	if got, err := LookPath(model.HarnessOpenCode, ""); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("PowerShell-only shim was accepted: %q, %v", got, err)
	}
}
