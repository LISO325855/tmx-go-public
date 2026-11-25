package config

// WatchConfig is config.json "watch": what the fleet's watch tells the head (DESIGN.md "Watch").
// Zero fields take the defaults; nil = everything at its default (on).
type WatchConfig struct {
	Enabled *bool `json:"enabled,omitempty"` // nil = on
	// DigestMinutes: at most one digest to the head in this many minutes (30). Urgent items
	// (disk under its reserve, memory critical, a crash loop, several agents dead at once) are
	// told at once, once per episode.
	DigestMinutes int `json:"digest_minutes,omitempty"`
	// DailyHour: the local hour of the daily digest (8); -1 = no daily digest.
	DailyHour *int `json:"daily_hour,omitempty"`
	// StuckMailMinutes: mail waiting this long for an idle agent is reported (5).
	StuckMailMinutes int `json:"stuck_mail_minutes,omitempty"`
	// StoppedMailMinutes: mail waiting this long for a stopped agent is reported (15).
	StoppedMailMinutes int `json:"stopped_mail_minutes,omitempty"`
	// PeerOfflineMinutes: another machine of the fleet unreachable this long is reported (5).
	PeerOfflineMinutes int `json:"peer_offline_minutes,omitempty"`
	// MachineWriteGB: all agents together writing more than this in a day is reported, and 80 %
	// of it as a warning (100); -1 = off. Alert only: the per-agent budget does the pausing.
	MachineWriteGB float64 `json:"machine_write_gb,omitempty"`
	// PromptState: include cached fleet state in the watch digest; nil = on.
	// Owner messages never carry monitor context.
	PromptState *bool `json:"prompt_state,omitempty"`
}

// Watch is the effective watch configuration.
type Watch struct {
	Enabled            bool
	DigestMinutes      int
	DailyHour          int // -1 = off
	StuckMailMinutes   int
	StoppedMailMinutes int
	PeerOfflineMinutes int
	MachineWriteGB     float64 // <= 0 = off
	PromptState        bool
}

// Effective fills the defaults (a nil receiver is the default configuration).
func (c *WatchConfig) Effective() Watch {
	w := Watch{Enabled: true, DigestMinutes: 30, DailyHour: 8, StuckMailMinutes: 5, StoppedMailMinutes: 15,
		PeerOfflineMinutes: 5, MachineWriteGB: 100, PromptState: true}
	if c == nil {
		return w
	}
	if c.Enabled != nil {
		w.Enabled = *c.Enabled
	}
	if c.DigestMinutes > 0 {
		w.DigestMinutes = c.DigestMinutes
	}
	if c.DailyHour != nil {
		if h := *c.DailyHour; h >= 0 && h <= 23 {
			w.DailyHour = h
		} else {
			w.DailyHour = -1
		}
	}
	if c.StuckMailMinutes > 0 {
		w.StuckMailMinutes = c.StuckMailMinutes
	}
	if c.StoppedMailMinutes > 0 {
		w.StoppedMailMinutes = c.StoppedMailMinutes
	}
	if c.PeerOfflineMinutes > 0 {
		w.PeerOfflineMinutes = c.PeerOfflineMinutes
	}
	if c.MachineWriteGB != 0 {
		w.MachineWriteGB = c.MachineWriteGB
	}
	if c.PromptState != nil {
		w.PromptState = *c.PromptState
	}
	return w
}
