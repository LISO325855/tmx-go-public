package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/hiddenexec"
)

func checkCodexSandbox() error {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		// Codex can use its bundled Bubblewrap if there is no system binary.
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := hiddenexec.CommandContext(ctx, bwrap, "--unshare-user", "--ro-bind", "/", "/", "/bin/true").CombinedOutput()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("Codex sandbox cannot start: Bubblewrap user namespace check timed out")
	}
	detail := strings.TrimSpace(string(output))
	if strings.Contains(detail, "Permission denied") || strings.Contains(detail, "Operation not permitted") {
		if policy, _ := os.ReadFile("/proc/sys/kernel/apparmor_restrict_unprivileged_userns"); strings.TrimSpace(string(policy)) == "1" {
			return fmt.Errorf("Codex sandbox cannot start: user namespaces blocked by AppArmor (%s)", detail)
		}
	}
	if detail == "" {
		detail = err.Error()
	}
	return fmt.Errorf("Codex sandbox cannot start: %s", detail)
}
