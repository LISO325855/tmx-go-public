package sysinfo

import (
	"os"
	"testing"
)

func BenchmarkSlotProcessIdentity(b *testing.B) {
	pid := os.Getpid()
	rows, err := Procs()
	if err != nil {
		b.Skip(err)
	}
	want, _ := find(rows, pid)
	if want.Start <= 0 {
		b.Skip("process start identity unavailable")
	}
	processes := float64(len(rows))
	b.Run("Snapshot", func(b *testing.B) {
		b.ReportMetric(processes, "host-procs")
		for b.Loop() {
			rows, _ := Procs()
			p, _ := find(rows, pid)
			if p.Start != want.Start {
				b.Fatal("snapshot process identity changed")
			}
		}
	})
	b.Run("ByPID", func(b *testing.B) {
		b.ReportMetric(processes, "host-procs")
		for b.Loop() {
			if ProcessStart(pid) != want.Start {
				b.Fatal("single-process identity differs from snapshot")
			}
		}
	})
}
