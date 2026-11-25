//go:build !windows

package api

import "context"

// allowPhonePort: on macOS and Linux tmx does not manage the firewall (macOS asks the owner
// itself the first time the daemon listens on the network).
func allowPhonePort(context.Context, int) (string, error) { return "", nil }
