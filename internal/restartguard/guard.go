// Package restartguard coordinates planned daemon handovers with audio capture.
package restartguard

import (
	"errors"
	"sync"
	"time"
)

var ErrCapturing = errors.New("audio capture is in progress; finish your sentence or recording before restarting (force overrides this)")
var ErrRestarting = errors.New("the computer is restarting; try again shortly")

// LeaseTTL covers background-tab timer throttling and the two-minute dictation limit.
const LeaseTTL = 3 * time.Minute

type Guard struct {
	mu         sync.Mutex
	leases     map[string]time.Time
	restarting bool
}

// Capture renews or releases a window's capture lease. A committed handover refuses new audio.
func (g *Guard) Capture(id string, active bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !active {
		delete(g.leases, id)
		return nil
	}
	if g.restarting {
		return ErrRestarting
	}
	if g.leases == nil {
		g.leases = make(map[string]time.Time)
	}
	g.leases[id] = time.Now().Add(LeaseTTL)
	return nil
}

// Admit orders creation of a daemon-owned recording against Reserve. busy must reflect it
// before fn returns; this closes the gap between checking recorder state and starting audio.
func (g *Guard) Admit(fn func()) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.restarting {
		return ErrRestarting
	}
	fn()
	return nil
}

// Reserve commits a planned restart atomically with capture admission. Call release if the
// handover fails; after success keep it reserved until the process exits.
func (g *Guard) Reserve(force bool, busy func() bool) (release func(), err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.restarting {
		return nil, ErrRestarting
	}
	now := time.Now()
	for id, until := range g.leases {
		if !until.After(now) {
			delete(g.leases, id)
		}
	}
	if !force && (len(g.leases) > 0 || (busy != nil && busy())) {
		return nil, ErrCapturing
	}
	g.restarting = true
	return func() { g.mu.Lock(); g.restarting = false; g.mu.Unlock() }, nil
}
