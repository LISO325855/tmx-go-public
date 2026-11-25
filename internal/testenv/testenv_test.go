package testenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWhatIsRemoved(t *testing.T) {
	for name, want := range map[string]bool{
		"TMXG_HOME": true, "TMXG_URL": true, "TMXG_PORT": true, "TMXG_AGENT_TOKEN": true, "tmxg_home": true,
		"TMX_RELAY_TOKEN": true, "TMX_GITHUB_TOKEN": true, "TMX_SECRET_MAIL": true, "TMX_RELEASE_SIGNING_KEY": true,
		"TMX_HOME": true, "TMXG_AGENT_ID": true,
		"TMXG_LIVE": false, "TMXG_SOAK": false, "TMXG_FAKE_CLAUDE": false, "TMX_VOICE_MODEL": false,
		"TMX_WATCHDOG_TEST_ROLE": false, Marker: false, "HOME": false, "GITHUB_TOKEN": false, "PATH": false,
	} {
		if got := Removed(name); got != want {
			t.Errorf("%s: removed %v, want %v", name, got, want)
		}
	}
}

// init has run before this test: whatever shell started it, this process points at no install.
func TestThisProcessPointsAtNoInstall(t *testing.T) {
	if os.Getenv(Marker) == "" {
		t.Fatal("the process is not marked")
	}
	for _, name := range []string{"TMXG_URL", "TMXG_PORT", "TMXG_AGENT_TOKEN", "TMX_HOME"} {
		if v, ok := os.LookupEnv(name); ok {
			t.Errorf("%s is still set (%d characters)", name, len(v))
		}
	}
	home := os.Getenv("TMXG_HOME")
	if rel, err := filepath.Rel(os.TempDir(), home); err != nil || strings.HasPrefix(rel, "..") || !strings.HasPrefix(filepath.Base(home), "tmxg-test-home-") {
		t.Errorf("TMXG_HOME is %q, want a folder of this test under the temp dir", home)
	}
}

// A program a test starts itself keeps the home and token the test gave it: Clean leaves a
// marked process tree alone.
func TestAMarkedTreeIsLeftAlone(t *testing.T) {
	t.Setenv("TMXG_URL", "http://127.0.0.1:1")
	t.Setenv("TMXG_AGENT_TOKEN", "chosen-by-the-test")
	Clean()
	if os.Getenv("TMXG_URL") == "" || os.Getenv("TMXG_AGENT_TOKEN") == "" {
		t.Fatal("Clean removed what a test set on purpose")
	}
	t.Setenv(Marker, "")
	Clean()
	if os.Getenv("TMXG_URL") != "" || os.Getenv("TMXG_AGENT_TOKEN") != "" {
		t.Fatal("an unmarked process kept variables that point at an install")
	}
}
