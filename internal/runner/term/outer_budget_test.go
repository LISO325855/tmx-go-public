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
	"github.com/blackcode-ch/tmx-go/internal/runner/host"
	"github.com/blackcode-ch/tmx-go/internal/runner/host/hosttest"
)

func TestLegacyRepaintClassificationWithinHandoverBudget(t *testing.T) {
	t.Setenv("TMXG_FAKE_LINE", "1")
	spec := runner.Spec{Harness: model.HarnessShell, Command: os.Args[0], Cwd: t.TempDir(), HostDir: filepath.Join(t.TempDir(), "host"), HostExe: hosttest.Executable(t)}
	p, err := New().Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Stop(spec.HostDir)
	var out strings.Builder
	waitOutput(t, p, &out, "ready")
	p.(runner.Detacher).Detach()
	for range p.Events() {
	}
	host.ClearState(spec.HostDir)
	spec.Harness = model.HarnessClaude
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _, err = New().(runner.Reattacher).Reattach(ctx, spec)
	if !errors.Is(err, runner.ErrHostUnknown) || !errors.Is(err, runner.ErrHostRepaint) {
		t.Fatalf("unexpected classification: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatalf("classification arrived after outer deadline: %v", ctx.Err())
	}
}
