package reshistory

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/resources"
)

// clock is a settable time for the store.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// base is a round quarter hour, so buckets of every tier start exactly there.
var base = time.Date(2026, 11, 17, 12, 0, 0, 0, time.Local)

func sample(at time.Time, cpu, write, queue float64) resources.Sample {
	return resources.Sample{At: at.UnixMilli(), Interval: 5, Machine: resources.MachineUsage{
		CPU: cpu, Cores: 16, MemTotal: 64 << 30, MemFree: 20 << 30, DiskWriteRate: write, DiskQueue: queue,
		AgentWriteRate: 1 << 20, WrittenToday: 3 << 30}}
}

func newStore(t *testing.T) (*Store, *clock, config.Paths) {
	t.Helper()
	c := &clock{t: base}
	p := config.Paths{Home: t.TempDir()}
	return New(p, c.now, nil), c, p
}

// Samples five seconds apart become 10-second, one-minute and quarter-hour averages; each
// period reads its own tier.
func TestAveragesPerTier(t *testing.T) {
	s, c, _ := newStore(t)
	for i := range 24 { // two minutes: CPU 10, 30, 10, 30 … ; writes 2 MB/s with one 10 MB/s spike
		w := 2.0 * (1 << 20)
		if i == 3 {
			w = 10 * (1 << 20)
		}
		s.Add(sample(base.Add(time.Duration(i)*5*time.Second), float64(10+20*(i%2)), w, 0.5))
	}
	c.t = base.Add(2 * time.Minute)

	v, ok := s.View("1h")
	if !ok || v.Step != 10 || len(v.Points) != 12 || v.From != c.t.Add(-time.Hour).UnixMilli() || v.To != c.t.UnixMilli() {
		t.Fatalf("1h: ok %v step %d points %d", ok, v.Step, len(v.Points))
	}
	p := v.Points[1] // samples 2 and 3: CPU 10 and 30, writes 2 and 10 MB/s
	if p.T != base.Add(10*time.Second).UnixMilli() || p.CPU != 20 || p.MemFree != 20<<30 || p.Write == nil || *p.Write != 6*(1<<20) ||
		p.WriteMax == nil || *p.WriteMax != 10*(1<<20) || p.Queue == nil || *p.Queue != 0.5 || p.AgentRate != 1<<20 {
		t.Fatalf("10-second point: %+v", p)
	}
	for period, want := range map[string]struct{ step, points int }{"6h": {60, 2}, "24h": {60, 2}, "7d": {900, 1}} {
		v, ok := s.View(period)
		if !ok || v.Step != want.step || len(v.Points) != want.points {
			t.Fatalf("%s: ok %v step %d points %d", period, ok, v.Step, len(v.Points))
		}
		if v.Points[0].CPU != 20 {
			t.Fatalf("%s: CPU average %v", period, v.Points[0].CPU)
		}
	}
	if v.MemTotal != 64<<30 || v.Cores != 16 || v.AgentsWritten != 3<<30 || v.Day != "2026-11-17" {
		t.Fatalf("header: %+v", v)
	}
	if _, ok := s.View("1y"); ok || ValidPeriod("1y") || !ValidPeriod("7d") {
		t.Fatal("an unknown period was accepted")
	}
}

// A value this build cannot measure (-1) is absent from the point, not zero; and it does not
// drag down the average of the samples that did measure it.
func TestUnmeasuredIsAbsent(t *testing.T) {
	s, c, _ := newStore(t)
	s.Add(sample(base, 10, -1, -1))
	s.Add(sample(base.Add(5*time.Second), 10, 4<<20, -1))
	s.Add(sample(base.Add(10*time.Second), 10, -1, -1))
	c.t = base.Add(time.Minute)
	v, _ := s.View("1h")
	if len(v.Points) != 2 {
		t.Fatalf("points: %d", len(v.Points))
	}
	if p := v.Points[0]; p.Write == nil || *p.Write != 4<<20 || p.Queue != nil {
		t.Fatalf("half-measured bucket: %+v", p)
	}
	if p := v.Points[1]; p.Write != nil || p.WriteMax != nil || p.Queue != nil {
		t.Fatalf("unmeasured bucket: %+v", p)
	}
}

// The same sample twice counts once; buckets older than a tier's window are dropped; a gap
// stays a gap.
func TestDuplicatesWindowAndGaps(t *testing.T) {
	s, c, _ := newStore(t)
	first := sample(base, 50, 1<<20, 0)
	s.Add(first)
	s.Add(first)
	s.Add(resources.Sample{}) // the sampler has not measured yet
	s.Add(sample(base.Add(-time.Minute), 99, 0, 0))
	c.t = base.Add(time.Second)
	if v, _ := s.View("1h"); len(v.Points) != 1 || v.Points[0].CPU != 50 {
		t.Fatalf("after duplicates: %+v", v.Points)
	}
	late := base.Add(90 * time.Minute)
	s.Add(sample(late, 70, 1<<20, 0))
	c.t = late.Add(time.Second)
	v, _ := s.View("1h")
	if len(v.Points) != 1 || v.Points[0].T != late.UnixMilli() {
		t.Fatalf("the hour kept a bucket from 90 minutes ago: %+v", v.Points)
	}
	if v, _ := s.View("24h"); len(v.Points) != 2 || v.Points[1].T-v.Points[0].T != (90*time.Minute).Milliseconds() {
		t.Fatalf("the day: a gap must stay a gap: %+v", v.Points)
	}
}

// Today's bytes by every program: the rate times the time each sample stands for, never more
// than half a minute per sample (a sleep is not written through), and from zero at midnight.
func TestWrittenToday(t *testing.T) {
	s, c, _ := newStore(t)
	s.Add(sample(base, 0, 10<<20, 0))
	s.Add(sample(base.Add(5*time.Second), 0, 10<<20, 0)) // 5 s at 10 MB/s
	s.Add(sample(base.Add(10*time.Second), 0, -1, 0))    // not measured: nothing added
	s.Add(sample(base.Add(4*time.Hour), 0, 10<<20, 0))   // after a sleep: 30 s at most
	c.t = base.Add(4 * time.Hour)
	v, _ := s.View("1h")
	if want := uint64(35 * 10 << 20); v.DiskWritten != want || !v.DiskMeasured {
		t.Fatalf("written today %d, want %d (measured %v)", v.DiskWritten, want, v.DiskMeasured)
	}
	next := time.Date(2026, 11, 18, 0, 0, 5, 0, time.Local)
	s.Add(sample(next, 0, -1, 0))
	c.t = next
	if v, _ := s.View("1h"); v.DiskWritten != 0 || v.DiskMeasured || v.Day != "2026-11-18" {
		t.Fatalf("a new day starts at zero and unmeasured: %+v", v)
	}
}

// The minute and quarter-hour tiers and today's bytes survive a restart; the last hour is then
// drawn from the minute tier until 10-second readings exist again.
func TestSurvivesARestart(t *testing.T) {
	s, c, p := newStore(t)
	for i := range 36 { // three minutes
		s.Add(sample(base.Add(time.Duration(i)*5*time.Second), 40, 2<<20, 0))
	}
	c.t = base.Add(3 * time.Minute)
	s.Save()
	st, err := os.Stat(filepath.Join(p.Home, "resources-history.json"))
	if err != nil || st.Size() == 0 {
		t.Fatalf("no file: %v", err)
	}
	s.Save() // nothing changed: nothing to write, and no error

	c.t = base.Add(5 * time.Minute)
	s2 := New(p, c.now, nil)
	v, _ := s2.View("1h")
	if len(v.Points) != 3 || v.Points[0].CPU != 40 || v.DiskWritten == 0 || !v.DiskMeasured {
		t.Fatalf("after the restart: %d points, written %d", len(v.Points), v.DiskWritten)
	}
	if v, _ := s2.View("7d"); len(v.Points) != 1 {
		t.Fatalf("quarter-hour tier after the restart: %d", len(v.Points))
	}
	// New 10-second readings join the minute points before them, without overlap.
	s2.Add(sample(c.t, 80, 2<<20, 0))
	c.t = c.t.Add(time.Second)
	v, _ = s2.View("1h")
	if len(v.Points) != 4 || v.Points[3].CPU != 80 {
		t.Fatalf("minute points then the new reading: %+v", v.Points)
	}
	for i := 1; i < len(v.Points); i++ {
		if v.Points[i].T <= v.Points[i-1].T {
			t.Fatalf("points out of order: %+v", v.Points)
		}
	}
}

// A file that is damaged, foreign, from the future or far too large costs the history, never
// the start and never a wrong graph.
func TestBadFilesStartEmpty(t *testing.T) {
	for name, content := range map[string]string{
		"not json":      "{",
		"other version": `{"version":99,"minute":[{"t":1,"n":1}]}`,
		"bad buckets": `{"version":1,"day":"2026-11-17","written":-5,"minute":[` +
			`{"t":1794913200000,"n":0,"c":10},` + // no samples
			`{"t":1794913200001,"n":1,"c":10},` + // not on a minute
			`{"t":9999999999000,"n":1,"c":10},` + // the future
			`{"t":60000,"n":1,"c":10},` + // long ago
			`{"t":1794913200000,"n":1,"c":-4}]}`, // a negative sum
	} {
		c := &clock{t: base}
		p := config.Paths{Home: t.TempDir()}
		if err := os.WriteFile(filepath.Join(p.Home, "resources-history.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		s := New(p, c.now, nil)
		for _, period := range []string{"1h", "24h", "7d"} {
			if v, _ := s.View(period); len(v.Points) != 0 || v.DiskWritten != 0 {
				t.Fatalf("%s, %s: %+v", name, period, v)
			}
		}
	}
	c := &clock{t: base}
	p := config.Paths{Home: t.TempDir()}
	if err := os.WriteFile(filepath.Join(p.Home, "resources-history.json"), make([]byte, maxFile+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if v, _ := New(p, c.now, nil).View("24h"); len(v.Points) != 0 {
		t.Fatal("an oversized file was read")
	}
}
