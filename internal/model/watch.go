package model

// WatchParty is the From of the watch's mail to the head (internal/fleet/watch.go): one digest
// of what the machine's guards and the fleet's own checks noticed, never one mail per event.
const WatchParty = "tmx-watch"

// Watch statuses. Anything but WatchOK is a problem the board keeps until it clears.
const (
	WatchOK = "ok"
)

// WatchItem is one reading for the watch board. A source reports the same Key again with every
// reading; the board keeps one row per key and starts a new episode when Status changes.
type WatchItem struct {
	// Key names what is watched: "memory", "disk:C:", "budget:<agent id>", "agent:<agent id>", …
	Key string `json:"key"`
	// Status is WatchOK (clears the row) or a short word for the problem ("low", "stuck", …).
	Status string `json:"status"`
	// Text is the line the head reads. With AgentID set the board puts the agent's name first.
	Text    string `json:"text"`
	AgentID string `json:"agent_id,omitempty"`
	// Urgent: told to the head at once (once per episode), not held for the next digest.
	Urgent bool `json:"urgent,omitempty"`
	// Note: a one-off event ("x was restarted"), not a state: it is told once and forgotten.
	Note bool `json:"note,omitempty"`
}

// WatchRow is one row of the board (GET /api/watch).
type WatchRow struct {
	Key      string `json:"key"`
	Status   string `json:"status"`
	Text     string `json:"text"`
	Urgent   bool   `json:"urgent,omitempty"`
	Since    int64  `json:"since"`              // Unix ms: this episode began
	Updated  int64  `json:"updated"`            // Unix ms: the last reading
	Notified int64  `json:"notified,omitempty"` // Unix ms: told to the head (0 = not yet)
}

// WatchView is GET /api/watch: the problems now, what waits for the next digest, and when the
// head was last told.
type WatchView struct {
	Enabled     bool       `json:"enabled"`
	Rows        []WatchRow `json:"rows"`
	Notes       []string   `json:"notes"`        // one-off events waiting for the next digest
	LastDigest  int64      `json:"last_digest"`  // Unix ms; 0 = never
	LastUrgent  int64      `json:"last_urgent"`  // Unix ms; 0 = never
	LastDaily   string     `json:"last_daily"`   // the local day (2006-01-02) of the last daily digest
	DigestEvery int        `json:"digest_every"` // minutes between digests
}

// WatchSample is what the watch reads from one resource sample (resources.Sample.WatchSample).
type WatchSample struct {
	WrittenToday uint64 // all agents, today
	MemFree      uint64
	MemTotal     uint64
	DiskFree     uint64
	DiskTotal    uint64
	DiskPath     string
	Agents       []WatchAgentUse
}

// WatchAgentUse is one agent's writing today against its budget (0 = no budget).
type WatchAgentUse struct {
	AgentID string
	Written uint64
	Budget  uint64
}

// Machine process kinds (MachineProcess.Kind).
const (
	MachineTool       = "tool"        // a registered tool an agent started (process_start)
	MachineSystemTool = "system_tool" // a tool of the daemon's own
	MachineShell      = "shell"       // a shell agent: the owner's terminal
	MachineProcessRun = "process"     // a long-running process seen under an agent's harness
)

// MachineProcess is one entry of the machine registry (GET /api/registry): every shell,
// registered tool and long-running process agents started on this machine. Command is the
// executable's name only: arguments and environment may hold secrets and are never listed.
type MachineProcess struct {
	// ID names the entry for stop and restart: "tool:<id>", "shell:<agent id>" or
	// "pid:<pid>:<start token>" (a PID together with its start time, never a name).
	ID   string `json:"id"`
	Kind string `json:"kind"`
	What string `json:"what"` // its purpose, or what it is
	// Command: the executable's name; for a shell its start command (its brief's first line).
	Command   string `json:"command"`
	AgentID   string `json:"agent_id,omitempty"`
	Agent     string `json:"agent,omitempty"` // who started it
	ProjectID string `json:"project_id,omitempty"`
	Project   string `json:"project,omitempty"`
	// ThreadID and Thread: the board thread a shell was launched on, if any.
	ThreadID string `json:"thread_id,omitempty"`
	Thread   string `json:"thread,omitempty"`
	Since    int64  `json:"since"` // Unix ms: started, or first seen
	// State: running | detached (its parent ended, its agent still runs) | orphan (its agent
	// is stopped or gone) | stopped | exited | retrying | stopping.
	State    string `json:"state"`
	PID      int    `json:"pid,omitempty"`
	Orphan   bool   `json:"orphan,omitempty"`
	Why      string `json:"why,omitempty"` // why it is flagged
	Restarts int    `json:"restarts,omitempty"`
	// RestartsWithNode: a shell the node starts again when it starts (after a reboot or a
	// daemon start); its start command (Command) is then typed again.
	RestartsWithNode bool `json:"restarts_with_node,omitempty"`
	// Actions the caller may take on it: "stop", "restart".
	Actions []string `json:"actions"`
}
