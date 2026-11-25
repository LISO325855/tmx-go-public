//go:build !windows

package codexguard

import "fmt"

func verifyMicrosoftSystemFile(string) error {
	return fmt.Errorf("Microsoft system refresh requires Windows")
}
