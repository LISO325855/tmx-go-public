package codexguard

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
)

func TestMicrosoftSystemVerification(t *testing.T) {
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyMicrosoftSystemFile(filepath.Join(dir, "cmd.exe")); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(t.TempDir(), "cmd.exe")
	if err := os.WriteFile(fixture, []byte("unsigned fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if verifyMicrosoftSystemFile(fixture) == nil {
		t.Fatal("accepted non-system executable")
	}
}

// A module exporting the verifier command on an inherited search path must
// never execute, even when its exported function claims a valid signature.
func TestMicrosoftVerificationIgnoresInheritedModules(t *testing.T) {
	root := t.TempDir()
	module := filepath.Join(root, "Microsoft.PowerShell.Security")
	if err := os.MkdirAll(module, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "substitute-loaded")
	t.Setenv("TMX_TEST_MODULE_MARKER", marker)
	t.Setenv("PSModulePath", root)
	t.Setenv("TMX_VERIFY_SECURITY_MODULE", filepath.Join(module, "Microsoft.PowerShell.Security.psd1"))
	manifest := `@{ RootModule='Microsoft.PowerShell.Security.psm1'; ModuleVersion='1.0.0'; FunctionsToExport=@('Get-AuthenticodeSignature') }`
	body := `[System.IO.File]::WriteAllText($env:TMX_TEST_MODULE_MARKER,'loaded'); function Get-AuthenticodeSignature { [pscustomobject]@{Status='Valid';SignerCertificate=[pscustomobject]@{Subject='O=Microsoft Corporation'}} }; Export-ModuleMember -Function Get-AuthenticodeSignature`
	for name, content := range map[string]string{"Microsoft.PowerShell.Security.psd1": manifest, "Microsoft.PowerShell.Security.psm1": body} {
		if err := os.WriteFile(filepath.Join(module, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyMicrosoftSystemFile(filepath.Join(system, "cmd.exe")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("substitute verifier module executed", err)
	}
}
