package codexguard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/hiddenexec"
	"golang.org/x/sys/windows"
)

// GetSystemDirectory comes from Windows, not the launch environment. Authenticode
// checks embedded and catalog signatures; require a valid Microsoft publisher.
func verifyMicrosoftSystemFile(path string) error {
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Dir(resolved), system) || !strings.EqualFold(filepath.Ext(resolved), ".exe") {
		return fmt.Errorf("not a Windows system executable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	script := `$ErrorActionPreference='Stop'; $env:PSModulePath=''; $PSModuleAutoLoadingPreference='None'; Import-Module -Name $env:TMX_VERIFY_SECURITY_MODULE -ErrorAction Stop; $s=Microsoft.PowerShell.Security\Get-AuthenticodeSignature -LiteralPath $env:TMX_VERIFY_SYSTEM_FILE; if ($s.Status -eq 'Valid' -and $s.SignerCertificate.Subject -match '(^|,\s*)O=Microsoft Corporation(,|$)') { exit 0 }; exit 1`
	cmd := hiddenexec.CommandContext(ctx, filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script)
	// Never let inherited module search paths or verifier variables select code.
	// Disable autoload in the child as well: PowerShell may initialize default
	// module paths at startup even when the inherited value is empty.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "PSModulePath") || strings.EqualFold(key, "TMX_VERIFY_SYSTEM_FILE") || strings.EqualFold(key, "TMX_VERIFY_SECURITY_MODULE") {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "PSModulePath=", "TMX_VERIFY_SYSTEM_FILE="+resolved,
		"TMX_VERIFY_SECURITY_MODULE="+filepath.Join(system, "WindowsPowerShell", "v1.0", "Modules", "Microsoft.PowerShell.Security", "Microsoft.PowerShell.Security.psd1"))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("system file lacks a valid Microsoft signature: %w", err)
	}
	return nil
}
