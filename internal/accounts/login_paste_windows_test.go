//go:build windows

package accounts

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"golang.org/x/sys/windows"
)

func TestClaudeSyntheticPasteThroughConPTY(t *testing.T) {
	code := "synthetic-" + strings.Repeat("aZ09_#-", 512)
	frame := []byte("\x1b[200~" + code + "\x1b[201~\r")
	if os.Getenv("TMX_TEST_SYNTHETIC_LOGIN_PASTE") == "1" {
		input := windows.Handle(os.Stdin.Fd())
		if err := windows.SetConsoleMode(input, windows.ENABLE_VIRTUAL_TERMINAL_INPUT); err != nil {
			os.Exit(2)
		}
		_, _ = os.Stdout.WriteString("SYNTHETIC_READY\r\n")
		got := make([]byte, len(frame))
		if _, err := io.ReadFull(os.Stdin, got); err != nil || !bytes.Equal(got, frame) {
			os.Exit(3)
		}
		_, _ = os.Stdout.WriteString("SYNTHETIC_MATCH\r\n")
		os.Exit(0)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	session, err := startLogin(model.HarnessClaude, t.TempDir(), exe, []string{"-test.run=^TestClaudeSyntheticPasteThroughConPTY$"}, append(os.Environ(), "TMX_TEST_SYNTHETIC_LOGIN_PASTE=1"))
	if err != nil {
		t.Fatal(err)
	}
	defer session.stop(time.Second)
	sub := session.attach()
	defer session.detach(sub)
	deadline := time.After(10 * time.Second)
	var output strings.Builder
	output.Write(sub.Replay)
	waitFor := func(marker string) {
		t.Helper()
		for !strings.Contains(output.String(), marker) {
			select {
			case chunk, ok := <-sub.C():
				if !ok {
					t.Fatalf("synthetic PTY exited before %s", marker)
				}
				output.Write(chunk)
			case <-deadline:
				t.Fatalf("synthetic PTY did not reach %s", marker)
			}
		}
	}
	waitFor("SYNTHETIC_READY")
	if err := session.write(frame); err != nil {
		t.Fatal(err)
	}
	waitFor("SYNTHETIC_MATCH")
}
