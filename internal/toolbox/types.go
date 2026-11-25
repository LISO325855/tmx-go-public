// Package toolbox is the Resources section (docs/RESOURCES.md): one list of everything the
// agents can reach to act in the world - channels, tools, accounts, devices and storage. Each
// resource is one record: what it is, who holds it, how an agent uses it, under which rule,
// and whether it works right now.
//
// The name: the owner reads "Resources"; in code "resources" already means processor, memory and
// disk load (internal/resources, GET /api/resources), so this package is toolbox.
//
// A record never holds a secret: identities are labels, never a number, an address, a token
// or a password (Validate refuses those shapes; the seed test runs it on every line).
package toolbox

// Kinds of resource, in the order the page groups them.
const (
	KindCommunication = "communication"
	KindTool          = "tool"
	KindAccount       = "account"
	KindDevice        = "device"
	KindStorage       = "storage"
)

// Kinds lists the kinds in page order.
var Kinds = []string{KindCommunication, KindTool, KindAccount, KindDevice, KindStorage}

// States: what was last established about a resource.
const (
	StateWorks       = "works"
	StatePartly      = "partly"
	StatePlanned     = "planned"
	StateNotVerified = "not_verified"
	// StateNotConnected: it exists and is meant for the agents, but no agent can reach it yet.
	StateNotConnected = "not_connected"
)

// States lists the valid states.
var States = []string{StateWorks, StatePartly, StatePlanned, StateNotVerified, StateNotConnected}

// Action types: what an action does to the world.
const (
	ActionRead      = "read"
	ActionSendFleet = "send_as_fleet"
	ActionSendOwner = "send_as_owner"
	ActionAct       = "act"
)

// Route kinds: how an agent reaches an action.
const (
	RouteTool = "tool" // a built-in tool of the Go node (Route.Tool names it)
	RouteMail = "mail" // fleet mail to the holder (Route.To names it)
	RouteNone = "none" // no route yet
)

// Health levels, measured by the node.
const (
	HealthGood    = "good"
	HealthWarning = "warning"
	HealthDown    = "down"
)

// Sources of a record.
const (
	SourceLive     = "live"     // this node derives it from a service it runs
	SourceDeclared = "declared" // written down by an agent (anything this node cannot measure)
)

// Resource is one line of the Resources section.
type Resource struct {
	ID      string `json:"id"`   // short and fixed, e.g. agent-phone
	Name    string `json:"name"` // what a human reads
	Kind    string `json:"kind"`
	Summary string `json:"summary"` // one sentence: what it is for
	// Identity: whose face it shows to the world. A label, never the number or address.
	Identity Identity `json:"identity"`
	// Machine it lives on; Holder is the agent or shell that runs it (name@machine), empty
	// when Go itself runs it. HolderNote says more when the holder is not one agent.
	Machine    string `json:"machine,omitempty"`
	Holder     string `json:"holder,omitempty"`
	HolderNote string `json:"holder_note,omitempty"`
	// Node is the Go node that can measure it (default: Machine). Only that node shows the
	// record as live; every other node shows it as declared.
	Node       string `json:"node,omitempty"`
	Maintainer string `json:"maintainer,omitempty"` // the lead to ask when it is broken
	Parent     string `json:"parent,omitempty"`     // the id of the resource it depends on
	// How: how an agent uses it today, in a sentence or two.
	How     string   `json:"how,omitempty"`
	Actions []Action `json:"actions"`
	Rule    Rule     `json:"rule"`
	State   string   `json:"state"` // works, partly, planned, not_verified or not_connected
	// StateNote: where the state comes from ("code read", "from memory, 16 Nov").
	StateNote  string `json:"state_note,omitempty"`
	VerifiedAt string `json:"verified_at,omitempty"` // ISO date (2026-11-17) or RFC 3339 time
	VerifiedBy string `json:"verified_by,omitempty"`
	// Health is filled by the node when it can measure; nil otherwise.
	Health *Health `json:"health,omitempty"`
	// Secrets: the names of the secrets it needs. Names only.
	Secrets []Secret `json:"secrets,omitempty"`
	// Meter: what is left (quota of an AI account, credit on the SIM, free space on a disk).
	Meter *Meter `json:"meter,omitempty"`
	// Live names the service of the node this record is read from: whatsapp, accounts or
	// companion ("" = none). Source says whether that happened for this answer.
	Live   string `json:"live,omitempty"`
	Source string `json:"source"`
	// Page: the dashboard page that shows more (a hash path such as /usage).
	Page string `json:"page,omitempty"`
}

// Identity is whose face a resource shows: the owner, the fleet, another person or nobody.
type Identity struct {
	Who   string `json:"who"`             // owner | fleet | other | none
	Label string `json:"label,omitempty"` // "the owner's number", "the fleet's alias"
	// Person: the key of the People entry it points at, when there is one.
	Person string `json:"person,omitempty"`
}

// Action is one thing an agent can do with a resource.
type Action struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Route Route  `json:"route"`
	Note  string `json:"note,omitempty"`
}

// Route is how an action is reached.
type Route struct {
	Kind string `json:"kind"`
	Tool string `json:"tool,omitempty"` // RouteTool: the Go tool's name
	To   string `json:"to,omitempty"`   // RouteMail: the holder to mail (name@machine)
}

// Rule is the sentence a human reads, plus the id in the rules registry when there is one.
type Rule struct {
	Text string `json:"text"`
	ID   string `json:"id,omitempty"`
}

// Health is what the node measured.
type Health struct {
	Level     string `json:"level"`
	CheckedAt int64  `json:"checked_at"` // Unix ms
	Reason    string `json:"reason,omitempty"`
}

// Secret is a secret by name. Stored is nil when this node cannot tell.
type Secret struct {
	Name   string `json:"name"`
	Stored *bool  `json:"stored,omitempty"`
}

// Meter is what is left.
type Meter struct {
	Label string `json:"label"` // "Claude (work): 5-hour limit"
	Text  string `json:"text"`  // "38% left"
	// UsedPercent 0-100, or -1 when the meter is not a percentage.
	UsedPercent float64 `json:"used_percent"`
}

// List is the answer of GET /api/toolbox.
type List struct {
	Machine   string     `json:"machine"` // the node that answered
	Resources []Resource `json:"resources"`
	UpdatedAt int64      `json:"updated_at"` // Unix ms
}
