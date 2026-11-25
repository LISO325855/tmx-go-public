package cli

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"
)

type logMirrorFunc func([]byte) (int, error)

func (f logMirrorFunc) Write(p []byte) (int, error) { return f(p) }

func TestDaemonLogSurvivesFailedStderr(t *testing.T) {
	var file bytes.Buffer
	var recovered bytes.Buffer
	calls := 0
	writer := &daemonLogWriter{file: &file, stderr: logMirrorFunc(func(p []byte) (int, error) {
		calls++
		if calls == 1 {
			return 0, io.ErrClosedPipe
		}
		if calls == 2 {
			return 0, nil
		}
		return recovered.Write(p)
	})}
	log := slog.New(slog.NewTextHandler(writer, nil))
	log.Info("daemon started")
	log.Info("still logging")
	log.Info("stderr recovered")
	for _, want := range []string{"daemon started", "still logging", "stderr recovered", "failures=2"} {
		if !strings.Contains(file.String(), want) {
			t.Errorf("file missing %q: %s", want, file.String())
		}
	}
	if writer.stderrFailures.Load() != 2 || !strings.Contains(recovered.String(), "stderr recovered") {
		t.Fatalf("mirror failures=%d, output=%q", writer.stderrFailures.Load(), recovered.String())
	}
}
