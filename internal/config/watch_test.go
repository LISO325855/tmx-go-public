package config

import (
	"encoding/json"
	"testing"
)

func TestWatchDefaultsAndOverrides(t *testing.T) {
	want := Watch{Enabled: true, DigestMinutes: 30, DailyHour: 8, StuckMailMinutes: 5, StoppedMailMinutes: 15,
		PeerOfflineMinutes: 5, MachineWriteGB: 100, PromptState: true}
	if got := (*WatchConfig)(nil).Effective(); got != want {
		t.Fatalf("nil: %+v", got)
	}
	if got := (&WatchConfig{}).Effective(); got != want {
		t.Fatalf("empty: %+v", got)
	}
	var c Config
	if err := json.Unmarshal([]byte(`{"watch":{"enabled":false,"digest_minutes":60,"daily_hour":-1,"stuck_mail_minutes":10,
		"stopped_mail_minutes":30,"peer_offline_minutes":2,"machine_write_gb":-1,"prompt_state":false}}`), &c); err != nil {
		t.Fatal(err)
	}
	got := c.Watch.Effective()
	if got != (Watch{DigestMinutes: 60, DailyHour: -1, StuckMailMinutes: 10, StoppedMailMinutes: 30, PeerOfflineMinutes: 2, MachineWriteGB: -1}) {
		t.Fatalf("overrides: %+v", got)
	}
	h := 25 // not an hour: no daily digest rather than a guess
	if got := (&WatchConfig{DailyHour: &h, DigestMinutes: -5}).Effective(); got.DailyHour != -1 || got.DigestMinutes != 30 {
		t.Fatalf("bad values: %+v", got)
	}
	zero := 0 // midnight is an hour
	if got := (&WatchConfig{DailyHour: &zero}).Effective(); got.DailyHour != 0 {
		t.Fatalf("hour 0: %+v", got)
	}
}
