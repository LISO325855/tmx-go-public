package update

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/rollback"
	"github.com/blackcode-ch/tmx-go/internal/store"
	"golang.org/x/sys/windows"
)

func TestBlockedDesktopMigrationLeavesExeAndFlavour(t *testing.T) {
	const tag = "fleet-v1.0.3"
	g := newFakeGitHub(t)
	key, pin := newKey(t)
	signedWindowsRelease(t, g, tag, key)
	paths := config.Paths{Home: t.TempDir()}
	db, err := store.Open(paths.DB())
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	exe := filepath.Join(paths.Home, "tmxg.exe")
	writeExe(t, exe, tag)
	migrationConfig(t, paths, config.FlavourDesktop, false)
	path, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)

	u, restarts := migrationInstall(t, g, pin, paths, exe, tag, false)
	u.checkRelease(context.Background(), true)
	runScheduledMigration(t, u)
	st := u.Status()
	if *restarts != 0 || st.State != StateError || !strings.Contains(st.Error, "cannot move the old") ||
		st.Flavour != config.FlavourDesktop || st.RunningFlavour != config.FlavourHeadless {
		t.Fatalf("blocked rename changed install: %+v, restarts=%d", st, *restarts)
	}
	if b, err := os.ReadFile(exe); err != nil || string(b) != "binary "+tag {
		t.Fatalf("old executable not retained: %q, %v", b, err)
	}
	if _, exists, err := rollback.OnProbation(u.checkpoint()); err != nil || exists || rollback.AwaitingMigration(u.checkpoint()) {
		t.Fatalf("failed first rename left active update state: %v, %v", exists, err)
	}
}
