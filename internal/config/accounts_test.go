package config

import (
	"path/filepath"
	"testing"
)

func TestUseUserAccounts(t *testing.T) {
	production := Config{Profile: ProfileProduction}
	normal := filepath.Join(filepath.VolumeName(t.TempDir())+string(filepath.Separator), "tmx-production")
	if !production.UseUserAccounts(normal) {
		t.Fatal("production denied")
	}
	for _, home := range []string{t.TempDir(), normal + "-trial", normal + "-test", ""} {
		if production.UseUserAccounts(home) {
			t.Fatalf("unsafe home allowed: %s", home)
		}
	}
	if (Config{}).UseUserAccounts(normal) {
		t.Fatal("trial allowed")
	}
	if !(Config{AllowUserAccounts: true}).UseUserAccounts(t.TempDir()) {
		t.Fatal("explicit opt-in denied")
	}
}
