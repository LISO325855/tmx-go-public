package codexguard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRenameExecutableWithGuardedLeaseAlive(t *testing.T) {
	root := t.TempDir()
	codex, daemon := filepath.Join(root, "codex.exe"), filepath.Join(root, "tmxg.exe")
	for _, path := range []string{codex, daemon} {
		if err := os.WriteFile(path, []byte("old executable"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	base := filepath.Join(root, "managed")
	guard := Guard{Own: root, Workspace: root, State: filepath.Join(root, "state")}
	old, err := Prepare(base, root, codex, daemon, root, guard)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if err := os.WriteFile(daemon, []byte("overwrite"), 0600); err == nil {
		t.Fatal("executable pin allowed writes")
	}
	if err := os.Rename(daemon, daemon+".old"); err != nil {
		t.Fatalf("rename with active guarded launch lease: %v", err)
	}
	old.CloseShimPin()
	if err := os.WriteFile(daemon, []byte("new executable"), 0600); err != nil {
		t.Fatal(err)
	}
	next, err := Prepare(base, root, codex, daemon, root, guard)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	oldShim := filepath.Join(old.Shims, ShimNames[0]+".exe")
	newShim := filepath.Join(next.Shims, ShimNames[0]+".exe")
	for path, want := range map[string]string{oldShim: "old executable", newShim: "new executable"} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v, want %q", path, got, err, want)
		}
	}
}
