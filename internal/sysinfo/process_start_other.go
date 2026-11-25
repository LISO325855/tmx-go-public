//go:build !windows && !linux

package sysinfo

func ProcessStart(pid int) int64 {
	rows, _ := Procs()
	p, _ := find(rows, pid)
	return p.Start
}
