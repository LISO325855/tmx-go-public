//go:build windows

package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/hiddenexec"
)

// phoneRuleName is the Windows Firewall rule for the same Wi-Fi fallback on a port.
func phoneRuleName(port int) string { return "tmx-phone-connect-" + strconv.Itoa(port) }

// phoneRuleArgs is the netsh command that lets the local subnet (only) reach the port.
func phoneRuleArgs(port int) string {
	return fmt.Sprintf("advfirewall firewall add rule name=%s dir=in action=allow protocol=TCP localport=%d remoteip=localsubnet profile=any", phoneRuleName(port), port)
}

func system32(exe string) string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", exe)
}

// allowPhonePort makes sure Windows Firewall lets the local subnet reach the daemon's port:
// without a rule it silently drops the phone's connection. Reading the rule needs no rights;
// adding it needs an administrator, so Windows asks once (the owner just clicked the switch).
// hint is the command to run by hand when that did not work.
func allowPhonePort(ctx context.Context, port int) (hint string, err error) {
	hint = "netsh " + phoneRuleArgs(port)
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	netsh := system32("netsh.exe")
	has := func() bool {
		return hiddenexec.CommandContext(ctx, netsh, "advfirewall", "firewall", "show", "rule", "name="+phoneRuleName(port)).Run() == nil
	}
	if has() {
		return hint, nil
	}
	ps := filepath.Join(system32("WindowsPowerShell"), "v1.0", "powershell.exe")
	script := fmt.Sprintf("Start-Process -FilePath '%s' -ArgumentList '%s' -Verb RunAs -WindowStyle Hidden -Wait", netsh, phoneRuleArgs(port))
	if out, runErr := hiddenexec.CommandContext(ctx, ps, "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script).CombinedOutput(); runErr != nil {
		return hint, fmt.Errorf("the firewall rule was not added: %w: %s", runErr, firstBytes(out, 200))
	}
	if !has() {
		return hint, errors.New("the firewall rule was not added")
	}
	return hint, nil
}

func firstBytes(b []byte, n int) string {
	if len(b) > n {
		b = b[:n]
	}
	return string(b)
}
