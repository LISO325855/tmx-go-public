package resources

import (
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

func TestGuardEventWatchItems(t *testing.T) {
	one := func(kind, vol, detail string) model.WatchItem {
		t.Helper()
		items := GuardEvent{Kind: kind, Volume: vol, Detail: detail}.WatchItems()
		if len(items) != 1 {
			t.Fatalf("%s: %d items", kind, len(items))
		}
		return items[0]
	}
	for _, tc := range []struct {
		kind, vol            string
		key, status          string
		urgent, note, hasKey bool
	}{
		{"memguard-ok", "", "memory", model.WatchOK, false, false, true},
		{"memguard-warn", "", "memory", "warn", false, false, true},
		{"memguard-refuse", "", "memory", "refuse", true, false, true},
		{"memguard-stop", "", "memory", "stop", true, false, true},
		{"memguard-orphan", "", "memory", "orphan", true, false, true},
		{"disk-reserve-ok", "C:", "disk:C:", model.WatchOK, false, false, true},
		{"disk-reserve-warn", "C:", "disk:C:", "warn", false, false, true},
		{"disk-reserve-low", "D:", "disk:D:", "low", true, false, true},
		{"disk-reserve-would-brake", "C:", "", "", false, true, false},
		{"disk-reserve-exempt-writer", "C:", "", "", false, true, false},
	} {
		it := one(tc.kind, tc.vol, "the detail")
		if it.Key != tc.key || it.Status != tc.status || it.Urgent != tc.urgent || it.Note != tc.note || it.Text == "" {
			t.Fatalf("%s: %+v", tc.kind, it)
		}
	}
	// The pauses are the brake's to report (with the Needs-you item), not twice.
	for _, kind := range []string{"memguard-pause", "disk-reserve-brake", "something-else"} {
		if items := (GuardEvent{Kind: kind, Agent: "w1", Detail: "x"}).WatchItems(); len(items) != 0 {
			t.Fatalf("%s: %+v", kind, items)
		}
	}
	// Every kind the guards emit is one of the above (a new kind must be mapped on purpose).
	if got := one("memguard-stop", "", "memory level stop: 1.0 GB free commit").Text; got != "memory is critical (memory level stop: 1.0 GB free commit): no new agents start and agents are paused one by one" {
		t.Fatal(got)
	}
}

func TestSampleWatchSample(t *testing.T) {
	s := Sample{Machine: MachineUsage{WrittenToday: 7, MemFree: 3, MemTotal: 8, DiskFree: 5, DiskTotal: 9, DiskPath: "D:"},
		Agents: []AgentUsage{{AgentID: "a", WrittenToday: 4, Budget: 10}, {AgentID: "b", WrittenToday: 3}}}
	w := s.WatchSample()
	if w.WrittenToday != 7 || w.MemFree != 3 || w.MemTotal != 8 || w.DiskFree != 5 || w.DiskTotal != 9 || w.DiskPath != "D:" ||
		len(w.Agents) != 2 || w.Agents[0] != (model.WatchAgentUse{AgentID: "a", Written: 4, Budget: 10}) || w.Agents[1].Budget != 0 {
		t.Fatalf("%+v", w)
	}
}
