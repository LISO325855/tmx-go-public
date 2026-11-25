//go:build windows

package config

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestProductionAccountWithRAMDiskTemp(t *testing.T) {
	const ramTemp = `R:\Temp`
	if _, err := ResolveAccountPath(ramTemp); !errors.Is(err, windows.ERROR_INVALID_FUNCTION) {
		t.Skip("R: is not a RAM disk with the Forge final-path limitation")
	}
	profile, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEMP", ramTemp)
	t.Setenv("TMP", ramTemp)
	c := Config{Profile: ProfileProduction}
	if !c.UseUserAccounts(profile) {
		t.Fatal("production account on the system volume was classified as temporary")
	}
	if c.UseUserAccounts(ramTemp) {
		t.Fatal("unresolved RAM disk home was allowed to use user accounts")
	}
}
