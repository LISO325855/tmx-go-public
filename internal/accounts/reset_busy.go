package accounts

import (
	"context"
	"errors"
	"sync"
	"time"
)

const resetClaimTimeout = 90 * time.Second

// ResetInFlight keeps automatic updates from interrupting a reset between its
// durable intent and verified result. A stuck claim or verification cannot hold
// an update beyond the claim's deadline. Each concurrent account has its own lease.
func (s *Service) ResetInFlight(now time.Time) bool {
	s.resetMu.Lock()
	defer s.resetMu.Unlock()
	return s.resetInFlightLocked(now)
}

func (s *Service) resetInFlightLocked(now time.Time) bool {
	busy := false
	for ctx, deadline := range s.resetActive {
		if now.Before(deadline) {
			busy = true
		} else {
			delete(s.resetActive, ctx)
		}
	}
	return busy
}

func (s *Service) beginReset(parent context.Context) (context.Context, func(), error) {
	s.resetMu.Lock()
	defer s.resetMu.Unlock()
	if s.resetRestart {
		return nil, nil, errors.New("daemon restart reserved; no reset requested")
	}
	ctx, cancel := context.WithTimeout(parent, resetClaimTimeout)
	deadline, _ := ctx.Deadline()
	if s.resetActive == nil {
		s.resetActive = make(map[context.Context]time.Time)
	}
	s.resetActive[ctx] = deadline
	return ctx, func() {
		cancel()
		s.resetMu.Lock()
		delete(s.resetActive, ctx)
		s.resetMu.Unlock()
	}, nil
}

// ReserveRestart atomically excludes new reset claims. Unlike the advisory Busy
// snapshot, this reservation remains held through checkpoint, swap and handover.
// Release it only if the restart is abandoned; successful handover keeps it held.
func (s *Service) ReserveRestart() (func(), error) {
	s.resetMu.Lock()
	defer s.resetMu.Unlock()
	if s.resetRestart {
		return nil, errors.New("daemon restart already reserved")
	}
	if s.resetInFlightLocked(time.Now()) {
		return nil, errors.New("account reset in progress; restart refused")
	}
	s.resetRestart = true
	var once sync.Once
	return func() {
		once.Do(func() {
			s.resetMu.Lock()
			s.resetRestart = false
			s.resetMu.Unlock()
		})
	}, nil
}
