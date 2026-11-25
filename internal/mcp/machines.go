package mcp

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Roster is GET /api/fleet as far as the fleet tool reads it: this machine and the owner's
// other machines, each with the agents it last listed (api/fleet_roster.go; Python's shape).
type Roster struct {
	Hosts    []RosterHost    `json:"hosts"`
	Sessions []RosterSession `json:"sessions"`
}

// RosterHost is one machine of the roster. LastOnline is Unix seconds of its last answer.
type RosterHost struct {
	Name       string  `json:"name"`
	Online     bool    `json:"online"`
	Local      bool    `json:"local"`
	Error      string  `json:"error"`
	LastOnline float64 `json:"last_online"`
}

// RosterSession is one agent of the roster.
type RosterSession struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	State   string `json:"state"`
	Project string `json:"project"`
	Role    string `json:"role"`
	Tier    string `json:"tier"`
	Queued  int    `json:"queued_messages"`
}

// machineAgentLines bounds how many agents of one other machine the fleet tool lists: a machine
// with hundreds of stopped agents must not bury the answer. Running ones come first.
const machineAgentLines = 60

// FormatMachines renders the owner's other machines and their agents for the fleet tool, below
// this machine's own tree. Empty when this daemon has no other machine. Every string comes from
// another machine: the caller marks the whole answer as untrusted data.
func FormatMachines(r Roster) string {
	byHost := map[string][]RosterSession{}
	for _, s := range r.Sessions {
		byHost[strings.ToLower(s.Host)] = append(byHost[strings.ToLower(s.Host)], s)
	}
	var b strings.Builder
	for _, h := range r.Hosts {
		if h.Local {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("\nThe owner's other machines. Address an agent there as name@machine (send reaches it); " +
				"the launch, stop, screen and type tools work on this machine only.\n")
		}
		agents := byHost[strings.ToLower(h.Name)]
		live := 0
		for _, a := range agents {
			if !restingState(a.State) {
				live++
			}
		}
		switch {
		case h.Online:
			fmt.Fprintf(&b, "machine %s: online, %d %s, %d not stopped\n", h.Name, len(agents), plural(len(agents), "agent", "agents"), live)
		case h.LastOnline > 0:
			fmt.Fprintf(&b, "machine %s: NOT REACHABLE since %s; its agents as last seen:\n", h.Name,
				time.Unix(int64(h.LastOnline), 0).UTC().Format("2006-01-02 15:04 UTC"))
		default:
			fmt.Fprintf(&b, "machine %s: NOT REACHABLE, never seen by this machine", h.Name)
			if h.Error != "" {
				fmt.Fprintf(&b, " (%s)", h.Error)
			}
			b.WriteString("\n")
		}
		// Running first, then by name: stable, and the cut below drops stopped agents first.
		sort.SliceStable(agents, func(i, j int) bool {
			if ri, rj := restingState(agents[i].State), restingState(agents[j].State); ri != rj {
				return rj
			}
			return agents[i].Name < agents[j].Name
		})
		for i, a := range agents {
			if i == machineAgentLines {
				fmt.Fprintf(&b, "  ... and %d more not shown\n", len(agents)-i)
				break
			}
			fmt.Fprintf(&b, "  %s@%s  %s  %s", a.Name, h.Name, orDash(a.Tier), orDash(a.State))
			if a.Project != "" {
				fmt.Fprintf(&b, "  project %s", a.Project)
			}
			if a.Queued > 0 {
				fmt.Fprintf(&b, "  %d queued", a.Queued)
			}
			if a.Role != "" {
				fmt.Fprintf(&b, "  (%s)", a.Role)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// restingState reports whether a roster state means the agent is not running (Go peers answer
// "exited" for stopped and error; Python peers also "stopped").
func restingState(state string) bool {
	return state == "exited" || state == "stopped"
}
