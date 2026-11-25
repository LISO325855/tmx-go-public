package companion

import (
	"context"
	"time"
)

type sendRecoveryPolicy struct {
	window, interval time.Duration
	firstAttempt     time.Time
	messageID        string
	eligible         func(Task) bool
}

// recoveryMatch never treats a status update timestamp as task creation time.
// Older Companion versions expose neither creation time nor the original message
// ID; their ambiguous sends must be checked by hand rather than guessed by text.
func recoveryMatch(rt Task, text, messageID string, firstAttempt time.Time) bool {
	if rt.ID == "" {
		return false
	}
	// Validate both wire formats when present: conflicting old creation evidence
	// must not be hidden by a newer field or by an echoed message identity.
	creation := rt.CreateTime
	if rt.Metadata.CreateTime != "" {
		creation = rt.Metadata.CreateTime
	}
	for _, raw := range []string{rt.CreateTime, rt.Metadata.CreateTime} {
		if at, parseErr := time.Parse(time.RFC3339Nano, raw); parseErr == nil && at.Before(firstAttempt.Add(-5*time.Second)) {
			return false
		}
	}
	_, err := time.Parse(time.RFC3339Nano, creation)
	for _, m := range rt.History {
		if m.Role == "ROLE_USER" && messageID != "" && m.MessageID == messageID {
			return true
		}
	}
	return err == nil && rt.FirstUserText() == text
}

// Prefer original message identity over text when both are present.
func findRecoveryTask(recent []Task, text string, p sendRecoveryPolicy) (Task, bool) {
	for _, identityOnly := range []bool{true, false} {
		for _, rt := range recent {
			candidate := rt
			if identityOnly {
				candidate.CreateTime = ""
				candidate.Metadata.CreateTime = ""
			}
			if recoveryMatch(candidate, text, p.messageID, p.firstAttempt) && recoveryMatch(rt, text, p.messageID, p.firstAttempt) && (p.eligible == nil || p.eligible(rt)) {
				return rt, true
			}
		}
	}
	return Task{}, false
}

// sendRecover posts exactly once. Absence from Recent is never proof that a
// request was not accepted. Busy is returned to the queue with its backoff.
func (c *Client) sendRecover(ctx context.Context, text string, p sendRecoveryPolicy, report func(int, string) bool) (Task, error) {
	if p.firstAttempt.IsZero() {
		p.firstAttempt = time.Now()
	}
	if p.messageID == "" {
		p.messageID = newMessageID()
	}
	if !report(1, "sending") {
		return Task{}, context.Canceled
	}
	sendCtx, cancel := context.WithTimeout(ctx, 135*time.Second)
	task, err := c.sendMessage(sendCtx, text, "", "", p.messageID)
	cancel()
	if err == nil {
		report(0, "accepted by Companion")
		return task, nil
	}
	e := AsError(err)
	if e == nil || !e.Unconfirmed {
		return Task{}, err
	}
	if !report(0, "send unconfirmed; checking Companion tasks without resending") {
		return Task{}, context.Canceled
	}
	until := time.Now().Add(p.window)
	for {
		queryCtx, stop := context.WithTimeout(ctx, 10*time.Second)
		recent, lookupErr := c.Recent(queryCtx, 100)
		stop()
		if lookupErr == nil {
			if rt, ok := findRecoveryTask(recent, text, p); ok {
				report(0, "found matching Companion task; following without resending")
				return rt, nil
			}
		}
		if ctx.Err() != nil {
			return Task{}, ctx.Err()
		}
		if !time.Now().Before(until) {
			return Task{}, err
		}
		if !waitSend(ctx, min(p.interval, time.Until(until))) {
			return Task{}, ctx.Err()
		}
	}
}

func waitSend(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
