package companion_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/companion"
)

// A Companion that advertises its engine in the second folder (on Windows the development
// build writes under %LOCALAPPDATA%\Companion, not %APPDATA%) is found; a live engine in the
// first folder still comes first.
func TestLocateLooksInEveryDataFolder(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	t.Setenv("TMXG_COMPANION_DATA", first+string(os.PathListSeparator)+second)
	write := func(dir string, port int) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, "agents"), 0o700); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(map[string]any{"url": "http://127.0.0.1:" + itoa(port), "pid": os.Getpid()})
		if err := os.WriteFile(filepath.Join(dir, "agents", "engine.json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if l, _ := companion.Locate(""); l.Source != companion.SourceDefault {
		t.Fatalf("nothing advertised: %+v", l)
	}
	write(second, 8011)
	if l, _ := companion.Locate(""); l.Source != companion.SourceEngine || l.URL != "http://127.0.0.1:8011" {
		t.Fatalf("an engine in the second folder: %+v", l)
	}
	write(first, 8124)
	if l, _ := companion.Locate(""); l.URL != "http://127.0.0.1:8124" {
		t.Fatalf("an engine in the first folder comes first: %+v", l)
	}
	if got := companion.UserDataDir(); got != first {
		t.Fatalf("UserDataDir %q, want the first folder", got)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
