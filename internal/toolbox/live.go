package toolbox

import (
	"fmt"
	"regexp"
	"strings"
)

// Live is what a node knows right now about the services it runs and the agents it can see.
// The API layer fills it (internal/api/toolbox.go); Apply is pure, so it is tested without a
// daemon. Nothing in it may be a number, an address or a key: the callers pass states,
// counts and labels only.
type Live struct {
	Machine string // this node's name in the fleet
	Now     int64  // Unix ms

	WhatsApp    *WhatsAppLive  // nil = this node has no WhatsApp service
	Accounts    *AccountsLive  // nil = no accounts service
	Companion   *CompanionLive // nil = no Companion service
	SystemTools []SystemTool   // the process registry's system tools

	// Agents: this node's agents, name -> up or not. Peers: the other machines it sees.
	Agents map[string]bool
	Peers  map[string]PeerLive
}

// WhatsAppLive is the Go node's own WhatsApp link.
type WhatsAppLive struct {
	Enabled bool
	State   string // unlinked, linking, connecting, connected, logged_out, error
	Error   string
}

// AccountsLive is the AI accounts service.
type AccountsLive struct {
	Accounts []AccountLive
}

// AccountLive is one account of a harness. Label never holds the sign-in address.
type AccountLive struct {
	Label     string // "Claude (work)"
	Installed bool
	Connected bool
	// Fullest limit: its label and how much is used (0-100); HasLimit false = none reported.
	HasLimit    bool
	LimitLabel  string
	UsedPercent float64
}

// CompanionLive is the link between this node and Companion.
type CompanionLive struct {
	Connected  bool  // tmx -> Companion: an agent key is set
	LinkToken  bool  // Companion -> tmx: the link token exists
	LinkUsedAt int64 // Companion last read tmx (Unix ms; 0 = not since the daemon started)
}

// SystemTool is one supervised tool of the node.
type SystemTool struct {
	ID, Purpose, State string
	Restarts           int
}

// PeerLive is another machine as this node sees it.
type PeerLive struct {
	Online bool
	Agents map[string]bool // name -> up or not (the last good reading)
}

func same(a, b string) bool { return a != "" && strings.EqualFold(a, b) }

// measuredHere: this node is the one that can measure the record.
func (l Live) measuredHere(r Resource) bool {
	node := r.Node
	if node == "" {
		node = r.Machine
	}
	return same(node, l.Machine)
}

// Apply overlays what the node knows on the declared list and appends the node's system
// tools that no record describes. The input list is not changed.
func Apply(list []Resource, l Live) []Resource {
	out := make([]Resource, 0, len(list)+len(l.SystemTools))
	for _, r := range list {
		if r.Live != "" && l.measuredHere(r) {
			switch r.Live {
			case "whatsapp":
				applyWhatsApp(&r, l)
			case "accounts":
				applyAccounts(&r, l)
			case "companion":
				applyCompanion(&r, l)
			}
		}
		if r.Health == nil {
			applyHolder(&r, l)
		}
		out = append(out, r)
	}
	for _, t := range l.SystemTools {
		out = append(out, systemToolResource(t, l))
	}
	return out
}

func (l Live) health(level, reason string) *Health {
	return &Health{Level: level, CheckedAt: l.Now, Reason: reason}
}

// live marks a record as measured by this node now.
func (l Live) live(r *Resource, state string, h *Health) {
	r.Source, r.State, r.Health = SourceLive, state, h
	r.StateNote, r.VerifiedBy, r.VerifiedAt = "Measured by this node.", l.Machine, ""
}

func applyWhatsApp(r *Resource, l Live) {
	w := l.WhatsApp
	if w == nil {
		return
	}
	switch {
	case !w.Enabled:
		l.live(r, StatePartly, l.health(HealthDown, "WhatsApp is switched off on this node."))
	case w.State == "connected":
		l.live(r, StateWorks, l.health(HealthGood, "Linked and connected."))
	case w.State == "connecting":
		l.live(r, StateWorks, l.health(HealthWarning, "Linked; reconnecting."))
	case w.State == "linking":
		l.live(r, StatePartly, l.health(HealthWarning, "Waiting for the owner to link the device."))
	case w.State == "logged_out":
		l.live(r, StatePartly, l.health(HealthDown, "The phone removed this device; link it again on the WhatsApp page."))
	case w.State == "error":
		l.live(r, StatePartly, l.health(HealthDown, "WhatsApp reports an error"+reasonTail(w.Error)))
	default:
		l.live(r, StatePartly, l.health(HealthDown, "No device linked."))
	}
}

// reasonTail appends a service's own error text, unless it holds a secret shape.
func reasonTail(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" || SecretShape(msg) != "" {
		return "."
	}
	if len(msg) > 160 {
		msg = msg[:160]
	}
	return ": " + strings.TrimRight(msg, ".") + "."
}

func applyAccounts(r *Resource, l Live) {
	a := l.Accounts
	if a == nil {
		return
	}
	var installed, connected int
	var full *AccountLive
	for i, acc := range a.Accounts {
		if !acc.Installed {
			continue
		}
		installed++
		if !acc.Connected {
			continue
		}
		connected++
		if acc.HasLimit && (full == nil || acc.UsedPercent > full.UsedPercent) {
			full = &a.Accounts[i]
		}
	}
	reason := fmt.Sprintf("%d of %d accounts signed in.", connected, installed)
	switch {
	case installed == 0:
		l.live(r, StatePartly, l.health(HealthDown, "No AI harness is installed on this node."))
	case connected == 0:
		l.live(r, StatePartly, l.health(HealthDown, "No account is signed in."))
	case full != nil && full.UsedPercent >= 90:
		l.live(r, StateWorks, l.health(HealthWarning, reason+" One limit is almost used up."))
	default:
		l.live(r, StateWorks, l.health(HealthGood, reason))
	}
	if full != nil {
		used := clampPct(full.UsedPercent)
		r.Meter = &Meter{Label: strings.TrimSpace(full.Label + ": " + full.LimitLabel), Text: fmt.Sprintf("%.0f%% left (the fullest limit)", 100-used), UsedPercent: used}
	}
}

func clampPct(p float64) float64 {
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

func applyCompanion(r *Resource, l Live) {
	c := l.Companion
	if c == nil {
		return
	}
	// The declared state stays (what agents can do with Companion was established by
	// people); the node adds only what it measures: its own link.
	r.Source = SourceLive
	r.Secrets = append([]Secret(nil), r.Secrets...) // a copy: the declared list is shared
	for i := range r.Secrets {
		var stored bool
		switch r.Secrets[i].Name {
		case "Companion agent key":
			stored = c.Connected
		case "Companion link token":
			stored = c.LinkToken
		default:
			continue
		}
		r.Secrets[i].Stored = &stored
	}
	switch {
	case !c.Connected:
		r.Health = l.health(HealthWarning, "This node has no key for Companion: agents cannot hand it tasks from here.")
	case c.LinkUsedAt > 0:
		r.Health = l.health(HealthGood, "The key is set; Companion read this node "+ago(l.Now-c.LinkUsedAt)+" ago.")
	default:
		r.Health = l.health(HealthGood, "The key is set. Companion was not asked whether it is up.")
	}
}

func ago(ms int64) string {
	switch s := ms / 1000; {
	case s < 90:
		return "a minute"
	case s < 90*60:
		return fmt.Sprintf("%d minutes", (s+30)/60)
	case s < 36*3600:
		return fmt.Sprintf("%d hours", (s+1800)/3600)
	default:
		return fmt.Sprintf("%d days", (s+43200)/86400)
	}
}

// applyHolder: "is the holder up" for a record held by an agent this node can see.
func applyHolder(r *Resource, l Live) {
	name, machine, ok := strings.Cut(r.Holder, "@")
	if !ok || name == "" || machine == "" {
		return
	}
	if same(machine, l.Machine) {
		if l.Agents == nil {
			return
		}
		up, known := l.Agents[name]
		r.Health = l.holderHealth(r.Holder, known, up)
		return
	}
	for peer, p := range l.Peers {
		if !same(peer, machine) {
			continue
		}
		if !p.Online {
			r.Health = l.health(HealthWarning, "This node cannot reach "+machine+" right now, so it cannot tell whether "+name+" is up.")
			return
		}
		up, known := p.Agents[name]
		r.Health = l.holderHealth(r.Holder, known, up)
		return
	}
}

func (l Live) holderHealth(holder string, known, up bool) *Health {
	switch {
	case !known:
		return l.health(HealthDown, "Its holder "+holder+" is not in the fleet list.")
	case !up:
		return l.health(HealthDown, "Its holder "+holder+" is stopped.")
	}
	return l.health(HealthGood, "Its holder "+holder+" is up. The service itself was not checked.")
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// systemToolResource is a supervised tool of this node that no declared record describes.
func systemToolResource(t SystemTool, l Live) Resource {
	slug := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(t.Purpose), "-"), "-")
	if slug == "" {
		slug = "tool"
	}
	if len(slug) > 16 { // with "sys-" and the id it stays under the length a token check flags
		slug = strings.Trim(slug[:16], "-")
	}
	id := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(t.ID), ""), "-")
	if len(id) > 6 {
		id = id[:6]
	}
	if id == "" {
		id = "x"
	}
	name := t.Purpose
	if SecretShape(name) != "" {
		name = "System tool"
	}
	r := Resource{
		ID: "sys-" + slug + "-" + id, Name: name, Kind: KindTool,
		Summary:    "A permanent tool this node runs and restarts by itself.",
		Identity:   Identity{Who: "none"},
		Machine:    l.Machine,
		HolderNote: "Run by the Go node (a system tool, not an agent).",
		Actions:    []Action{},
		Rule:       Rule{Text: "Supervised by the node. Agents cannot stop it."},
		Source:     SourceLive, StateNote: "Measured by this node.", VerifiedBy: l.Machine,
	}
	switch t.State {
	case "running":
		r.State = StateWorks
		r.Health = l.health(HealthGood, "Running.")
		if t.Restarts > 0 {
			r.Health.Reason = fmt.Sprintf("Running; restarted %d times.", t.Restarts)
		}
	case "retrying", "exited":
		r.State = StatePartly
		r.Health = l.health(HealthWarning, "It stopped; the node is restarting it.")
	default:
		r.State = StatePartly
		r.Health = l.health(HealthDown, "Not running ("+t.State+").")
	}
	return r
}
