package reshistory

import (
	"fmt"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/sysinfo"
)

// disk is a cumulative reading: written bytes, completed writes, busy time.
func disk(name string, writtenMB, writes uint64, busy time.Duration) sysinfo.Disk {
	return sysinfo.Disk{Name: name, Written: writtenMB << 20, Writes: writes, Busy: busy, HasOps: true}
}

// Differences of the cumulative counters become each disk's write rate, write operations per
// second and busy share; the first reading is only the baseline.
func TestDiskRatesFromCounters(t *testing.T) {
	s, c, _ := newStore(t)
	at := func(sec int) int64 { return base.Add(time.Duration(sec) * time.Second).UnixMilli() }
	s.AddDisks(at(0), []sysinfo.Disk{disk("PhysicalDrive0", 100, 1000, 10*time.Second), disk("PhysicalDrive1", 0, 0, 0)})
	c.t = base.Add(time.Second)
	if v, _ := s.View("1h"); len(v.Disks) != 0 || v.Disks == nil {
		t.Fatalf("a baseline alone is no reading (and the list is never null): %+v", v.Disks)
	}
	// 5 s later: drive 0 wrote 50 MB in 500 writes and was busy 1 s; drive 1 did nothing.
	s.AddDisks(at(5), []sysinfo.Disk{disk("PhysicalDrive0", 150, 1500, 11*time.Second), disk("PhysicalDrive1", 0, 0, 0)})
	c.t = base.Add(6 * time.Second)
	v, _ := s.View("1h")
	if len(v.Disks) != 2 || v.Disks[0].Name != "PhysicalDrive0" || v.Disks[1].Name != "PhysicalDrive1" {
		t.Fatalf("disks: %+v", v.Disks)
	}
	p := v.Disks[0].Points[0]
	if p.Write != 10<<20 || p.WriteMax != 10<<20 || p.Ops == nil || *p.Ops != 100 || p.Busy == nil || *p.Busy != 20 {
		t.Fatalf("drive 0: %+v ops %v busy %v", p, p.Ops, p.Busy)
	}
	if q := v.Disks[1].Points[0]; q.Write != 0 || q.Ops == nil || *q.Ops != 0 || *q.Busy != 0 {
		t.Fatalf("an idle drive reads zero, which is a reading: %+v", q)
	}
	for _, period := range []string{"6h", "24h", "7d"} {
		if v, _ := s.View(period); len(v.Disks) != 2 || len(v.Disks[0].Points) != 1 || v.Disks[0].Points[0].Write != 10<<20 {
			t.Fatalf("%s: %+v", period, v.Disks)
		}
	}
}

// A counter that goes backwards (a 32-bit write count wrapping, a swapped drive), a reading
// after a long pause, and a system without operation counts never make a spike or a zero.
func TestDiskCountersThatCannotBeTrusted(t *testing.T) {
	s, c, _ := newStore(t)
	at := func(sec int) int64 { return base.Add(time.Duration(sec) * time.Second).UnixMilli() }
	s.AddDisks(at(0), []sysinfo.Disk{disk("d", 100, 4_000_000_000, 10*time.Second)})
	s.AddDisks(at(5), []sysinfo.Disk{disk("d", 105, 4_000_000_500, 11*time.Second)})    // 1 MB/s, 100 ops/s
	s.AddDisks(at(10), []sysinfo.Disk{disk("d", 110, 200, 12*time.Second)})             // the write count wrapped
	s.AddDisks(at(15), []sysinfo.Disk{disk("d", 3, 700, 13*time.Second)})               // bytes went backwards: a new baseline
	s.AddDisks(at(20), []sysinfo.Disk{disk("d", 8, 1200, 14*time.Second)})              // 1 MB/s again
	s.AddDisks(at(4000), []sysinfo.Disk{disk("d", 90000, 9_000_000, 3000*time.Second)}) // after a sleep: a baseline, no spike
	s.AddDisks(at(4005), []sysinfo.Disk{{Name: "d", Written: 90005 << 20}})             // this system has no operation counts
	s.AddDisks(0, []sysinfo.Disk{disk("d", 1, 1, 1)})                                   // no time: ignored
	c.t = base.Add(4006 * time.Second)
	v, _ := s.View("24h")
	if len(v.Disks) != 1 {
		t.Fatalf("disks: %+v", v.Disks)
	}
	var top float64
	for _, p := range v.Disks[0].Points {
		top = max(top, p.WriteMax)
		if p.Busy != nil && (*p.Busy < 0 || *p.Busy > 100) {
			t.Fatalf("busy outside 0-100: %v", *p.Busy)
		}
	}
	if top != 1<<20 {
		t.Fatalf("highest write rate %v, want 1 MiB/s: a wrap, a swap or a sleep made a spike", top)
	}
	first := v.Disks[0].Points[0] // the minute holding seconds 5-20: 3 rates, 2 of them with operations
	if first.Write != 1<<20 || first.Ops == nil || *first.Ops != 100 {
		t.Fatalf("first minute: %+v ops %v", first, first.Ops)
	}
	last := v.Disks[0].Points[len(v.Disks[0].Points)-1] // only the reading without operation counts
	if last.Write != 1<<20 || last.Ops != nil || last.Busy != nil {
		t.Fatalf("a reading without operation counts: %+v", last)
	}
}

// Per-disk history survives a restart with the machine's; removable drives are bounded.
func TestDisksPersistAndAreBounded(t *testing.T) {
	s, c, p := newStore(t)
	for i := range 13 { // one minute of readings, 5 s apart
		var ds []sysinfo.Disk
		for d := range maxDisks + 4 {
			ds = append(ds, disk(fmt.Sprintf("disk%02d", d), uint64(i*5), uint64(i*50), time.Duration(i)*time.Second))
		}
		s.AddDisks(base.Add(time.Duration(i)*5*time.Second).UnixMilli(), ds)
	}
	c.t = base.Add(2 * time.Minute)
	if v, _ := s.View("24h"); len(v.Disks) != maxDisks {
		t.Fatalf("%d disks kept, want %d", len(v.Disks), maxDisks)
	}
	s.Save()
	s2 := New(config.Paths{Home: p.Home}, c.now, nil)
	v, _ := s2.View("1h")
	if len(v.Disks) != maxDisks || v.Disks[0].Name != "disk00" || len(v.Disks[0].Points) == 0 || v.Disks[0].Points[0].Write != 1<<20 {
		t.Fatalf("after a restart: %d disks, %+v", len(v.Disks), v.Disks)
	}
	if pt := v.Disks[0].Points[0]; pt.Ops == nil || *pt.Ops != 10 || pt.Busy == nil || *pt.Busy != 20 {
		t.Fatalf("after a restart: ops %v busy %v", pt.Ops, pt.Busy)
	}
}
