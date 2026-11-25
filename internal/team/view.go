package team

import (
	"slices"
	"strings"
	"time"
)

// Build states on the Team page.
const (
	BuildCurrent    = "current"     // based on the newest release any computer in the group runs
	BuildOld        = "old"         // older than that, at or above the floor
	BuildBelowFloor = "below_floor" // below Floor
	BuildUnknown    = "unknown"     // not a release (a dev build) or no report
	BuildFleet      = "fleet"       // a fleet build (Node.BasedOn == FleetLine): its own release line
)

// FleetLine is what a fleet build reports as Node.BasedOn: it is not based on an upstream
// release number, so it is neither compared with the floor nor with the others' releases.
const FleetLine = "fleet"

// Last seen.
const (
	SeenNow     = "now"     // a report in the last 45 minutes
	SeenToday   = "today"   // earlier today (local time)
	SeenOffline = "offline" // before today: offline since then
	SeenNever   = "never"   // no report from this computer
)

// Member is a current member of the group, as this install knows it.
type Member struct {
	PeerID string
	Name   string // the name this install shows for them (a person may run several computers)
	Me     bool   // this computer
	Admin  bool
}

// Stored is the latest report of one computer, with what the checker needs.
type Stored struct {
	Report   Report
	SignedBy string
	SigOK    bool
	At       int64 // signed at (Unix ms)
	Changes  []Change
}

// NodeRow is one computer on the Team page.
type NodeRow struct {
	PeerID     string   `json:"peer_id"`
	Me         bool     `json:"me,omitempty"`
	Admin      bool     `json:"admin,omitempty"`
	HasReport  bool     `json:"has_report"`
	Label      string   `json:"label"`
	BuildState string   `json:"build_state"` // current | old | below_floor | unknown
	Seen       string   `json:"seen"`        // now | today | offline | never
	ReportedAt int64    `json:"reported_at,omitempty"`
	Links      LinkSum  `json:"links"`
	Linked     bool     `json:"linked"` // reaches every member who is online now
	Channels   int      `json:"channels"`
	Report     *Report  `json:"report,omitempty"`
	Checks     []Check  `json:"checks"`
	Passed     int      `json:"passed"`
	Checked    int      `json:"checked"`
	Changes    []Change `json:"changes,omitempty"` // the latest state changes (admin only)
}

// LinkSum counts a computer's links to the other members.
type LinkSum struct {
	Direct  int `json:"direct"`
	Relayed int `json:"relayed"`
	Offline int `json:"offline"`
	Members int `json:"members"` // other current members
}

// Person is one block on the Team page: a name, its computers.
type Person struct {
	Name     string    `json:"name"`
	Me       bool      `json:"me,omitempty"`
	Admin    bool      `json:"admin,omitempty"`
	Nodes    []NodeRow `json:"nodes"`
	Passing  bool      `json:"passing"`  // every computer reports and passes what is checked; all three channels
	Linked   bool      `json:"linked"`   // every computer seen now reaches everyone online now
	Channels bool      `json:"channels"` // phone, e-mail and WhatsApp connected (on any of their computers)
}

// Summary is the four numbers above the rows.
type Summary struct {
	People   int      `json:"people"`
	Passing  int      `json:"passing"`
	Builds   []string `json:"builds"` // builds in use (computers seen in the last 30 days)
	Linked   int      `json:"linked"`
	Channels int      `json:"channels"`
	Newest   string   `json:"newest,omitempty"` // the newest upstream release in the group
}

// BuildPeople makes the Team page's blocks for one group from its members and the reports.
func BuildPeople(members []Member, reports map[string]Stored, now time.Time) ([]Person, Summary) {
	nowMs := now.UnixMilli()
	online := map[string]bool{}
	var newest Build
	var newestRaw string
	for _, m := range members {
		st, ok := reports[m.PeerID]
		if !ok {
			continue
		}
		if time.Duration(nowMs-st.At)*time.Millisecond < Fresh {
			online[m.PeerID] = true
		}
		if b, ok := ParseBuild(st.Report.Node.BasedOn); ok && (newestRaw == "" || newest.Less(b)) {
			newest, newestRaw = b, st.Report.Node.BasedOn
		}
	}
	floor, _ := ParseBuild(Floor)
	var people []*Person
	byName := map[string]*Person{}
	builds := map[string]bool{}
	for _, m := range members {
		key := strings.ToLower(strings.TrimSpace(m.Name))
		p := byName[key]
		if p == nil {
			p = &Person{Name: m.Name}
			byName[key] = p
			people = append(people, p)
		}
		p.Me = p.Me || m.Me
		p.Admin = p.Admin || m.Admin
		row := NodeRow{PeerID: m.PeerID, Me: m.Me, Admin: m.Admin, BuildState: BuildUnknown, Seen: SeenNever}
		st, ok := reports[m.PeerID]
		if ok {
			r := st.Report
			row.HasReport, row.Report, row.ReportedAt, row.Label = true, &r, st.At, r.Node.Label
			row.Checks = Checks(Input{Report: r, HasReport: true, SignedBy: st.SignedBy, SigOK: st.SigOK, Member: m.PeerID, At: st.At, Now: nowMs})
			row.Seen = seen(st.At, now)
			if r.Node.BasedOn == FleetLine {
				row.BuildState = BuildFleet
			} else if b, ok := ParseBuild(r.Node.BasedOn); ok {
				switch {
				case b.Less(floor):
					row.BuildState = BuildBelowFloor
				case b.Less(newest):
					row.BuildState = BuildOld
				default:
					row.BuildState = BuildCurrent
				}
			}
			if r.Node.Build != "" {
				builds[r.Node.Build] = true
			}
			states := linkMap(r.Links)
			row.Linked = true
			for _, o := range members {
				if o.PeerID == m.PeerID {
					continue
				}
				row.Links.Members++
				switch states[o.PeerID] {
				case LinkDirect:
					row.Links.Direct++
				case LinkRelayed:
					row.Links.Relayed++
				default:
					row.Links.Offline++
					if online[o.PeerID] {
						row.Linked = false
					}
				}
			}
			for _, s := range []string{r.Phone.State, r.Email.State, r.WhatsApp.State} {
				if s == Connected {
					row.Channels++
				}
			}
			row.Changes = st.Changes
		} else {
			row.Checks = Checks(Input{Member: m.PeerID, Now: nowMs})
		}
		row.Passed, row.Checked = Passed(row.Checks)
		p.Nodes = append(p.Nodes, row)
	}
	sum := Summary{People: len(people), Newest: newestRaw, Builds: []string{}}
	for b := range builds {
		sum.Builds = append(sum.Builds, b)
	}
	slices.Sort(sum.Builds)
	out := make([]Person, 0, len(people))
	for _, p := range people {
		phone, email, wa := false, false, false
		p.Passing, p.Linked = true, false
		seenNow := 0
		allLinked := true
		for _, n := range p.Nodes {
			if !n.HasReport {
				p.Passing = false
				continue
			}
			for _, c := range n.Checks {
				if (c.ID == "C2" || c.ID == "C3" || c.ID == "C6") && c.Result != Pass {
					p.Passing = false
				}
			}
			phone = phone || n.Report.Phone.State == Connected
			email = email || n.Report.Email.State == Connected
			wa = wa || n.Report.WhatsApp.State == Connected
			if n.Seen == SeenNow {
				seenNow++
				allLinked = allLinked && n.Linked
			}
		}
		p.Channels = phone && email && wa
		p.Passing = p.Passing && p.Channels
		p.Linked = seenNow > 0 && allLinked
		if p.Passing {
			sum.Passing++
		}
		if p.Linked {
			sum.Linked++
		}
		if p.Channels {
			sum.Channels++
		}
		out = append(out, *p)
	}
	// You first, then the admin, then by name.
	slices.SortStableFunc(out, func(a, b Person) int {
		rank := func(p Person) int {
			switch {
			case p.Me:
				return 0
			case p.Admin:
				return 1
			}
			return 2
		}
		if ra, rb := rank(a), rank(b); ra != rb {
			return ra - rb
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out, sum
}

func seen(at int64, now time.Time) string {
	t := time.UnixMilli(at)
	switch {
	case now.Sub(t) < Fresh:
		return SeenNow
	case sameDay(t, now):
		return SeenToday
	}
	return SeenOffline
}

func sameDay(a, b time.Time) bool {
	a, b = a.Local(), b.Local()
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}
