//go:build windows

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const logonKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const logonValue = "TiamatrixGo"

// RegisteredLogonHome returns the home recorded by Tiamatrix Go's own per-user
// logon entry. A malformed entry needs repair instead of opening fresh data.
func RegisteredLogonHome() (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, logonKey, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer k.Close()
	value, _, err := k.GetStringValue(logonValue)
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return logonHomeFromCommand(value)
}

func logonHomeFromCommand(command string) (string, error) {
	args, err := windows.DecomposeCommandLine(command)
	if err != nil || len(args) != 6 || args[1] != "--headless" ||
		args[3] != "daemon" || args[4] != "--home" || !filepath.IsAbs(args[5]) {
		return "", errors.New("invalid Tiamatrix Go logon entry; run tmx startup install with the correct TMXG_HOME")
	}
	return filepath.Clean(args[5]), nil
}

func windowsLogonCommand(conhost, exe, home string) (string, error) {
	if !filepath.IsAbs(conhost) || !filepath.IsAbs(exe) || !filepath.IsAbs(home) {
		return "", errors.New("logon launcher requires absolute executable and home paths")
	}
	// Run launches this directly from the user's interactive logon session.
	// Headless conhost keeps either PE flavour out of the foreground.
	command := windows.ComposeCommandLine([]string{conhost, "--headless", exe, "daemon", "--home", home})
	if len(windows.StringToUTF16(command)) > 260 {
		return "", errors.New("Tiamatrix Go logon command exceeds the Windows Run entry limit")
	}
	return command, nil
}

// InstallLogon registers this installed executable and resolved data home.
func InstallLogon(exe, home string) error {
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return err
	}
	command, err := windowsLogonCommand(filepath.Join(system, "conhost.exe"), exe, home)
	if err != nil {
		return err
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, logonKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(logonValue, command)
}

func RemoveLogon() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, logonKey, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer k.Close()
	err = k.DeleteValue(logonValue)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	return err
}

func selectedOSHome() (string, error) {
	registered, err := RegisteredLogonHome()
	if err != nil {
		return "", err
	}
	return windowsNodeHome(os.Getenv("LOCALAPPDATA"), registered)
}

func windowsNodeHome(local, registered string) (string, error) {
	if registered != "" {
		if st, err := os.Stat(registered); err != nil || !st.IsDir() {
			return "", fmt.Errorf("registered Tiamatrix Go home %q is unavailable; set TMXG_HOME to repair its registration", registered)
		}
		return registered, nil
	}
	if local == "" {
		return "", errors.New("LOCALAPPDATA is not set")
	}
	installed := filepath.Join(local, "TiamatrixGo")
	st, err := os.Stat(installed)
	switch {
	case err == nil && st.IsDir():
		return installed, nil
	case err == nil:
		return "", fmt.Errorf("installed Tiamatrix Go home %q is not a directory", installed)
	case !errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("inspect installed Tiamatrix Go home: %w", err)
	default:
		return filepath.Join(local, "tmxg-trial"), nil
	}
}
