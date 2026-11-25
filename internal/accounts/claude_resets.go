package accounts

import (
	"encoding/json"
	"time"
)

// FreeResets is a read-only snapshot of Claude's free reset grants. Unknown is
// deliberately distinct from a successfully read response with no offers.
type FreeResets struct {
	accountID string      // dedicated read's identity, never sent to the UI
	Known     bool        `json:"known"`
	CheckedAt int64       `json:"checked_at"`
	Note      string      `json:"note,omitempty"`
	Grants    []FreeReset `json:"grants"`
}

type FreeReset struct {
	Type      string `json:"type"`
	Remaining int    `json:"remaining"`
	ExpiresAt int64  `json:"expires_at"`
	UsableNow bool   `json:"usable_now"`
}

func unknownResets(note string) *FreeResets {
	return &FreeResets{Note: note, Grants: []FreeReset{}}
}

// Only a completed dedicated offer read can establish that no reset is offered.
// The ordinary usage cache does not record which API query produced it.
func parseClaudeResetOffers(b []byte, now time.Time) *FreeResets {
	var usage map[string]json.RawMessage
	if json.Unmarshal(b, &usage) != nil || len(usage) == 0 {
		return unknownResets("Unknown: could not read reset offers.")
	}
	// Reject in-band error bodies, even with HTTP 200.
	if _, ok := usage["error"]; ok {
		return unknownResets("Unknown: could not read reset offers.")
	}
	valid := false
	for _, key := range []string{"cedar_ember", "limits", "five_hour", "seven_day"} {
		if _, ok := usage[key]; ok {
			valid = true
		}
	}
	if !valid {
		return unknownResets("Unknown: could not read reset offers.")
	}
	out := &FreeResets{Known: true, CheckedAt: now.UnixMilli(), Grants: []FreeReset{}}
	raw := usage["cedar_ember"]
	if len(raw) == 0 || string(raw) == "null" {
		return out
	}
	var status struct {
		Eligible *bool  `json:"eligible"`
		Reason   string `json:"ineligible_reason"`
		Grants   []struct {
			Type      string   `json:"type"`
			Left      *int     `json:"resets_left"`
			Ends      string   `json:"ends_at"`
			Starts    string   `json:"starts_at"`
			Paused    bool     `json:"paused"`
			Usable    bool     `json:"usable_now"`
			GivesBack bool     `json:"gives_back_weekly"`
			Clears    []string `json:"clears"`
		} `json:"grants"`
	}
	if json.Unmarshal(raw, &status) != nil || status.Eligible == nil {
		return unknownResets("Could not read reset offers.")
	}
	if status.Reason == "unavailable" {
		return unknownResets("Unknown: reset offers are temporarily unavailable.")
	}
	if !*status.Eligible {
		return out
	}
	for _, g := range status.Grants {
		kind := g.Type
		if g.GivesBack {
			kind = "fable"
		}
		// Match Claude's legacy grant classification, never merge Fable into weekly.
		if kind == "" {
			session, weekly := false, false
			for _, c := range g.Clears {
				if c == "five_hour" {
					session = true
				} else {
					weekly = true
				}
			}
			if weekly && !session {
				kind = "weekly"
			}
		}
		if kind != "fable" && kind != "weekly" {
			continue
		}
		if g.Left == nil || *g.Left < 0 {
			return unknownResets("Could not read reset offers.")
		}
		var expires int64
		if g.Ends != "" {
			t, err := time.Parse(time.RFC3339Nano, g.Ends)
			if err != nil {
				return unknownResets("Could not read reset expiry.")
			}
			expires = t.UnixMilli()
			if !t.After(now) {
				continue
			}
		}
		usable := g.Usable && !g.Paused
		if g.Starts != "" {
			t, err := time.Parse(time.RFC3339Nano, g.Starts)
			if err != nil {
				return unknownResets("Could not read reset availability.")
			}
			usable = usable && !t.After(now)
		}
		out.Grants = append(out.Grants, FreeReset{Type: kind, Remaining: *g.Left, ExpiresAt: expires, UsableNow: usable})
	}
	return out
}
