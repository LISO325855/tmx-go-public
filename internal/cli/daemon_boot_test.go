package cli

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/config"
)

func TestDaemonExplicitHomeLogsInvalidConfig(t *testing.T) {
	wrong, real := t.TempDir(), t.TempDir()
	t.Setenv(config.EnvHome, wrong)
	oldLogger := slog.Default()
	defer slog.SetDefault(oldLogger)
	p := config.Paths{Home: real}
	if err := os.WriteFile(p.ConfigFile(), []byte("{invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if code := run([]string{"daemon", "--home", real}, &out, &errw); code == 0 {
		t.Fatal("invalid config was accepted")
	}
	log, err := os.ReadFile(p.DaemonLog())
	if err != nil || !strings.Contains(string(log), "tmx daemon starting") || !strings.Contains(string(log), "tmx daemon exited with an error") {
		t.Fatalf("explicit home did not log startup and config failure: %s %v", log, err)
	}
	if _, err := os.Stat(filepath.Join(wrong, "logs")); !os.IsNotExist(err) {
		t.Fatalf("unexpected side effects in inherited TMXG_HOME: %v", err)
	}
}
