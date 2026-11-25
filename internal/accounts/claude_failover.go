package accounts

import (
	"strings"
	"time"
)

// ClaudeFailoverAccount returns the next connected account with a current
// weekly reading and room for model when the selected account is exhausted.
// An empty result leaves the caller's existing selection in place.
func (s *Service) ClaudeFailoverAccount(selected, model string) string {
	if selected == "" {
		selected = DefaultName
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	maxAge := max(30*time.Minute, 2*s.o.Every).Milliseconds()
	modelLimit := claudeModelLimit(model)
	var exhausted, modelExhausted bool
	for _, e := range s.entries {
		if e.def.h == Claude && e.name == selected {
			_, weeklyFull := claudeLimit(e.acct, "week", now)
			_, modelExhausted = claudeLimit(e.acct, modelLimit, now)
			exhausted = weeklyFull || modelExhausted
			break
		}
	}
	if !exhausted {
		return ""
	}
	for _, e := range s.entries {
		if e.def.h != Claude || e.name == selected || !e.acct.Connected || e.acct.Error != "" || e.acct.LimitsNote != "" ||
			e.acct.CheckedAt <= 0 || now-e.acct.CheckedAt > maxAge || e.login.State == LoginRunning {
			continue
		}
		weeklyKnown, weeklyFull := claudeLimit(e.acct, "week", now)
		modelKnown, modelFull := claudeLimit(e.acct, modelLimit, now)
		_, sessionFull := claudeLimit(e.acct, "session", now)
		if weeklyKnown && !weeklyFull && !modelFull && !sessionFull && (!modelExhausted || modelKnown) {
			return e.name
		}
	}
	return ""
}

func claudeModelLimit(model string) string {
	model = strings.ToLower(model)
	for _, family := range []string{"fable", "opus", "sonnet", "haiku"} {
		if strings.Contains(model, family) {
			return "week_" + family
		}
	}
	return ""
}

func claudeLimit(a Account, id string, now int64) (known, full bool) {
	if id == "" {
		return false, false
	}
	for _, limit := range a.Limits {
		if limit.ID == id && limit.UsedPercent >= 0 && limit.UsedPercent <= 100 &&
			(limit.ResetsAt == 0 || limit.ResetsAt > now) {
			return true, limit.UsedPercent >= 100
		}
	}
	return false, false
}
