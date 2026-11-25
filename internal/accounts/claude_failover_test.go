package accounts

import (
	"testing"
	"time"
)

func TestClaudeFailoverSelection(t *testing.T) {
	root := t.TempDir()
	p, err := OpenProfiles(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first", "second"} {
		if err := p.Add(Claude, name); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	s := New(Options{Dir: root, Profiles: p, Now: func() time.Time { return now }, Every: -1})
	defer s.Close()
	set := func(name string, connected bool, weekly, opus float64) {
		e := s.entry(string(Claude), name)
		e.acct.Connected, e.acct.CheckedAt = connected, now.UnixMilli()
		end := now.Add(time.Hour).UnixMilli()
		e.acct.Limits = []Limit{{ID: "week", UsedPercent: weekly, ResetsAt: end}, {ID: "week_opus", UsedPercent: opus, ResetsAt: end}}
	}
	set("default", true, 100, 0)
	set("first", true, 20, 100)
	set("second", true, 30, 25)
	if got := s.ClaudeFailoverAccount("", "claude-sonnet-5"); got != "first" {
		t.Fatalf("weekly exhaustion selected %q", got)
	}
	set("default", true, 10, 100)
	if got := s.ClaudeFailoverAccount("default", "claude-opus-5-5"); got != "second" {
		t.Fatalf("model exhaustion selected %q", got)
	}
	if got := s.ClaudeFailoverAccount("default", "claude-sonnet-5"); got != "" {
		t.Fatalf("Opus quota affected Sonnet: %q", got)
	}
	set("second", false, 30, 25)
	if got := s.ClaudeFailoverAccount("default", "claude-opus-5-5"); got != "" {
		t.Fatalf("selected an ineligible alternate: %q", got)
	}
	now = now.Add(2 * time.Hour)
	if got := s.ClaudeFailoverAccount("default", "claude-opus-5-5"); got != "" {
		t.Fatalf("expired window triggered a switch: %q", got)
	}
}

func TestClaudeFailoverDeduplicatesRateLimitEvents(t *testing.T) {
	s := New(Options{Dir: t.TempDir(), Every: -1})
	defer s.Close()
	e := s.entry(string(Claude), "")
	e.acct.Connected, e.acct.CheckedAt = true, s.now()
	end := time.Now().Add(time.Hour).Unix()
	if !s.RateLimited(Claude, "", "seven_day_fable", end) {
		t.Fatal("first model-specific rejection did not change usage")
	}
	if s.RateLimited(Claude, "", "seven_day_fable", end) {
		t.Fatal("duplicate rejection changed usage again")
	}
	if _, full := claudeLimit(e.acct, "week_fable", s.now()); !full {
		t.Fatal("Fable rejection did not mark the model's weekly limit full")
	}
}
