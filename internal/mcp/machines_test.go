package mcp

import (
	"fmt"
	"strings"
	"testing"
)

// The fleet tool's second part: every other machine of the owner with its agents as
// name@machine, running ones first; this machine's own rows are not repeated.
func TestFormatMachines(t *testing.T) {
	r := Roster{
		Hosts: []RosterHost{
			{Name: "hosta-go", Online: true, Local: true},
			{Name: "hostc", Online: true, LastOnline: 1794940000},
			{Name: "hostb", LastOnline: 1794940000, Error: "context deadline exceeded"},
			{Name: "later", Error: "not paired with later (tmx peers import-python)"},
		},
		Sessions: []RosterSession{
			{Name: "head", Host: "hosta-go", State: "idle", Tier: "head"},
			{Name: "zeta", Host: "hostc", State: "exited", Tier: "worker"},
			{Name: "tmx-lead", Host: "Hostc", State: "working", Tier: "lead", Project: "tmx", Role: "leads tmx", Queued: 2},
			{Name: "alpha", Host: "hostc", State: "dialog"},
			{Name: "old", Host: "hostb", State: "stopped", Tier: "worker"},
		},
	}
	got := FormatMachines(r)
	want := "\nThe owner's other machines. Address an agent there as name@machine (send reaches it); " +
		"the launch, stop, screen and type tools work on this machine only.\n" +
		"machine hostc: online, 3 agents, 2 not stopped\n" +
		"  alpha@hostc  -  dialog\n" +
		"  tmx-lead@hostc  lead  working  project tmx  2 queued  (leads tmx)\n" +
		"  zeta@hostc  worker  exited\n" +
		"machine hostb: NOT REACHABLE since 2026-11-17 18:26 UTC; its agents as last seen:\n" +
		"  old@hostb  worker  stopped\n" +
		"machine later: NOT REACHABLE, never seen by this machine (not paired with later (tmx peers import-python))\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, "head@") || strings.Contains(got, "hosta-go") {
		t.Fatalf("this machine's own agents are repeated: %s", got)
	}
}

// A daemon with no other machine adds nothing to the tree; a machine with hundreds of agents
// is cut after the running ones, and says how many it left out.
func TestFormatMachinesAloneAndLong(t *testing.T) {
	if got := FormatMachines(Roster{Hosts: []RosterHost{{Name: "solo", Online: true, Local: true}},
		Sessions: []RosterSession{{Name: "head", Host: "solo", State: "idle"}}}); got != "" {
		t.Fatalf("a single machine: %q", got)
	}
	r := Roster{Hosts: []RosterHost{{Name: "hostc", Online: true}}}
	for i := range 200 {
		r.Sessions = append(r.Sessions, RosterSession{Name: fmt.Sprintf("old-%03d", i), Host: "hostc", State: "exited"})
	}
	r.Sessions = append(r.Sessions, RosterSession{Name: "zz-live", Host: "hostc", State: "working"})
	got := FormatMachines(r)
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if !strings.Contains(lines[1], "201 agents, 1 not stopped") || !strings.HasPrefix(lines[2], "  zz-live@hostc") {
		t.Fatalf("the running agent is not first:\n%s", strings.Join(lines[:4], "\n"))
	}
	if last := lines[len(lines)-1]; len(lines) != 2+machineAgentLines+1 || !strings.Contains(last, "and 141 more") {
		t.Fatalf("%d lines, last %q", len(lines), last)
	}
}
