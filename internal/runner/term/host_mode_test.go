package term

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
	"github.com/blackcode-ch/tmx-go/internal/runner/claude"
	"github.com/blackcode-ch/tmx-go/internal/runner/host/hosttest"
)

// A session host that runs a terminal is refused by the chat runner (runner.ErrHostMode), left
// running and untouched, and still taken over by the terminal runner afterwards: the case of an
// agent switched to its harness's own screen before the daemon restarted. With a real host; no
// harness is started (the program in the host is this test binary's line echo).
func TestChatRunnerRefusesATerminalHost(t *testing.T) {
	if runner.Elevated() {
		t.Skip("protocol integration requires an unelevated runner")
	}
	t.Setenv("TMXG_FAKE_LINE", "1")
	dir := filepath.Join(t.TempDir(), "host")
	spec := runner.Spec{Harness: model.HarnessShell, Command: os.Args[0], Cwd: t.TempDir(),
		HostDir: dir, HostExe: hosttest.Executable(t), HostToken: "agent-cred-1"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	p1, err := New().Start(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	var out1 strings.Builder
	waitOutput(t, p1, &out1, "ready")
	pid := p1.PID()
	if !p1.(runner.Detacher).Detach() {
		t.Fatal("not detachable")
	}
	for range p1.Events() {
	}

	// The next daemon believes the agent is in chat mode.
	chatSpec := spec
	chatSpec.Env = []string{claude.EnvConfigDir + "=" + t.TempDir()}
	if p, _, err := claude.New().(runner.Reattacher).Reattach(ctx, chatSpec); !errors.Is(err, runner.ErrHostMode) {
		if p != nil {
			_ = p.Stop(ctx)
		}
		t.Fatalf("the chat runner took over a terminal host: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "host.json")); err != nil {
		t.Fatalf("the refused host is gone: %v", err)
	}

	// The terminal runner still takes it over: the same program, its screen and its input.
	p2, cred, err := New().(runner.Reattacher).Reattach(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if cred != "agent-cred-1" || p2.PID() != pid {
		t.Fatalf("credential %q, pid %d (was %d)", cred, p2.PID(), pid)
	}
	var out2 strings.Builder
	waitOutput(t, p2, &out2, "ready")
	if err := p2.Write([]byte("hello\r"), true); err != nil {
		t.Fatal(err)
	}
	waitOutput(t, p2, &out2, "got:hello")
	sctx, scancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer scancel()
	if err := p2.Stop(sctx); err != nil {
		t.Fatal(err)
	}
	for range p2.Events() {
	}
}
