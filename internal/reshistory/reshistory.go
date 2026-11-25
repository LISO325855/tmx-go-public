// Package reshistory keeps the machine's resource history for the Watchdog page: CPU, free
// memory, disk writes (every program and the agents) and the disk queue, as averages over 10
// seconds for the last hour, over a minute for the last day and over 15 minutes for the last
// week. It reads the samples resource control already takes (resources.Manager), measures
// nothing itself, and survives a restart: the two longer tiers are saved to one small file.
//
// A gap in the series means no reading (the daemon was down, or this build cannot measure that
// value): nothing is interpolated and a missing value is never written as zero.
package reshistory

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/resources"
	"github.com/blackcode-ch/tmx-go/internal/sysinfo"
)

// tier is one resolution: buckets of step, kept for keep.
type tier struct {
	step, keep time.Duration
	saved      bool // written to the file (the 10-second tier is not: an hour is rebuilt quickly)
}

var tiers = [3]tier{
	{step: 10 * time.Second, keep: time.Hour},
	{step: time.Minute, keep: 24 * time.Hour, saved: true},
	{step: 15 * time.Minute, keep: 7 * 24 * time.Hour, saved: true},
}

// Periods the page offers, each with the tier it reads and how far back it goes.
var periods = map[string]struct {
	tier int
	span time.Duration
}{
	"1h":  {0, time.Hour},
	"6h":  {1, 6 * time.Hour},
	"24h": {1, 24 * time.Hour},
	"7d":  {2, 7 * 24 * time.Hour},
}

// ValidPeriod reports whether p is a period View accepts.
func ValidPeriod(p string) bool { _, ok := periods[p]; return ok }

const (
	// saveEvery: the file is a few hundred kB, so half-hourly keeps its own writing to a few MB
	// a day on a machine whose writes are budgeted. A crash costs at most that much history.
	saveEvery = 30 * time.Minute
	// maxGap bounds how long one sample counts toward today's written bytes: a sample after a
	// pause (sleep, a stalled sampler) says nothing about the time in between.
	maxGap  = 30 * time.Second
	fileVer = 1
	// maxFile bounds the file read back at start: far above what the tiers can hold.
	maxFile = 8 << 20
)

// bucket is the running sums of one step. JSON keys are one letter: the file holds thousands.
type bucket struct {
	T    int64   `json:"t"`           // Unix ms: the bucket's start
	N    int     `json:"n"`           // samples in it
	CPU  float64 `json:"c"`           // sum of CPU %
	Mem  float64 `json:"m"`           // sum of free memory, bytes
	W    float64 `json:"w,omitempty"` // sum of the disk write rate (every program), bytes/s
	WMax float64 `json:"x,omitempty"` // its highest sample
	WN   int     `json:"v,omitempty"` // samples that measured it
	A    float64 `json:"a,omitempty"` // sum of the agents' write rate, bytes/s
	Q    float64 `json:"q,omitempty"` // sum of the disk queue
	QN   int     `json:"k,omitempty"` // samples that measured it
}

// Point is one bucket for the page: averages over Step seconds. A value this build could not
// measure in that bucket is absent (nil), never zero.
type Point struct {
	T         int64    `json:"t"`                    // Unix ms: the bucket's start
	CPU       float64  `json:"cpu"`                  // percent of all cores
	MemFree   uint64   `json:"mem_free"`             // bytes
	Write     *float64 `json:"write,omitempty"`      // disk writes by every program, bytes/s
	WriteMax  *float64 `json:"write_max,omitempty"`  // the highest sample in the bucket
	AgentRate float64  `json:"agent_write"`          // disk writes by the agents, bytes/s
	Queue     *float64 `json:"disk_queue,omitempty"` // requests in flight, busiest disk
}

// View is GET /api/resources/history.
type View struct {
	Period string  `json:"period"`
	Step   int     `json:"step"` // seconds per point
	From   int64   `json:"from"` // Unix ms: the start of the window
	To     int64   `json:"to"`   // Unix ms: now
	Points []Point `json:"points"`
	// Today, since local midnight. DiskWritten is every program's writes as far as this daemon
	// ran and measured them (a lower bound after a restart); AgentsWritten is the agents'.
	Day           string `json:"day"`
	DiskWritten   uint64 `json:"disk_written"`
	DiskMeasured  bool   `json:"disk_measured"`
	AgentsWritten uint64 `json:"agents_written"`
	MemTotal      uint64 `json:"mem_total"`
	Cores         int    `json:"cores"`
	// Disks: each physical disk's own series for the period (disks.go); empty where the system
	// reports no per-disk counters.
	Disks []DiskSeries `json:"disks"`
}

type file struct {
	Version int                 `json:"version"`
	Day     string              `json:"day"`
	Written float64             `json:"written"`
	Minute  []bucket            `json:"minute"`
	Quarter []bucket            `json:"quarter"`
	Disks   map[string]diskFile `json:"disks,omitempty"` // by disk name (disks.go)
}

type diskFile struct {
	Minute  []diskBucket `json:"minute"`
	Quarter []diskBucket `json:"quarter"`
}

// Store is the history. New, then go Run(ctx); Close saves.
type Store struct {
	path string
	now  func() time.Time
	log  *slog.Logger

	mu       sync.Mutex
	tiers    [3][]bucket
	lastAt   int64   // the last sample's time, Unix ms
	day      string  // local day of written
	written  float64 // bytes written by every program today, as measured
	measured bool    // the write rate was measured at least once today
	last     resources.MachineUsage
	disks    map[string]*diskState // per-disk history, by disk name (disks.go)
	dirty    bool
	lastSave time.Time
}

// New opens the history kept under p (an unreadable or foreign file starts an empty history:
// a graph is never worth refusing to start for).
func New(p config.Paths, now func() time.Time, log *slog.Logger) *Store {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := &Store{path: filepath.Join(p.Home, "resources-history.json"), now: now, log: log, lastSave: now()}
	s.load()
	return s
}

func (s *Store) load() {
	st, err := os.Stat(s.path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.log.Warn("resource history: not read", "err", err)
		}
		return
	}
	if st.Size() > maxFile {
		s.log.Warn("resource history: file too large, starting empty", "bytes", st.Size())
		return
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		s.log.Warn("resource history: not read", "err", err)
		return
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil || f.Version != fileVer {
		s.log.Warn("resource history: file not understood, starting empty", "err", err, "version", f.Version)
		return
	}
	nowMs := s.now().UnixMilli()
	s.tiers[1] = usable(f.Minute, tiers[1], nowMs)
	s.tiers[2] = usable(f.Quarter, tiers[2], nowMs)
	s.disks = map[string]*diskState{}
	for name, d := range f.Disks {
		if name == "" || len(name) > 64 || len(s.disks) >= maxDisks {
			continue
		}
		st := &diskState{}
		st.tiers[1], st.tiers[2] = usableDisk(d.Minute, tiers[1], nowMs), usableDisk(d.Quarter, tiers[2], nowMs)
		if len(st.tiers[1])+len(st.tiers[2]) > 0 {
			s.disks[name] = st
		}
	}
	if f.Day == s.now().Format(time.DateOnly) && f.Written >= 0 {
		s.day, s.written, s.measured = f.Day, f.Written, f.Written > 0
	}
}

// usable keeps the buckets of a saved tier that are well-formed, inside the tier's window and
// in rising order: a damaged or hand-edited file can cost history, never a wrong graph.
func usable(in []bucket, t tier, nowMs int64) []bucket {
	out := make([]bucket, 0, len(in))
	oldest, step := nowMs-t.keep.Milliseconds(), t.step.Milliseconds()
	var prev int64
	for _, b := range in {
		if b.N <= 0 || b.WN < 0 || b.QN < 0 || b.WN > b.N || b.QN > b.N || b.T%step != 0 || b.T < oldest || b.T > nowMs || b.T <= prev ||
			b.CPU < 0 || b.Mem < 0 || b.W < 0 || b.WMax < 0 || b.A < 0 || b.Q < 0 {
			continue
		}
		prev = b.T
		out = append(out, b)
	}
	return out
}

// Add records one sample. A sample no newer than the last one is ignored (the same reading
// seen twice).
func (s *Store) Add(sm resources.Sample) {
	if sm.At <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if sm.At <= s.lastAt {
		return
	}
	m := sm.Machine
	for i, t := range tiers {
		start := sm.At - sm.At%t.step.Milliseconds()
		bs := s.tiers[i]
		if n := len(bs); n == 0 || bs[n-1].T != start {
			bs = append(bs, bucket{T: start})
		}
		b := &bs[len(bs)-1]
		b.N++
		b.CPU += m.CPU
		b.Mem += float64(m.MemFree)
		b.A += max(m.AgentWriteRate, 0)
		if m.DiskWriteRate >= 0 { // -1 = not measured
			b.W += m.DiskWriteRate
			b.WMax = max(b.WMax, m.DiskWriteRate)
			b.WN++
		}
		if m.DiskQueue >= 0 {
			b.Q += m.DiskQueue
			b.QN++
		}
		oldest := sm.At - t.keep.Milliseconds()
		drop := 0
		for drop < len(bs) && bs[drop].T < oldest {
			drop++
		}
		s.tiers[i] = bs[drop:]
	}
	// Today's bytes by every program: the rate times the time it stands for.
	day := time.UnixMilli(sm.At).In(s.now().Location()).Format(time.DateOnly)
	if day != s.day {
		s.day, s.written, s.measured = day, 0, false
	}
	if m.DiskWriteRate >= 0 {
		s.measured = true
		if s.lastAt > 0 {
			dt := min(time.Duration(sm.At-s.lastAt)*time.Millisecond, maxGap)
			s.written += m.DiskWriteRate * dt.Seconds()
		}
	}
	s.lastAt, s.last, s.dirty = sm.At, m, true
}

// View answers one period; ok is false for a period the page does not offer.
func (s *Store) View(period string) (View, bool) {
	p, ok := periods[period]
	if !ok {
		return View{}, false
	}
	now := s.now()
	nowMs := now.UnixMilli()
	from := nowMs - p.span.Milliseconds()
	s.mu.Lock()
	defer s.mu.Unlock()
	v := View{Period: period, Step: int(tiers[p.tier].step / time.Second), From: from, To: nowMs, Points: []Point{},
		Day: now.Format(time.DateOnly), MemTotal: s.last.MemTotal, Cores: s.last.Cores, AgentsWritten: s.last.WrittenToday}
	if s.day == v.Day {
		v.DiskWritten, v.DiskMeasured = uint64(s.written), s.measured
	}
	src := s.tiers[p.tier]
	if p.tier == 0 && len(s.tiers[1]) > 0 {
		// Right after a restart the 10-second tier is short: the minute tier fills the part of
		// the hour before it, so the graph is not empty where there were readings.
		first := nowMs
		if len(src) > 0 {
			first = src[0].T
		}
		for _, b := range s.tiers[1] {
			if b.T >= from && b.T+tiers[1].step.Milliseconds() <= first {
				v.Points = append(v.Points, b.point())
			}
		}
	}
	for _, b := range src {
		if b.T >= from {
			v.Points = append(v.Points, b.point())
		}
	}
	v.Disks = s.diskSeriesLocked(p.tier, from, nowMs)
	return v, true
}

func (b bucket) point() Point {
	n := float64(b.N)
	p := Point{T: b.T, CPU: b.CPU / n, MemFree: uint64(b.Mem / n), AgentRate: b.A / n}
	if b.WN > 0 {
		avg, top := b.W/float64(b.WN), b.WMax
		p.Write, p.WriteMax = &avg, &top
	}
	if b.QN > 0 {
		q := b.Q / float64(b.QN)
		p.Queue = &q
	}
	return p
}

// Run reads the sampler's latest sample every interval until ctx ends, saving now and then and
// once more at the end.
func (s *Store) Run(ctx context.Context, latest func() resources.Sample, every time.Duration) {
	s.RunWithDisks(ctx, latest, nil, every)
}

// RunWithDisks is Run that also reads the disks' counters each interval (nil = no per-disk
// history; an error from it, e.g. a system without such counters, just leaves none).
func (s *Store) RunWithDisks(ctx context.Context, latest func() resources.Sample, disks func() ([]sysinfo.Disk, error), every time.Duration) {
	if every <= 0 {
		every = 5 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.Save()
			return
		case <-t.C:
			s.Add(latest())
			if disks != nil {
				if ds, err := disks(); err == nil {
					s.AddDisks(s.now().UnixMilli(), ds)
				}
			}
			if s.now().Sub(s.lastSaved()) >= saveEvery {
				s.Save()
			}
		}
	}
}

func (s *Store) lastSaved() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSave
}

// Save writes the two longer tiers, if anything changed since the last save.
func (s *Store) Save() {
	s.mu.Lock()
	s.lastSave = s.now()
	if !s.dirty {
		s.mu.Unlock()
		return
	}
	f := file{Version: fileVer, Day: s.day, Written: s.written,
		Minute: append([]bucket(nil), s.tiers[1]...), Quarter: append([]bucket(nil), s.tiers[2]...)}
	if len(s.disks) > 0 {
		f.Disks = map[string]diskFile{}
		for name, st := range s.disks {
			f.Disks[name] = diskFile{Minute: append([]diskBucket(nil), st.tiers[1]...), Quarter: append([]diskBucket(nil), st.tiers[2]...)}
		}
	}
	s.dirty = false
	s.mu.Unlock()
	b, err := json.Marshal(f)
	if err == nil {
		err = config.WriteFileAtomic(s.path, b, 0o600)
	}
	if err != nil {
		s.log.Warn("resource history: not saved", "err", err)
		s.mu.Lock()
		s.dirty = true
		s.mu.Unlock()
	}
}
