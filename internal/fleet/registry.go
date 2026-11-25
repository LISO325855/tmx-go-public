package fleet

// The machine registry (DESIGN.md "Machine registry"): ONE list of every shell, registered
// tool and long-running process agents started on this machine, so nothing is left running
// that nobody knows about. Per entry: what it is, who started it, the project, the command
// (the executable's name only), since when, its state and its PID.
//
// Three sources:
//   - registered tools (internal/processes: process_start, system tools);
//   - shell agents: the owner's terminals, and the shells agents launch. The registry is the
//     shells' handler: who launched each, for which project and thread, its start command,
//     whether it starts again with the node, and stop / restart;
//   - processes seen under an agent's harness (commands the agent ran that keep running:
//     dev servers, script workers, watchers). The registry remembers each by PID and start
//     time. When its agent is stopped or gone and the process still runs, it is an ORPHAN:
//     flagged here and told to the head in the watch's digest.
//
// Stop is always by identity: a registered tool by its registry ID, a seen process by PID
// together with its start time (sysinfo.KillExact) — never by name, never a tree. Restart
// exists for registered tools only (tmx knows their command; of a seen process it does not).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/sysinfo"
)

const (
	// procMinAge: a process under an agent is listed once it has run this long (the many
	// short commands an agent runs are not registry entries).
	procMinAge = time.Minute
	// procMaxSeen bounds what the registry remembers.
	procMaxSeen = 2000
)

// procNoise are console helpers every command gets on Windows: never entries of their own.
var procNoise = map[string]bool{"conhost": true, "openconsole": true}

// seenProc is one process seen under an agent's harness.
type seenProc struct {
	PID       int    `json:"pid"`
	Start     int64  `json:"start"`
	Name      string `json:"name"`
	AgentID   string `json:"agent_id"`
	Agent     string `json:"agent"` // its name then (the agent may be gone now)
	ProjectID string `json:"project_id,omitempty"`
	FirstSeen int64  `json:"first_seen"`
	// Attached: in its agent's process tree at the last scan.
	Attached bool `json:"attached"`
}

func (p seenProc) key() string {
	return "pid:" + strconv.Itoa(p.PID) + ":" + strconv.FormatInt(p.Start, 10)
}

// procRegistry is what the registry remembers between scans. Its own mutex; never held
// together with Fleet.mu by the same call path in the other order (scan: Fleet.mu, then mu).
type procRegistry struct {
	mu     sync.Mutex
	seen   map[string]*seenProc
	shells map[string]*shellLaunch // by shell agent ID
	path   string
	loaded bool
	dirty  bool
	// procs, root and kill are the OS (tests replace them). root is the identity tmx recorded
	// for an agent's harness when it started it: only a proven harness has children here.
	procs func() ([]sysinfo.Proc, error)
	root  func(pid int) (sysinfo.Proc, bool)
	kill  func(sysinfo.Proc) error
}

// shellLaunch is who launched a shell and on which thread (an agent keeps no such record: its
// parent is its place in the tree, and the owner's launches hang under the head too).
type shellLaunch struct {
	By       string `json:"by"`              // an agent's name then, or "owner"
	ByID     string `json:"by_id,omitempty"` // the launching agent's ID; "" = the owner
	ThreadID string `json:"thread_id,omitempty"`
	At       int64  `json:"at"`
}

// registryFile is registry.json.
type registryFile struct {
	Seen   []*seenProc             `json:"seen"`
	Shells map[string]*shellLaunch `json:"shells"`
}

func (f *Fleet) initRegistry() {
	f.procreg = &procRegistry{seen: map[string]*seenProc{}, shells: map[string]*shellLaunch{}, procs: sysinfo.Procs, root: sysinfo.RegisteredRoot, kill: sysinfo.KillExact}
	if f.opts.Paths.Home != "" {
		f.procreg.path = filepath.Join(f.opts.Paths.Home, "registry.json")
	}
}

func (g *procRegistry) loadLocked() {
	if g.loaded {
		return
	}
	g.loaded = true
	if g.path == "" {
		return
	}
	raw, err := os.ReadFile(g.path)
	if err != nil {
		return
	}
	var file registryFile
	if json.Unmarshal(raw, &file) != nil {
		return
	}
	for id, s := range file.Shells {
		if s != nil && id != "" {
			g.shells[id] = s
		}
	}
	for _, p := range file.Seen {
		if p != nil && p.PID > 0 && p.Start > 0 && p.AgentID != "" {
			p.Attached = false // a new daemon: proven again by the next scan
			g.seen[p.key()] = p
		}
	}
}

func (g *procRegistry) saveLocked() {
	if !g.dirty || g.path == "" {
		return
	}
	g.dirty = false
	list := make([]*seenProc, 0, len(g.seen))
	for _, p := range g.seen {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].key() < list[j].key() })
	if raw, err := json.Marshal(registryFile{Seen: list, Shells: g.shells}); err == nil {
		_ = config.WriteFileAtomic(g.path, raw, 0o600)
	}
}

func procName(name string) string {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(name, `\`, "/")))
	return strings.TrimSuffix(base, ".exe")
}

// regAgent is what the registry needs to know of an agent.
type regAgent struct {
	id, name, project string
	pid               int
	running, shell    bool
	level             model.Level
	since             int64
	// For a shell: its start command (the brief), its state, when it was launched, and
	// whether the node starts it again.
	brief, state string
	created      int64
	desired      bool
}

func (f *Fleet) regAgentsLocked() map[string]regAgent {
	out := make(map[string]regAgent, len(f.agents))
	for id, r := range f.agents {
		out[id] = regAgent{id: id, name: r.a.Name, project: r.a.ProjectID, pid: r.a.PID, level: r.a.Level,
			running: r.proc != nil && r.a.PID > 0, shell: r.a.Harness == model.HarnessShell, since: r.procStarted,
			brief: r.a.Brief, state: string(r.a.State), created: r.a.CreatedAt, desired: r.a.Desired == "running"}
	}
	return out
}

// scanProcesses reads the process table once and updates what is seen under the agents.
// Read-only towards the machine: it starts, stops and signals nothing.
func (f *Fleet) scanProcesses() {
	g := f.procreg
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	agents := f.regAgentsLocked()
	f.mu.Unlock()
	rows, err := g.procs()
	if err != nil {
		return // unreadable (or not supported here): what is known stays as it is
	}
	alive := make(map[int]int64, len(rows))
	for _, p := range rows {
		alive[p.PID] = p.Start
	}
	now := model.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.loadLocked()
	attached := map[string]bool{}
	for _, a := range agents {
		// A shell is an entry itself (below): what runs in it is its command, not listed apart.
		if !a.running || a.shell {
			continue
		}
		// Only a harness this daemon started, still the same process (PID and start time):
		// a PID that is someone else's now has no children of this agent.
		root, ok := g.root(a.pid)
		if !ok || alive[a.pid] != root.Start {
			continue
		}
		tree := sysinfo.Tree(rows, a.pid)
		for _, p := range tree {
			if p.PID == a.pid || p.Start <= 0 || procNoise[procName(p.Name)] || sysinfo.IsHostImage(p.Name) {
				continue
			}
			sp := seenProc{PID: p.PID, Start: p.Start}
			key := sp.key()
			attached[key] = true
			cur := g.seen[key]
			if cur == nil {
				if len(g.seen) >= procMaxSeen {
					continue
				}
				g.seen[key] = &seenProc{PID: p.PID, Start: p.Start, Name: p.Name, AgentID: a.id, Agent: a.name,
					ProjectID: a.project, FirstSeen: now, Attached: true}
				g.dirty = true
				continue
			}
			if !cur.Attached || cur.Agent != a.name && cur.AgentID == a.id {
				cur.Attached, g.dirty = true, true
				if cur.AgentID == a.id {
					cur.Agent, cur.ProjectID = a.name, a.project
				}
			}
		}
	}
	for id := range g.shells {
		if a, ok := agents[id]; !ok || !a.shell {
			delete(g.shells, id) // the shell was removed
			g.dirty = true
		}
	}
	for key, p := range g.seen {
		if start, ok := alive[p.PID]; !ok || start != p.Start {
			delete(g.seen, key) // it ended (or its PID is another process now)
			g.dirty = true
			continue
		}
		if !attached[key] && p.Attached {
			p.Attached, g.dirty = false, true
		}
	}
	g.saveLocked()
}

// Registry is GET /api/registry: the owner and the head see everything; another agent sees
// what it started itself. project ("" = all) filters by project ID.
func (f *Fleet) Registry(c Caller, project string) ([]model.MachineProcess, error) {
	if !c.Owner && c.AgentID == "" {
		return nil, errf(KindForbidden, "only the owner and agents read the registry")
	}
	f.scanProcesses()
	return f.registryEntries(c, project)
}

func (f *Fleet) registryEntries(c Caller, project string) ([]model.MachineProcess, error) {
	f.mu.Lock()
	if _, err := f.callerRecLocked(c); err != nil {
		f.mu.Unlock()
		return nil, err
	}
	agents := f.regAgentsLocked()
	projects := map[string]string{}
	for id, p := range f.projects {
		projects[id] = p.Name
	}
	threads := map[string]string{} // the threads shells were launched on, by ID
	f.procreg.mu.Lock()
	f.procreg.loadLocked()
	for _, s := range f.procreg.shells {
		if s.ThreadID != "" {
			threads[s.ThreadID] = ""
		}
	}
	f.procreg.mu.Unlock()
	for id := range threads {
		threads[id] = f.threadTitleLocked(id)
	}
	f.mu.Unlock()
	all := c.Owner || agents[c.AgentID].level == model.LevelHead
	now := model.Now()
	out := []model.MachineProcess{}
	add := func(e model.MachineProcess) {
		if (project != "" && e.ProjectID != project) || (!all && e.AgentID != c.AgentID) {
			return
		}
		e.Project = projects[e.ProjectID]
		e.Actions = f.registryActions(c, all, e)
		out = append(out, e)
	}
	for _, p := range f.processes.List() {
		e := model.MachineProcess{ID: "tool:" + p.ID, Kind: model.MachineTool, What: p.Purpose, Command: p.Command,
			ProjectID: p.ProjectID, Since: p.StartedAt, State: p.State, PID: p.PID, Restarts: p.Restarts}
		switch {
		case p.Category == model.CategorySystemTool:
			e.Kind, e.Agent = model.MachineSystemTool, "tmx"
		case p.OwnerID == model.Owner:
			e.Agent = "the owner (a project tool)"
			if a, ok := agents[p.CreatorID]; ok {
				e.Agent = a.name + " (now a project tool)"
			}
		default:
			a, ok := agents[p.OwnerID]
			e.AgentID, e.Agent = p.OwnerID, a.name
			if p.PID > 0 && (!ok || !a.running) {
				e.Orphan, e.State = true, "orphan"
				e.Why = "its agent is gone"
				if ok {
					e.Why = a.name + " is not running"
				}
			}
		}
		add(e)
	}
	g := f.procreg
	g.mu.Lock()
	g.loadLocked()
	launches := make(map[string]shellLaunch, len(g.shells))
	for id, s := range g.shells {
		launches[id] = *s
	}
	g.mu.Unlock()
	for _, a := range agents {
		if !a.shell {
			continue
		}
		// A shell with no launch record is from before agents could launch shells: the owner's.
		l, known := launches[a.id]
		e := model.MachineProcess{ID: "shell:" + a.id, Kind: model.MachineShell, What: "shell " + a.name, Command: shellCommand(a.brief),
			AgentID: l.ByID, Agent: "the owner", ProjectID: a.project, ThreadID: l.ThreadID, Thread: threads[l.ThreadID],
			Since: a.created, State: a.state, RestartsWithNode: a.desired}
		if known && l.ByID != "" {
			e.Agent = l.By
			if by, ok := agents[l.ByID]; ok {
				e.Agent = by.name
			}
		}
		if a.running {
			e.PID = a.pid
			if a.since > 0 {
				e.Since = a.since
			}
			if by, ok := agents[l.ByID]; l.ByID != "" && (!ok || !by.running) {
				e.Orphan, e.State = true, "orphan"
				e.Why = l.By + " was removed"
				if ok {
					e.Why = by.name + " is not running"
				}
			}
		}
		add(e)
	}
	g.mu.Lock()
	seen := make([]seenProc, 0, len(g.seen))
	for _, p := range g.seen {
		seen = append(seen, *p)
	}
	g.mu.Unlock()
	for _, p := range seen {
		a, ok := agents[p.AgentID]
		e := model.MachineProcess{ID: p.key(), Kind: model.MachineProcessRun, What: "started by " + p.Agent + "'s harness",
			Command: p.Name, AgentID: p.AgentID, Agent: p.Agent, ProjectID: p.ProjectID, Since: p.FirstSeen, State: "running", PID: p.PID}
		switch {
		case p.Attached:
			if now-p.FirstSeen < procMinAge.Milliseconds() {
				continue // one of the many short commands
			}
		case !ok:
			e.Orphan, e.State, e.Why = true, "orphan", p.Agent+" was removed"
		case !a.running:
			e.Orphan, e.State, e.Why = true, "orphan", a.name+" is not running"
		default:
			e.State, e.Why = "detached", "its parent process ended; "+a.name+" still runs"
		}
		add(e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Orphan != out[j].Orphan {
			return out[i].Orphan
		}
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		if out[i].Agent != out[j].Agent {
			return out[i].Agent < out[j].Agent
		}
		return out[i].Since < out[j].Since
	})
	return out, nil
}

// registryActions is what the caller may do to e. The rules, in one place:
//   - a system tool: nothing here (the daemon restarts its own tools);
//   - a shell: stop and restart, by the owner; a shell an agent launched also by that agent
//     and by the head. The owner's own shells are the owner's alone;
//   - a registered tool: stop and restart, by the owner, the head, or the agent that owns it;
//   - a seen process: stop only. The owner may stop any; the head only an orphan (stopping a
//     live agent's child is that agent's business, or the owner's); an agent only its own.
func (f *Fleet) registryActions(c Caller, all bool, e model.MachineProcess) []string {
	acts := []string{}
	switch e.Kind {
	case model.MachineTool:
		if c.Owner || all || (e.AgentID != "" && e.AgentID == c.AgentID) {
			if e.PID > 0 {
				acts = append(acts, "stop")
			}
			acts = append(acts, "restart")
		}
	case model.MachineShell:
		if c.Owner || (e.AgentID != "" && (all || e.AgentID == c.AgentID)) {
			if e.PID > 0 {
				acts = append(acts, "stop")
			}
			acts = append(acts, "restart")
		}
	case model.MachineProcessRun:
		if c.Owner || (all && e.Orphan) || e.AgentID == c.AgentID {
			acts = append(acts, "stop")
		}
	}
	return acts
}

func hasAction(e model.MachineProcess, act string) bool {
	for _, a := range e.Actions {
		if a == act {
			return true
		}
	}
	return false
}

func (f *Fleet) registryEntry(c Caller, id string) (model.MachineProcess, error) {
	f.scanProcesses()
	list, err := f.registryEntries(c, "")
	if err != nil {
		return model.MachineProcess{}, err
	}
	for _, e := range list {
		if e.ID == id {
			return e, nil
		}
	}
	// Seen but younger than the listing age: still stoppable by who may.
	if strings.HasPrefix(id, "pid:") {
		g := f.procreg
		g.mu.Lock()
		p, ok := g.seen[id]
		var sp seenProc
		if ok {
			sp = *p
		}
		g.mu.Unlock()
		if ok && (c.Owner || sp.AgentID == c.AgentID) {
			e := model.MachineProcess{ID: id, Kind: model.MachineProcessRun, Command: sp.Name, AgentID: sp.AgentID, Agent: sp.Agent, PID: sp.PID, State: "running"}
			e.Actions = []string{"stop"}
			return e, nil
		}
	}
	return model.MachineProcess{}, errf(KindNotFound, "no registry entry %q (it may have ended)", id)
}

// RegistryStop stops one entry by its registry ID: a registered tool through the tool
// registry, a seen process by PID together with its start time. Never by name.
func (f *Fleet) RegistryStop(c Caller, id string) error {
	e, err := f.registryEntry(c, id)
	if err != nil {
		return err
	}
	if !hasAction(e, "stop") {
		return errf(KindForbidden, "%s", registryRefusal(e, "stop"))
	}
	who := f.callerName(c)
	switch e.Kind {
	case model.MachineTool:
		f.processes.Stop(strings.TrimPrefix(id, "tool:"))
	case model.MachineShell:
		// The registry's own rule decided (registryActions); the stop is the agent's ordinary
		// one: its verified process tree, by PID.
		f.stopProcess(f.ctx, strings.TrimPrefix(id, "shell:"), false)
	case model.MachineProcessRun:
		pid, start, ok := parsePidID(id)
		if !ok {
			return errf(KindInvalid, "not a registry ID: %q", id)
		}
		if err := f.procreg.kill(sysinfo.Proc{PID: pid, Start: start}); err != nil {
			switch err {
			case sysinfo.ErrProtected:
				return errf(KindForbidden, "%s (PID %d) is a protected program; tmx does not stop it", e.Command, pid)
			case sysinfo.ErrNotChild:
				return errf(KindConflict, "PID %d is a different process now; nothing was stopped", pid)
			}
			return errf(KindConflict, "could not stop %s (PID %d): %v", e.Command, pid, err)
		}
	default:
		return errf(KindForbidden, "%s", registryRefusal(e, "stop"))
	}
	f.log.Info("registry stop", "id", id, "command", e.Command, "pid", e.PID, "agent", e.Agent, "by", who)
	f.scanProcesses()
	return nil
}

// RegistryRestart restarts a registered tool with the command it had. It answers the tool's
// new entry (a restart is a new registry ID).
func (f *Fleet) RegistryRestart(c Caller, id string) (model.MachineProcess, error) {
	e, err := f.registryEntry(c, id)
	if err != nil {
		return model.MachineProcess{}, err
	}
	if !hasAction(e, "restart") {
		return model.MachineProcess{}, errf(KindForbidden, "%s", registryRefusal(e, "restart"))
	}
	if e.Kind == model.MachineShell {
		// A new process of the shell: its start command is typed again (shell_start.go).
		sid := strings.TrimPrefix(id, "shell:")
		if _, err := f.RestartAgent(OwnerCaller, sid); err != nil {
			return model.MachineProcess{}, err
		}
		f.log.Info("registry restart", "id", id, "command", e.Command, "agent", e.Agent, "by", f.callerName(c))
		list, err := f.registryEntries(c, "")
		if err != nil {
			return model.MachineProcess{}, err
		}
		for _, n := range list {
			if n.ID == id {
				return n, nil
			}
		}
		return e, nil
	}
	if f.opts.Elevated() {
		return model.MachineProcess{}, errf(KindForbidden, "refusing to launch from an elevated process")
	}
	p, err := f.processes.Restart(strings.TrimPrefix(id, "tool:"))
	if err != nil {
		return model.MachineProcess{}, errf(KindConflict, "could not restart: %s", err)
	}
	f.log.Info("registry restart", "id", id, "new", p.ID, "command", e.Command, "agent", e.Agent, "by", f.callerName(c))
	list, err := f.registryEntries(c, "")
	if err != nil {
		return model.MachineProcess{}, err
	}
	for _, n := range list {
		if n.ID == "tool:"+p.ID {
			return n, nil
		}
	}
	return model.MachineProcess{ID: "tool:" + p.ID, Kind: model.MachineTool, What: p.Purpose, Command: p.Command, State: p.State, PID: p.PID, Actions: []string{}}, nil
}

func registryRefusal(e model.MachineProcess, act string) string {
	switch {
	case e.Kind == model.MachineSystemTool:
		return "a tool of tmx's own is not yours to " + act + "; the daemon restarts it itself"
	case e.Kind == model.MachineShell:
		return "this shell is the owner's own terminal (or another agent's shell): not yours to " + act
	case e.Kind == model.MachineProcessRun && act == "restart":
		return "tmx does not know the command of a process it only saw; start it again where it was started"
	case e.Kind == model.MachineProcessRun && !e.Orphan:
		return fmt.Sprintf("%s (PID %d) belongs to %s, which still runs: ask it, or the owner stops it", e.Command, e.PID, e.Agent)
	}
	return "this entry is not yours to " + act
}

func parsePidID(id string) (pid int, start int64, ok bool) {
	parts := strings.Split(id, ":")
	if len(parts) != 3 || parts[0] != "pid" {
		return 0, 0, false
	}
	pid, err1 := strconv.Atoi(parts[1])
	start, err2 := strconv.ParseInt(parts[2], 10, 64)
	return pid, start, err1 == nil && err2 == nil && pid > 0 && start > 0
}

// watchProcScan is the watch's part: one row per agent whose processes run on after it.
func (f *Fleet) watchProcScan() {
	b := f.watch.board
	if !b.on() {
		return
	}
	f.scanProcesses()
	list, err := f.registryEntries(OwnerCaller, "")
	if err != nil {
		return
	}
	type group struct {
		agent, why string
		names      []string
	}
	groups := map[string]*group{}
	for _, e := range list {
		if !e.Orphan {
			continue
		}
		k := e.AgentID
		if groups[k] == nil {
			groups[k] = &group{agent: e.Agent, why: e.Why}
		}
		groups[k].names = append(groups[k].names, fmt.Sprintf("%s (PID %d)", e.Command, e.PID))
	}
	keys := map[string]bool{}
	var items []model.WatchItem
	for id, g := range groups {
		key := "orphan:" + id
		keys[key] = true
		names := g.names
		more := ""
		if len(names) > 4 {
			more = fmt.Sprintf(" and %d more", len(names)-4)
			names = names[:4]
		}
		items = append(items, model.WatchItem{Key: key, Status: "orphan",
			Text: fmt.Sprintf("%d process(es) started by %s still run although %s: %s%s. See the registry; stop them there if they are not needed",
				len(g.names), g.agent, g.why, strings.Join(names, ", "), more)})
	}
	b.report(items...)
	b.keep("orphan:", keys)
}

// shellCommand is a safe display of the brief's first line, redacted before bounding. A
// shell without one starts as an empty prompt.
func shellCommand(brief string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(brief), "\n")
	line = oneLine(safeShellDisplay(line), 200)
	if line == "" {
		return "(an empty prompt)"
	}
	return line
}

// regShellLaunchedLocked records who launched a shell and on which thread (LaunchAgent).
func (f *Fleet) regShellLaunchedLocked(a model.Agent, launcher *agentRec, thread string) {
	if a.Harness != model.HarnessShell {
		return
	}
	l := &shellLaunch{By: model.Owner, ThreadID: thread, At: model.Now()}
	if launcher != nil {
		l.By, l.ByID = launcher.a.Name, launcher.a.ID
	}
	g := f.procreg
	g.mu.Lock()
	g.loadLocked()
	g.shells[a.ID] = l
	g.dirty = true
	g.saveLocked()
	g.mu.Unlock()
}

// newShells are the shells launched since since (Unix ms), for the daily digest.
func (f *Fleet) newShells(since int64) []string {
	list, err := f.registryEntries(OwnerCaller, "")
	if err != nil {
		return nil
	}
	f.mu.Lock()
	created := map[string]int64{}
	for id, r := range f.agents {
		if r.a.Harness == model.HarnessShell {
			created["shell:"+id] = r.a.CreatedAt
		}
	}
	f.mu.Unlock()
	var out []string
	for _, e := range list {
		if at, ok := created[e.ID]; !ok || at < since {
			continue
		}
		line := strings.TrimPrefix(e.What, "shell ") + " (by " + e.Agent
		if e.Project != "" {
			line += ", " + e.Project
		}
		out = append(out, line+"): "+e.Command)
	}
	return out
}
