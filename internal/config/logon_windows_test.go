//go:build windows

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsHomePrefersInstalledData(t *testing.T) {
	local := t.TempDir()
	trial := filepath.Join(local, "tmxg-trial")
	if err := os.Mkdir(trial, 0700); err != nil {
		t.Fatal(err)
	}
	if got, err := windowsNodeHome(local, ""); err != nil || got != trial {
		t.Fatalf("fresh installation home = %q, %v", got, err)
	}
	installed := filepath.Join(local, "TiamatrixGo")
	if err := os.Mkdir(installed, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installed, "tmx.db"), []byte("existing state"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := windowsNodeHome(local, ""); err != nil || got != installed {
		t.Fatalf("boot with both homes = %q, %v", got, err)
	}
	registered := t.TempDir()
	if got, err := windowsNodeHome(local, registered); err != nil || got != registered {
		t.Fatalf("registered installation home = %q, %v", got, err)
	}
	if got, err := windowsNodeHome(local, filepath.Join(local, "missing")); err == nil || got != "" {
		t.Fatalf("missing registered home must not silently open trial: %q, %v", got, err)
	}
}

func TestWindowsLogonCommandPreservesHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "Tiamatrix data")
	line, err := windowsLogonCommand("C:\\Windows\\System32\\conhost.exe", "C:\\Program Files\\Tiamatrix Go\\tmxg.exe", home)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := logonHomeFromCommand(line); err != nil || got != home {
		t.Fatalf("registered home = %q, %v; command: %s", got, err, line)
	}
	if !strings.Contains(line, "--headless") {
		t.Fatal("native logon launch needs a hidden console")
	}
}
