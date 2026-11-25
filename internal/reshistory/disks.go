package reshistory

import (
	"sort"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/sysinfo"
)

// Per-disk history: each physical disk's write rate, write operations per second and the share
// of time it was busy, in the same tiers as the machine's. The counters are the ones resource
// control already reads (sysinfo.Disks: one query per disk, no process started); this file only
// takes their differences.

// maxDisks bounds how many disks are kept: removable drives come and go.
const maxDisks = 16

// diskBucket is the running sums of one step for one disk.
type diskBucket struct {
	T  int64   `json:"t"`
	N  int     `json:"n"`
	W  float64 `json:"w,omitempty"` // sum of the write rate, bytes/s
	WX float64 `json:"x,omitempty"` // its highest sample
	O  float64 `json:"o,omitempty"` // sum of write operations per second
	B  float64 `json:"b,omitempty"` // sum of the busy share, percent
	ON int     `json:"k,omitempty"` // samples that had operations and busy time
}

// DiskPoint is one bucket of one disk. Ops and Busy are absent where the system does not
// report them.
type DiskPoint struct {
	T        int64    `json:"t"`
	Write    float64  `json:"write"`               // bytes/s
	WriteMax float64  `json:"write_max"`           // the highest sample in the bucket
	Ops      *float64 `json:"write_ops,omitempty"` // write operations per second
	Busy     *float64 `json:"busy,omitempty"`      // percent of the time the disk had work
}

// DiskSeries is one disk's points for the asked period.
type DiskSeries struct {
	Name   string      `json:"name"`
	Points []DiskPoint `json:"points"`
}

type diskState struct {
	prev   sysinfo.Disk
	prevAt int64
	tiers  [3][]diskBucket
}

// AddDisks records one reading of the disks' cumulative counters taken at `at` (Unix ms). The
// first reading of a disk only sets its baseline; a counter that went backwards (a 32-bit wrap,
// a replaced drive) or a reading after a long pause starts a new baseline instead of a spike.
func (s *Store) AddDisks(at int64, disks []sysinfo.Disk) {
	if at <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disks == nil {
		s.disks = map[string]*diskState{}
	}
	for _, d := range disks {
		st, ok := s.disks[d.Name]
		if !ok {
			if len(s.disks) >= maxDisks || d.Name == "" {
				continue
			}
			st = &diskState{}
			s.disks[d.Name] = st
		}
		p, pAt := st.prev, st.prevAt
		st.prev, st.prevAt = d, at
		dt := time.Duration(at-pAt) * time.Millisecond
		if pAt == 0 || dt <= 0 || dt > maxGap || d.Written < p.Written {
			continue
		}
		secs := dt.Seconds()
		rate := float64(d.Written-p.Written) / secs
		ops, busy, hasOps := 0.0, 0.0, d.HasOps && p.HasOps && d.Writes >= p.Writes && d.Busy >= p.Busy
		if hasOps {
			ops = float64(d.Writes-p.Writes) / secs
			busy = min((d.Busy-p.Busy).Seconds()/secs, 1) * 100
		}
		for i, t := range tiers {
			start := at - at%t.step.Milliseconds()
			bs := st.tiers[i]
			if n := len(bs); n == 0 || bs[n-1].T != start {
				bs = append(bs, diskBucket{T: start})
			}
			b := &bs[len(bs)-1]
			b.N++
			b.W += rate
			b.WX = max(b.WX, rate)
			if hasOps {
				b.O += ops
				b.B += busy
				b.ON++
			}
			oldest, drop := at-t.keep.Milliseconds(), 0
			for drop < len(bs) && bs[drop].T < oldest {
				drop++
			}
			st.tiers[i] = bs[drop:]
		}
		s.dirty = true
	}
}

func (b diskBucket) point() DiskPoint {
	p := DiskPoint{T: b.T, Write: b.W / float64(b.N), WriteMax: b.WX}
	if b.ON > 0 {
		ops, busy := b.O/float64(b.ON), b.B/float64(b.ON)
		p.Ops, p.Busy = &ops, &busy
	}
	return p
}

// diskSeriesLocked answers every disk that has a point in the window, by name. As for the
// machine, the last hour is filled from the minute tier before the first 10-second reading.
func (s *Store) diskSeriesLocked(tier int, from, nowMs int64) []DiskSeries {
	out := []DiskSeries{}
	for name, st := range s.disks {
		var pts []DiskPoint
		src := st.tiers[tier]
		if tier == 0 && len(st.tiers[1]) > 0 {
			first := nowMs
			if len(src) > 0 {
				first = src[0].T
			}
			for _, b := range st.tiers[1] {
				if b.T >= from && b.T+tiers[1].step.Milliseconds() <= first {
					pts = append(pts, b.point())
				}
			}
		}
		for _, b := range src {
			if b.T >= from {
				pts = append(pts, b.point())
			}
		}
		if len(pts) > 0 {
			out = append(out, DiskSeries{Name: name, Points: pts})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// usableDisk is usable() for a disk's saved tier.
func usableDisk(in []diskBucket, t tier, nowMs int64) []diskBucket {
	out := make([]diskBucket, 0, len(in))
	oldest, step := nowMs-t.keep.Milliseconds(), t.step.Milliseconds()
	var prev int64
	for _, b := range in {
		if b.N <= 0 || b.ON < 0 || b.ON > b.N || b.T%step != 0 || b.T < oldest || b.T > nowMs || b.T <= prev ||
			b.W < 0 || b.WX < 0 || b.O < 0 || b.B < 0 || b.B > float64(b.N)*100 {
			continue
		}
		prev = b.T
		out = append(out, b)
	}
	return out
}
