package team

import (
	"sync"
	"time"
)

// Meter counts how much this computer's agents worked today, for Report.Activity: it is told
// once a minute how many agents are working right now and keeps two sums for the local day.
// It knows numbers only - never which agent, project or task. A restart starts the day's count
// again from what was saved with Restore; nothing is estimated for the time the daemon was down.
type Meter struct {
	mu     sync.Mutex
	day    string
	active int       // minutes with at least one agent working
	agents int       // agent-minutes
	last   time.Time // the last minute counted
}

// Tick records that `working` agents are working at now. It counts at most one minute per
// call and at most once per minute, so a caller that ticks more often, or late, never inflates
// the day; a new local day starts from zero.
func (m *Meter) Tick(now time.Time, working int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if day := now.Format(time.DateOnly); day != m.day {
		m.day, m.active, m.agents = day, 0, 0
	}
	if !m.last.IsZero() && now.Sub(m.last) < 55*time.Second && !now.Before(m.last) {
		return
	}
	m.last = now
	if working <= 0 {
		return
	}
	if m.active < 1440 {
		m.active++
	}
	m.agents += min(working, maxAgents)
}

// Activity is today's count as of now (zero counts on a day nothing was recorded yet).
func (m *Meter) Activity(now time.Time) Activity {
	m.mu.Lock()
	defer m.mu.Unlock()
	day := now.Format(time.DateOnly)
	if day != m.day {
		return Activity{Day: day}
	}
	return Activity{Day: day, ActiveMinutes: m.active, AgentMinutes: m.agents}
}

// Restore continues a day's count after a restart, if a is that same local day and well-formed.
func (m *Meter) Restore(now time.Time, a Activity) {
	if a.Day != now.Format(time.DateOnly) || a.ActiveMinutes < 0 || a.ActiveMinutes > 1440 || a.AgentMinutes < a.ActiveMinutes || a.AgentMinutes > 1440*maxAgents {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.day, m.active, m.agents = a.Day, a.ActiveMinutes, a.AgentMinutes
}
