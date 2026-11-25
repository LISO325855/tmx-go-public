// Package team is a group's status report (DESIGN.md "Team status"): what each computer in a
// group tells the group's admin about itself (build, online, links to the other members, phone /
// e-mail / WhatsApp connected yes or no), the contract checks the admin runs on it, and the
// Team page's rows. It is pure: the chat layer carries the reports (chat.status envelopes), the
// API collects this computer's own report, the store keeps the admin's copies.
//
// Privacy: a report carries states, counts, versions and peer IDs of group members only. Never
// any content: no message text, chat or contact names, phone numbers, e-mail addresses, file
// names, agent names, prompts, project names or network addresses. Every string field is an
// enumeration, a version, a time zone, or the computer's own label.
package team

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// ReportVersion is the report format's major version ("report": 1). Another major is
	// dropped by the receiver.
	ReportVersion = 1
	// ContractVersion is the contract the checks implement ("contract": 1, "contract v1").
	ContractVersion = 1
	// Every: a computer sends its report this often, and at once when its state changes.
	Every = 15 * time.Minute
	// Fresh: a report younger than this is "now" (seen, and passing C3).
	Fresh = 45 * time.Minute
	// Keep: the admin keeps reports and state changes this long.
	Keep = 30 * 24 * time.Hour
	// Floor is the oldest upstream release a computer may be based on (C6).
	Floor = "v0.1.14"
	// MaxReportBytes bounds one report's JSON (a few hundred bytes in practice).
	MaxReportBytes = 4 << 10
)

// Update modes (node.update).
const (
	UpdatePinned  = "pinned"  // automatic updates off: stays on this build
	UpdateAuto    = "auto"    // installs new releases by itself
	UpdateOff     = "off"     // this build cannot update itself (a dev build, a package)
	UpdateWaiting = "waiting" // a new release is staged and waits for busy agents
)

// Channel and link states.
const (
	Connected = "connected"
	NotSet    = "not_set"
	Error     = "error"

	SignedIn = "signed_in"
	None     = "none"

	LinkDirect  = "direct"
	LinkRelayed = "relayed"
	LinkOffline = "offline"

	GateOnWithPIN = "on_with_pin"
	GateOn        = "on"
	GateOff       = "off"

	ViaMCP       = "mcp"
	ViaCompanion = "companion"
)

// Report is one computer's status, v1 (a few hundred bytes).
type Report struct {
	Report   int      `json:"report"`
	Contract int      `json:"contract"`
	SentAt   int64    `json:"sent_at"` // Unix ms
	Node     Node     `json:"node"`
	Hours    *Hours   `json:"hours,omitempty"` // the owner's working hours; absent = not set
	Fleet    Fleet    `json:"fleet"`
	Links    []Link   `json:"links"` // to the other current members of the group only
	Phone    Phone    `json:"phone"`
	WhatsApp WhatsApp `json:"whatsapp"`
	Email    Email    `json:"email"`
	Health   Health   `json:"health"`
	// Optional since the fleet build (absent from an older computer's report).
	Companion *Companion `json:"companion,omitempty"`
	Activity  *Activity  `json:"activity,omitempty"`
}

// Node is the computer and its build.
type Node struct {
	Label     string `json:"label"`    // the computer's name in tmx (config "machine")
	OS        string `json:"os"`       // darwin, linux, windows
	Build     string `json:"build"`    // the running version
	BasedOn   string `json:"based_on"` // the upstream release it is based on
	Personal  bool   `json:"personal"` // a desktop build (someone's own computer), not a headless server
	Update    string `json:"update"`   // pinned | auto | off | waiting
	StartedAt int64  `json:"started_at"`
}

type Hours struct {
	Days []string `json:"days,omitempty"`
	From string   `json:"from,omitempty"`
	To   string   `json:"to,omitempty"`
	TZ   string   `json:"tz,omitempty"`
}

type Fleet struct {
	AgentsRunning   int               `json:"agents_running"`
	WaitingForOwner int               `json:"waiting_for_owner"`
	AIAccounts      map[string]string `json:"ai_accounts"` // harness → signed_in | none
	// Agents and AgentsWorking are the fleet build's additions: all agents on this computer
	// (running or not) and those in a turn right now. Counts only, never a name. An older
	// computer leaves them out (nil: "not reported"), which is not zero.
	Agents        *int `json:"agents,omitempty"`
	AgentsWorking *int `json:"agents_working,omitempty"`
}

// Companion is whether the owner's assistant is connected on this computer (the fleet build's
// addition; an older computer leaves it out).
type Companion struct {
	State string `json:"state"` // connected | not_set | error
}

// Activity is how much this computer's agents worked today, as two counts and nothing else:
// no agent, project or task is named, and nothing says what the work was (the fleet build's
// addition; an older computer leaves it out).
type Activity struct {
	Day string `json:"day"` // the computer's local day, 2006-01-02
	// ActiveMinutes: minutes today in which at least one agent was working (at most 1440).
	ActiveMinutes int `json:"active_minutes"`
	// AgentMinutes: the agents' working minutes added up (two agents for an hour = 120).
	AgentMinutes int `json:"agent_minutes"`
}

// Link is this computer's connection to another member of the group.
type Link struct {
	Member string `json:"member"` // the member's peer ID
	State  string `json:"state"`  // direct | relayed | offline
}

type Phone struct {
	State         string `json:"state"`                  // connected | not_set
	Paired        int    `json:"paired"`                 // paired phones and browsers
	LastSeenAt    int64  `json:"last_seen_at,omitempty"` // the last request from one of them
	Notifications bool   `json:"notifications"`
	OwnerGate     string `json:"owner_gate"` // on_with_pin | on | off
}

type WhatsApp struct {
	State           string `json:"state"`                      // connected | not_set | error
	AuthorisedChats int    `json:"authorised_chats,omitempty"` // work chats: the chats agents may see (a count, never which)
	LastEventAt     int64  `json:"last_event_at,omitempty"`
}

type Email struct {
	State string `json:"state"`         // connected | not_set | error
	Via   string `json:"via,omitempty"` // mcp | companion
}

type Health struct {
	DiskOK    bool   `json:"disk_ok"`
	MemoryOK  bool   `json:"memory_ok"`
	LastError string `json:"last_error,omitempty"` // which area failed last (an enumeration), never the error text
}

// Health.LastError values.
var lastErrors = []string{"", "whatsapp", "update", "accounts", "disk", "memory", "companion", "email"}

var (
	updateModes  = []string{UpdatePinned, UpdateAuto, UpdateOff, UpdateWaiting}
	phoneStates  = []string{Connected, NotSet}
	chanStates   = []string{Connected, NotSet, Error}
	linkStates   = []string{LinkDirect, LinkRelayed, LinkOffline}
	gateStates   = []string{GateOnWithPIN, GateOn, GateOff}
	emailVias    = []string{"", ViaMCP, ViaCompanion}
	accountState = []string{SignedIn, None}
	dayNames     = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
)

// Problems lists what makes a report incomplete or malformed ("" none): the required fields
// and the enumerations (C3).
func Problems(r Report) []string {
	var out []string
	add := func(f string, a ...any) { out = append(out, fmt.Sprintf(f, a...)) }
	if r.Report != ReportVersion {
		add("report format %d (expected %d)", r.Report, ReportVersion)
	}
	if r.Contract < 1 {
		add("no contract version")
	}
	if r.SentAt <= 0 {
		add("no sent_at")
	}
	if r.Node.Build == "" {
		add("no build")
	}
	if r.Node.BasedOn == "" {
		add("no based_on")
	}
	if r.Node.OS == "" {
		add("no os")
	}
	enum := func(name, v string, ok []string) {
		if !slices.Contains(ok, v) {
			add("%s %q is not one of %s", name, v, strings.Join(ok, ", "))
		}
	}
	enum("node.update", r.Node.Update, updateModes)
	enum("phone.state", r.Phone.State, phoneStates)
	enum("phone.owner_gate", r.Phone.OwnerGate, gateStates)
	enum("whatsapp.state", r.WhatsApp.State, chanStates)
	enum("email.state", r.Email.State, chanStates)
	enum("email.via", r.Email.Via, emailVias)
	enum("health.last_error", r.Health.LastError, lastErrors)
	for _, l := range r.Links {
		enum("links.state", l.State, linkStates)
	}
	for h, v := range r.Fleet.AIAccounts {
		enum("fleet.ai_accounts."+h, v, accountState)
	}
	if r.Companion != nil {
		enum("companion.state", r.Companion.State, chanStates)
	}
	return out
}

// Sanitize bounds a received report: strings cut to size, links only to the given members
// (peer IDs; nil = keep all), at most a few AI accounts. Enumerations are kept as they are (cut
// short), so Problems still reports a wrong one.
func Sanitize(r Report, members []string) Report {
	r.Node.Label = clip(r.Node.Label, 64)
	r.Node.OS = clip(r.Node.OS, 16)
	r.Node.Build = clip(r.Node.Build, 64)
	r.Node.BasedOn = clip(r.Node.BasedOn, 64)
	r.Node.Update = clip(r.Node.Update, 16)
	if r.Hours != nil {
		h := Hours{From: clip(r.Hours.From, 5), To: clip(r.Hours.To, 5), TZ: clip(r.Hours.TZ, 64)}
		for _, d := range r.Hours.Days {
			if d = strings.ToLower(d); slices.Contains(dayNames, d) && !slices.Contains(h.Days, d) {
				h.Days = append(h.Days, d)
			}
		}
		r.Hours = &h
	}
	if len(r.Fleet.AIAccounts) > 0 {
		acc := map[string]string{}
		for h, v := range r.Fleet.AIAccounts {
			if len(acc) < 4 && utf8.RuneCountInString(h) <= 16 {
				acc[clip(h, 16)] = clip(v, 16)
			}
		}
		r.Fleet.AIAccounts = acc
	}
	var links []Link
	for _, l := range r.Links {
		if (members == nil || slices.Contains(members, l.Member)) && len(links) < 20 && len(l.Member) <= 128 {
			links = append(links, Link{Member: l.Member, State: clip(l.State, 16)})
		}
	}
	r.Links = links
	r.Phone.State = clip(r.Phone.State, 16)
	r.Phone.OwnerGate = clip(r.Phone.OwnerGate, 16)
	r.WhatsApp.State = clip(r.WhatsApp.State, 16)
	r.Email.State = clip(r.Email.State, 16)
	r.Email.Via = clip(r.Email.Via, 16)
	r.Health.LastError = clip(r.Health.LastError, 16)
	// The fleet build's additions: a count outside what a computer can have is dropped (not
	// reported), never shown; the same for a day that is not a date.
	r.Fleet.Agents, r.Fleet.AgentsWorking = count(r.Fleet.Agents, maxAgents), count(r.Fleet.AgentsWorking, maxAgents)
	if r.Fleet.Agents != nil && r.Fleet.AgentsWorking != nil && *r.Fleet.AgentsWorking > *r.Fleet.Agents {
		r.Fleet.AgentsWorking = nil
	}
	if r.Companion != nil {
		r.Companion = &Companion{State: clip(r.Companion.State, 16)}
	}
	if a := r.Activity; a != nil {
		if _, err := time.Parse(time.DateOnly, a.Day); err != nil || a.ActiveMinutes < 0 || a.ActiveMinutes > 1440 ||
			a.AgentMinutes < a.ActiveMinutes || a.AgentMinutes > 1440*maxAgents {
			r.Activity = nil
		} else {
			r.Activity = &Activity{Day: a.Day, ActiveMinutes: a.ActiveMinutes, AgentMinutes: a.AgentMinutes}
		}
	}
	return r
}

// maxAgents bounds the agent counts of a report: far above any real computer.
const maxAgents = 10000

func count(n *int, top int) *int {
	if n == nil || *n < 0 || *n > top {
		return nil
	}
	c := *n
	return &c
}

func clip(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// Decode parses a report of this major version.
func Decode(b []byte) (Report, error) {
	if len(b) > MaxReportBytes {
		return Report{}, fmt.Errorf("report larger than %d bytes", MaxReportBytes)
	}
	var head struct {
		Report int `json:"report"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return Report{}, fmt.Errorf("report is not JSON: %w", err)
	}
	if head.Report != ReportVersion {
		return Report{}, fmt.Errorf("report format %d (this tmx reads %d)", head.Report, ReportVersion)
	}
	var r Report
	if err := json.Unmarshal(b, &r); err != nil {
		return Report{}, err
	}
	return r, nil
}

// StateKey is what "the state changed" compares: the report without its times and the counts
// that move all day (agents running, waiting), which the 15-minute report carries anyway.
func StateKey(r Report) string {
	r.SentAt = 0
	r.Phone.LastSeenAt = 0
	r.WhatsApp.LastEventAt = 0
	r.Fleet.AgentsRunning, r.Fleet.WaitingForOwner = 0, 0
	r.Fleet.Agents, r.Fleet.AgentsWorking, r.Activity = nil, nil, nil
	slices.SortFunc(r.Links, func(a, b Link) int { return strings.Compare(a.Member, b.Member) })
	b, _ := json.Marshal(r)
	return string(b)
}

// Change is one state change of a computer, as the admin records it.
type Change struct {
	At    int64  `json:"at"`
	Field string `json:"field"`
	Old   string `json:"old"`
	New   string `json:"new"`
}

// Changes lists what changed from prev to next (the fields the Team page shows; links by member).
func Changes(prev, next Report, at int64) []Change {
	var out []Change
	cmp := func(field, a, b string) {
		if a != b {
			out = append(out, Change{At: at, Field: field, Old: a, New: b})
		}
	}
	yes := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	cmp("build", prev.Node.Build, next.Node.Build)
	cmp("based_on", prev.Node.BasedOn, next.Node.BasedOn)
	cmp("update", prev.Node.Update, next.Node.Update)
	cmp("phone", prev.Phone.State, next.Phone.State)
	cmp("owner_gate", prev.Phone.OwnerGate, next.Phone.OwnerGate)
	cmp("email", prev.Email.State, next.Email.State)
	cmp("whatsapp", prev.WhatsApp.State, next.WhatsApp.State)
	if prev.Companion != nil && next.Companion != nil { // both report it: an upgrade is not a change
		cmp("companion", prev.Companion.State, next.Companion.State)
	}
	cmp("disk_ok", yes(prev.Health.DiskOK), yes(next.Health.DiskOK))
	cmp("memory_ok", yes(prev.Health.MemoryOK), yes(next.Health.MemoryOK))
	for _, h := range sortedKeys(prev.Fleet.AIAccounts, next.Fleet.AIAccounts) {
		cmp("ai_accounts."+h, prev.Fleet.AIAccounts[h], next.Fleet.AIAccounts[h])
	}
	pl, nl := linkMap(prev.Links), linkMap(next.Links)
	for _, m := range sortedKeys(pl, nl) {
		cmp("link."+m, pl[m], nl[m])
	}
	return out
}

func linkMap(ls []Link) map[string]string {
	m := map[string]string{}
	for _, l := range ls {
		m[l.Member] = l.State
	}
	return m
}

func sortedKeys(a, b map[string]string) []string {
	var ks []string
	for k := range a {
		ks = append(ks, k)
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			ks = append(ks, k)
		}
	}
	slices.Sort(ks)
	return ks
}
