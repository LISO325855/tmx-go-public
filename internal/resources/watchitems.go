package resources

import (
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// What the fleet's watch (internal/fleet/watch.go) reads from resource control. The fleet
// finds these methods on the data of the bus events resources.guard and resources.sampled, so
// neither package imports the other.

// WatchItems turns one guard event into readings for the watch board: the memory guard's
// level and each volume's reserve level are states (urgent from "refuse" and under the
// reserve); a writer the reserve could not or would not pause is a one-off note. The pauses
// themselves are reported by the fleet's brake, with the agent's Needs-you item.
func (e GuardEvent) WatchItems() []model.WatchItem {
	switch {
	case e.Kind == "memguard-pause" || e.Kind == "disk-reserve-brake":
		return nil
	case strings.HasPrefix(e.Kind, "memguard-"):
		lvl := Level(strings.TrimPrefix(e.Kind, "memguard-"))
		it := model.WatchItem{Key: "memory", Status: string(lvl), Text: e.Detail}
		switch {
		case lvl == LevelOK:
			it.Status = model.WatchOK
		case levelRank[lvl] >= levelRank[LevelRefuse]:
			it.Urgent = true
			it.Text = "memory is critical (" + e.Detail + "): no new agents start"
			if levelRank[lvl] >= levelRank[LevelStop] {
				it.Text += " and agents are paused one by one"
			}
		}
		return []model.WatchItem{it}
	case e.Kind == "disk-reserve-would-brake" || e.Kind == "disk-reserve-exempt-writer":
		return []model.WatchItem{{Note: true, Text: "disk: " + e.Detail}}
	case strings.HasPrefix(e.Kind, "disk-reserve-"):
		lvl := strings.TrimPrefix(e.Kind, "disk-reserve-")
		it := model.WatchItem{Key: "disk:" + e.Volume, Status: lvl, Text: "disk: " + e.Detail}
		switch lvl {
		case VolOK:
			it.Status = model.WatchOK
		case VolLow:
			it.Urgent = true
			it.Text = "disk is below its free-space reserve: " + e.Detail
		}
		return []model.WatchItem{it}
	}
	return nil
}

// WatchSample is the part of a sample the watch reads: the day's writing per agent against
// its budget, and the machine's totals for the daily digest.
func (s Sample) WatchSample() model.WatchSample {
	w := model.WatchSample{WrittenToday: s.Machine.WrittenToday, MemFree: s.Machine.MemFree, MemTotal: s.Machine.MemTotal,
		DiskFree: s.Machine.DiskFree, DiskTotal: s.Machine.DiskTotal, DiskPath: s.Machine.DiskPath}
	for _, a := range s.Agents {
		w.Agents = append(w.Agents, model.WatchAgentUse{AgentID: a.AgentID, Written: a.WrittenToday, Budget: a.Budget})
	}
	return w
}
