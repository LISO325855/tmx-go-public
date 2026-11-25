package config

import (
	"slices"
	"time"
)

// PhoneLockConfig is config.json "phone_lock" (internal/api/phonelock.go). The zero value is
// the default: on, locking five minutes after the phone's page went to the background.
type PhoneLockConfig struct {
	Off bool `json:"off,omitempty"`
	// AfterSeconds: 0 (at once), 30, 60, 300 or 900; nil = 300.
	AfterSeconds *int `json:"after_seconds,omitempty"`
}

// PhoneLockAfters are the "Lock after" choices, in seconds.
var PhoneLockAfters = []int{0, 30, 60, 300, 900}

// PhoneLockDefaultAfter is "Lock after" when it was never chosen.
const PhoneLockDefaultAfter = 5 * time.Minute

// Resolved is whether the phone lock is on and after how long in the background it locks (p
// may be nil; an unknown length counts as the default).
func (p *PhoneLockConfig) Resolved() (on bool, after time.Duration) {
	if p == nil {
		return true, PhoneLockDefaultAfter
	}
	after = PhoneLockDefaultAfter
	if p.AfterSeconds != nil && slices.Contains(PhoneLockAfters, *p.AfterSeconds) {
		after = time.Duration(*p.AfterSeconds) * time.Second
	}
	return !p.Off, after
}
