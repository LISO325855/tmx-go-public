// Package guard classifies an agent's tool call (a shell command, a file write) as allowed,
// held for the owner, or blocked. It never runs anything and never touches the network; the
// only files it reads are a repository's .git/HEAD (for the git rules). `tmx hook pretooluse` applies it to chat-mode Claude
// agents through a Claude Code PreToolUse hook, which runs in every permission mode, including
// bypassPermissions. See DESIGN.md "Safety".
//
// It is a seatbelt against accidents and casual prompt injection, not a sandbox: the agent runs
// as the owner's OS user, and a determined program can always hide what it does.
package guard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

// Outcome is what happens to a tool call.
type Outcome string

const (
	Allow Outcome = "allow" // runs as usual (Claude's own permission mode still applies)
	Hold  Outcome = "hold"  // waits for the owner's decision in Needs you
	Block Outcome = "block" // never runs; the agent is told why
)

func (o Outcome) rank() int {
	switch o {
	case Block:
		return 2
	case Hold:
		return 1
	}
	return 0
}

// Verdict is the result of a check.
type Verdict struct {
	Outcome  Outcome `json:"outcome"`
	Rule     string  `json:"rule,omitempty"`      // Rule.ID
	RuleName string  `json:"rule_name,omitempty"` // Rule.Name
	Why      string  `json:"why,omitempty"`       // Rule.Why
	Detail   string  `json:"detail,omitempty"`    // what exactly matched, in plain words
	Match    string  `json:"match,omitempty"`     // the part of the command that matched
}

// Message is the sentence shown to the agent for a block or a hold.
func (v Verdict) Message() string {
	var b strings.Builder
	b.WriteString("tmx guard (" + v.RuleName + "): ")
	if v.Detail != "" {
		b.WriteString(v.Detail + ". ")
	}
	b.WriteString(v.Why)
	return b.String()
}

// Call is one tool call as Claude Code's PreToolUse hook reports it.
type Call struct {
	Tool  string          `json:"tool_name"`
	Input json.RawMessage `json:"tool_input"`
	Cwd   string          `json:"cwd"`
}

// Context is where the agent stands. Paths are absolute; empty ones are ignored.
type Context struct {
	AllowBrowserAutomation bool // trusted host setting; never read from agent environment
	// States is the owner's state per switchable rule (Settings.Effective). nil = the strict
	// built-in outcomes (Rule.Outcome); a daemon always passes its effective states.
	States    map[string]Outcome
	OS        string // runtime.GOOS of the machine ("darwin", "linux", "windows")
	Cwd       string // the agent's current folder (the hook's cwd; else Own)
	Own       string // the agent's own folder: recursive deletes inside it are fine
	Workspace string // project folders: writes inside it are fine
	StateDir  string // tmx's state directory: never written, secrets never read
	// AccountHome is the agent's own harness account home when the daemon keeps it in its state
	// (<StateDir>/ai-accounts/homes/<harness>/<account>, the agent's CLAUDE_CONFIG_DIR on a
	// named account). The harness keeps the agent's conversations, memory, plans and todos
	// there, so those folders are the agent's own; the rest of the home (the sign-in, settings)
	// and every other account's home stay closed. "" = none. Set by the daemon's launch, never
	// from an environment the agent controls.
	AccountHome string
	Home        string
	Temp        []string            // scratch folders (TMPDIR, /tmp, %TEMP%): writes and deletes are fine
	Env         func(string) string // environment lookup for $VAR / %VAR%; nil = none
	// TmxNames are extra names of the tmx executable (basenames or full paths) besides "tmx".
	TmxNames []string
	// Git answers "which branch is checked out in dir, and which is the remote's default?";
	// nil = read .git (only HEAD files; nothing is run).
	Git func(dir string) (branch, remoteDefault string)
}

// StateOf is what a finding of rule does in this context: fixed rules always their outcome; with
// no owner states the strict built-in outcome; else the owner's state (missing = the default).
func (c Context) StateOf(rule string) Outcome {
	r, ok := ruleByID[rule]
	if !ok {
		return Block
	}
	if r.Fixed || c.States == nil {
		return r.Outcome
	}
	if o, ok := c.States[rule]; ok && o.valid() {
		return o
	}
	return r.Default
}

func (o Outcome) valid() bool { return o == Allow || o == Hold || o == Block }

// ValidState reports whether o is a state a rule switch may take.
func ValidState(o Outcome) bool { return o.valid() }

// Settings is the owner's guard configuration (config.json "guard"). There is no master switch:
// every agent starts with the guard, and the per-rule states (Blocked, Held for me, Allowed) are
// the whole safety model.
type Settings struct {
	AllowBrowserAutomation bool `json:"allow_browser_automation"`
	// LegacyEnabled and LegacyOffFor are the removed master switch ("enabled", "off_for") as an
	// older config.json or client still sends them. They are read so that the upgrade can say,
	// once, what it dropped (Fleet.applyRuleDefaultsLocked); nothing acts on them and they are
	// never written again.
	LegacyEnabled *bool    `json:"enabled,omitempty"`
	LegacyOffFor  []string `json:"off_for,omitempty"`
	// AlwaysAllow: the owner's standing "Allow always" answers to guard holds (Settings > Safety
	// lists them). Only the owner-gated allow-always and revoke routes change it; PUT
	// /api/safety/guard keeps the stored list whatever it is sent.
	AlwaysAllow []AlwaysAllow `json:"always_allow,omitempty"`
	// RuleStates: the owner's switch per rule (block | hold | allow); a missing rule has its
	// default. Only the owner-gated rule route changes it; PUT /api/safety/guard keeps it.
	RuleStates map[string]Outcome `json:"rule_states,omitempty"`
	// RuleLog: every switch change, newest last (at most RuleLogMax).
	RuleLog []RuleChange `json:"rule_log,omitempty"`
	// DefaultsVersion: the rule defaults this install has applied (and logged); see DefaultsVersion.
	DefaultsVersion int `json:"defaults_version,omitempty"`
	// AllowedBefore: held calls (AllowedKey) the owner allowed once, with when. The same call
	// later is allowed without a card, for the Rememberable rules only (the owner, 3 Nov 22:30:
	// "the same thing never asks twice").
	AllowedBefore []AllowedEntry `json:"allowed_before,omitempty"`
}

// AllowedEntry is one call the owner allowed.
type AllowedEntry struct {
	Key string `json:"key"`
	At  string `json:"at"` // RFC 3339, the first approval
}

// Rememberable is the ALLOW-LIST of rules whose Allow may be remembered (tmx-lead and
// go-reviewer-o4, 4 Nov 04:27, P0 on #234). Every other rule, whatever its switch says and
// including any rule added later, asks every time with a fresh proof: it is never written to
// allowed_before, never allowed without a card and never spared the PIN. Off the list on
// purpose: the 9 Sep class (disk, delete-root, permissions-root, boot, permissions-outside), the
// lockout rows (elevation, hello-key, firewall-tailscale) and tmx's owner commands. This
// matters only for a rule the owner set to Held for me: no rule is held by default.
//
// browser-unverified is on the list since 17 Nov (the owner, through tmx-lead): a scripted browser
// run was the most frequent card of a working day, and it asked again for the very script the
// owner had just allowed. The same command in the same folder by the same kind of agent now asks
// once; another script, another folder or another kind of agent still brings a card. The block
// on a browser that reaches Windows Hello (browser-unsafe-launch) is untouched.
var Rememberable = map[string]bool{
	RuleWriteOutside: true, RuleDeleteOutside: true, RuleGitRewrite: true, RulePipeShell: true,
	RuleDesktop: true, RuleClaudeConfig: true, RuleBrowserScript: true,
}

// AllowedBeforeMax is how many allowed calls are remembered (oldest dropped).
const AllowedBeforeMax = 2000

// AllowedKey identifies a held call for AllowedBefore: rule, tool, the exact command, the folder
// it ran in (relative paths mean the same files only there) and the kind of agent (its profile,
// or "level:<level>"). As narrow as an Allow always for that agent kind (go-reviewer-o4, #234).
func AllowedKey(rule, tool, command, cwd, agentKind string) string {
	sum := sha256.Sum256([]byte(rule + "\x00" + tool + "\x00" + command + "\x00" + cwd + "\x00" + agentKind))
	return hex.EncodeToString(sum[:16])
}

// DefaultsVersion is the current set of rule defaults:
//
//	1 = the owner's brief, 3 Nov 22:14 (the first rule switches).
//	2 = the master switch is gone and the rule list is the only safety model (the owner, 17 Nov
//	    16:05): Blocked = disk, delete-root, permissions-root, boot, and nothing else; every
//	    other switchable rule is Allowed. No rule is Held by default: Held for me is the
//	    owner's choice per rule. Against version 1 that moves tmx-cli, elevation, hello-key,
//	    firewall-tailscale, delete-outside, permissions-outside and git-rewrite from Held to
//	    Allowed. The four fixed holds of version 1 (browser-unverified and the three pairing
//	    tools) became switchable rules, Allowed too; kill-by-name lost its switch and is a
//	    fixed Block.
const DefaultsVersion = 2

// earlierDefaults holds, per defaults version, the state of every rule that then differed from
// what the next version does with it: what DefaultAt needs to say truthfully what an upgrade
// changed. It names fixed rules too: the state they had while they had a switch (kill-by-name),
// and the fixed outcome of rules that got a switch later.
var earlierDefaults = map[int]map[string]Outcome{
	1: {
		RuleTmxCLI: Hold, RuleElevation: Hold, RuleHelloKey: Hold, RuleNetwork: Hold,
		RuleDeleteOutside: Hold, RulePermsOutside: Hold, RuleGitRewrite: Hold,
		RuleBrowserScript: Hold, RuleP2PInvite: Hold, RuleP2PJoin: Hold, RuleP2PInviteWA: Hold,
		RuleKillByName: Allow,
	},
}

// DefaultAt is what rule did, the owner having set nothing, on an install whose applied
// defaults were version: 0 (an install from before the rule switches) = the strict built-in
// outcome; the current version or later = what it does today (its default, or its outcome if
// it is fixed). ok is false for an unknown rule.
func DefaultAt(version int, rule string) (Outcome, bool) {
	r, ok := ruleByID[rule]
	if !ok {
		return "", false
	}
	if version <= 0 || version >= DefaultsVersion {
		if r.Fixed || version <= 0 {
			return r.Outcome, true
		}
		return r.Default, true
	}
	for v := version; v < DefaultsVersion; v++ {
		if o, ok := earlierDefaults[v][rule]; ok {
			return o, true
		}
	}
	if r.Fixed {
		return r.Outcome, true
	}
	return r.Default, true
}

// HadSwitch reports whether rule, fixed today, had a switch on an install whose applied
// defaults were version: then an owner's entry for it in RuleStates was real, and the upgrade
// says what became of it.
func HadSwitch(version int, rule string) bool {
	return version == 1 && rule == RuleKillByName
}

// RuleChange is one flip of a rule's switch (or the defaults applied at an upgrade: By "tmx").
type RuleChange struct {
	Rule string  `json:"rule"`
	Old  Outcome `json:"old"`
	New  Outcome `json:"new"`
	At   string  `json:"at"` // RFC 3339
	By   string  `json:"by"` // "owner" | "tmx"
}

// LegacyMasterSwitch is the RuleChange.Rule of the one log entry written when an install that
// had the removed on/off switch off (or off for some agents) moves to defaults version 2. Its
// Old ("off", "off for worker") and New ("always on") are words, not rule states.
const LegacyMasterSwitch = "master-switch"

// RuleLogMax is how many changes the log keeps.
const RuleLogMax = 200

// Effective is the state of every switchable rule: the defaults with the owner's switches on top.
func (s Settings) Effective() map[string]Outcome {
	m := DefaultStates()
	for id, o := range s.RuleStates {
		if r, ok := ruleByID[id]; ok && !r.Fixed && o.valid() {
			m[id] = o
		}
	}
	return m
}

// AlwaysAllow is one standing allow: a hold of rule Rule for exactly Command (the whole
// command, or the file path for file tools) through Tool runs without asking again, for agents
// of profile Profile ("" = any agent). Blocks are never allowed; the owner gate's own
// confirmations are not guard holds and never remembered.
type AlwaysAllow struct {
	ID       string `json:"id"`
	Rule     string `json:"rule"`
	RuleName string `json:"rule_name"`
	Tool     string `json:"tool"`
	Command  string `json:"command"`
	Profile  string `json:"profile"`        // the agent's profile (or "level:<level>" without one); "" = any agent
	Cwd      string `json:"cwd,omitempty"`  // the folder it ran in: relative paths mean the same files only there
	From     string `json:"from,omitempty"` // the agent whose hold it was answered on
	Created  string `json:"created"`        // RFC 3339
}

// Covers reports whether a matches a hold of rule for command via tool, run in cwd by an agent
// of profile. Exact strings only: a different command, even one that differs by a space, or the
// same one in another folder, asks again.
func (a AlwaysAllow) Covers(rule, tool, command, cwd, profile string) bool {
	return a.Rule == rule && a.Tool == tool && a.Command == command && command != "" && a.Cwd == cwd &&
		(a.Profile == "" || a.Profile == profile)
}

// RunTimeDependent reports whether command can mean different things each time it runs: shell or
// environment variables ($x, ${x}, $env:X, %X%, !X!), subshells and backticks, wildcards. Such a
// hold is never remembered: "Allow always" on `Remove-Item -Recurse $target` would cover any
// $target later.
func RunTimeDependent(command string) bool {
	if strings.ContainsAny(command, "$`*?") {
		return true
	}
	for _, mark := range []byte{'%', '!'} {
		if i := strings.IndexByte(command, mark); i >= 0 && strings.IndexByte(command[i+1:], mark) > 0 {
			return true // %VAR% (cmd) or !VAR! (delayed expansion)
		}
	}
	return false
}

// DefaultSettings: every rule at its default, nothing remembered.
func DefaultSettings() Settings { return Settings{} }

// Weaker reports whether next protects less than s: that change needs the owner's confirmation.
// The rule switches have their own owner-gated route; what is left here is browser automation.
func (s Settings) Weaker(next Settings) bool {
	return !s.AllowBrowserAutomation && next.AllowBrowserAutomation
}

// Environment the daemon gives a guarded agent (inherited by its hook).
const (
	EnvFolder    = "TMXG_GUARD_FOLDER"    // the agent's own folder
	EnvWorkspace = "TMXG_GUARD_WORKSPACE" // the workspace
	EnvState     = "TMXG_GUARD_STATE"     // tmx's state directory
)

// HoldWait is how long a held call waits for the owner before it is denied.
const HoldWait = 10 * time.Minute

// Matcher is the Claude Code hook matcher: the tools the guard looks at.
const Matcher = ".*"

// Check classifies one tool call.
func Check(ctx Context, call Call) Verdict {
	// Only this exact MCP server bypasses the browser tool-name rule.
	if !strings.HasPrefix(call.Tool, "mcp__agent-browser-guard__") {
		if v := checkBrowser(ctx, call.Tool); v.Outcome == Block {
			return v
		}
	}
	if call.Cwd != "" {
		ctx.Cwd = call.Cwd
	}
	if ctx.Cwd == "" {
		ctx.Cwd = ctx.Own
	}
	var in map[string]any
	_ = json.Unmarshal(call.Input, &in)
	str := func(k string) string {
		s, _ := in[k].(string)
		return s
	}
	switch call.Tool {
	case "tmx_exec":
		var exec struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		}
		if err := json.Unmarshal(call.Input, &exec); err != nil || strings.TrimSpace(exec.Command) == "" {
			return Verdict{Outcome: Block, Rule: RuleTmxState, Detail: "invalid guarded executable request"}
		}
		return CheckExec(ctx, exec.Command, exec.Args)
	case "Bash":
		return CheckCommand(ctx, ShellPOSIX, str("command"))
	case "PowerShell":
		return CheckCommand(ctx, ShellPowerShell, str("command"))
	case "Write", "Edit", "MultiEdit":
		if p := str("file_path"); startupPlace.MatchString(p) && shutdownWord.MatchString(string(call.Input)) {
			return ruleVerdict(RuleShutdownStart, "writes a shutdown or restart into "+p+", which runs at startup or logon")
		}
		if p := str("file_path"); p != "" {
			a := newAnalyzer(ctx)
			if t := a.resolve(p); a.claudeSettingsFile(t.p) && touchesGuardHook(call.Tool, call.Input) {
				return ruleVerdict(RuleClaudeHooks, "changes the hooks in "+p)
			}
		}
		return checkFile(ctx, str("file_path"), true)
	case "NotebookEdit":
		if p := str("notebook_path"); startupPlace.MatchString(p) && shutdownWord.MatchString(string(call.Input)) {
			return ruleVerdict(RuleShutdownStart, "writes a shutdown or restart into "+p+", which runs at startup or logon")
		}
		return checkFile(ctx, str("notebook_path"), true)
	case "Read":
		return checkFile(ctx, str("file_path"), false)
	case "Grep":
		return checkFile(ctx, str("path"), false)
	}
	return Verdict{Outcome: Allow}
}

// Shell is the syntax a command is written in.
type Shell int

const (
	ShellPOSIX      Shell = iota // sh, bash, zsh (also Git Bash on Windows)
	ShellPowerShell              // Windows PowerShell and pwsh
	ShellCmd                     // cmd.exe (inside cmd /c ...)
)

// CheckExec classifies an executable and literal arguments without inventing
// shell expansion. Interpreter arguments are analyzed by the existing handlers.
func CheckExec(ctx Context, executable string, argv []string) Verdict {
	a := newAnalyzer(ctx)
	values := append([]string{executable}, argv...)
	args := make([]arg, len(values))
	for i, value := range values {
		args[i] = arg{s: value, raw: value}
	}
	raw := strings.Join(values, " ")
	sh := ShellPOSIX
	if ctx.OS == "windows" {
		sh = ShellPowerShell
	}
	a.dispatch(args, raw, sh, nil)
	if shutdownAtStartup(raw) {
		a.add(RuleShutdownStart, "sets a shutdown or restart to run at startup or logon", raw)
	}
	v := a.verdict()
	if browser := checkBrowser(ctx, raw); browser.Outcome.rank() > v.Outcome.rank() {
		return browser
	}
	return v
}

// CheckCommand classifies a shell command line.
func CheckCommand(ctx Context, sh Shell, command string) Verdict {
	if strings.TrimSpace(command) == "" {
		return Verdict{Outcome: Allow}
	}
	a := newAnalyzer(ctx)
	a.script(command, sh)
	if shutdownAtStartup(command) {
		a.add(RuleShutdownStart, "sets a shutdown or restart to run at startup or logon", command)
	}
	v := a.verdict()
	if v.Outcome == Block {
		return v
	}
	if browser := checkBrowser(ctx, commandText(command, sh)); browser.Outcome.rank() > v.Outcome.rank() {
		return browser
	}
	return v
}

func checkFile(ctx Context, p string, write bool) Verdict {
	if p == "" {
		return Verdict{Outcome: Allow}
	}
	a := newAnalyzer(ctx)
	a.fileTool = true
	t := a.resolve(p)
	match := p
	if write {
		a.write(t, match)
	} else {
		a.read(t, match)
	}
	return a.verdict()
}
