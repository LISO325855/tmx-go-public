//go:build !windows

package config

import "errors"

func selectedOSHome() (string, error)      { return OSDefaultHome() }
func RegisteredLogonHome() (string, error) { return "", errors.New("startup is available on Windows") }
func InstallLogon(string, string) error    { return errors.New("startup is available on Windows") }
func RemoveLogon() error                   { return errors.New("startup is available on Windows") }
