package fleet

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/harness"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
)

// TypingQuiet protects agent-driven raw terminal controls, not message delivery.
const TypingQuiet = 20 * time.Second

// defaultModeLocked is the mode of a launch that names none: the harness's own default (chat for
// Claude and Codex), except under the classic UI profile, where a Claude agent starts in Claude
// Code's own screen as every Python tmx agent did (the owner, 17 Nov 2026; DESIGN.md "Classic UI
// profile"). Codex stays in chat (its screen's conversation is not followed), and a daemon
// without a terminal runner keeps the harness default. An explicit mode always wins, and
// agents that exist keep the mode they were launched in.
func (f *Fleet) defaultModeLocked(info harness.Info) model.Mode {
	if f.opts.UIProfile == config.UIProfileClassic && info.Name == model.HarnessClaude && info.Supports(model.ModeTerminal) {
		if _, ok := f.opts.Runners[model.ModeTerminal]; ok {
			return model.ModeTerminal
		}
	}
	return info.DefaultMode()
}

// harnessModeLocked resolves a launch's harness and mode: the harness must be known, run in
// that mode, and this daemon must have a runner that takes it.
func (f *Fleet) harnessModeLocked(h model.Harness, m model.Mode) (model.Harness, model.Mode, error) {
	if h == "" {
		h = model.HarnessClaude
	}
	info, ok := harness.Get(h)
	if !ok {
		return "", "", errf(KindInvalid, "unknown harness %q", h)
	}
	if m == "" {
		m = f.defaultModeLocked(info)
	}
	if m != model.ModeChat && m != model.ModeTerminal {
		return "", "", errf(KindInvalid, "unknown mode %q", m)
	}
	if !info.Supports(m) {
		if h == model.HarnessShell {
			return "", "", errf(KindInvalid, "a shell runs only in terminal mode")
		}
		return "", "", errf(KindInvalid, "%s runs only in %s mode", info.Label, info.DefaultMode())
	}
	run, ok := f.opts.Runners[m]
	if !ok {
		return "", "", errf(KindInvalid, "this daemon has no runner for %s mode", m)
	}
	if s, ok := run.(runner.Supporter); ok && !s.Supports(h) {
		return "", "", errf(KindInvalid, "this daemon cannot run %s in %s mode", info.Label, m)
	}
	return h, m, nil
}

// Claude workers use the profile's model and allowlist. Fae checks the selected model
// against Ollama at start. Other harnesses use their
// own default unless a model is requested. The owner may pick any for those harnesses.
// A shell has no model.
func (f *Fleet) pickHarnessModelLocked(h model.Harness, prof model.Profile, requested string, byOwner bool) (string, error) {
	if prof.Provider == model.FaeProvider {
		if requested == "" {
			requested = prof.Model
		}
		if _, ok := model.FaeModelName(requested); !ok {
			return "", errf(KindInvalid, "model %s must use the fae/ provider", requested)
		}
		return requested, nil
	}
	if h == model.HarnessClaude {
		return pickModel(prof, requested)
	}
	if h == model.HarnessShell {
		return "", nil
	}
	if requested == "" || byOwner {
		return requested, nil
	}
	if !slices.Contains(prof.AllowedModels, requested) {
		return "", errf(KindForbidden, "model %s is not allowed for the %s profile (allowed: %s; or leave it out for the harness's default)",
			requested, prof.Slug, strings.Join(prof.AllowedModels, ", "))
	}
	return requested, nil
}

// harnessProfileOK refuses a profile the harness cannot honour: a read-only profile on pi,
// which has no permission controls at all (Codex runs it in its read-only sandbox, or on
// Windows unsandboxed with untrusted approvals; opencode with edit and bash denied).
func harnessProfileOK(h model.Harness, prof model.Profile) error {
	if prof.ReadOnly && (h == model.HarnessPi || h == "agy" || h == "antigravity") {
		return errf(KindInvalid, "the %s profile is read-only and %s cannot be limited to reading; pick another harness", prof.Slug, h)
	}
	return nil
}

// harnessSpecLocked completes the spec of an agent on a harness other than Claude. The
// profile gives the permission mode (a read-only profile runs as "plan": Codex's read-only
// sandbox on macOS and Linux, untrusted approvals on Windows where Codex has no sandbox;
// opencode with edits and shell denied) and the instructions; its Claude tool lists and the
// guard's hook do not apply (see DESIGN.md "Harnesses").
func (f *Fleet) harnessSpecLocked(r *agentRec, spec runner.Spec) runner.Spec {
	a := r.a
	if a.Harness == model.HarnessShell {
		return spec
	}
	prof := f.profileForLocked(a)
	spec.PermissionMode = prof.PermissionMode
	if prof.ReadOnly {
		spec.PermissionMode = "plan"
	}
	spec.SessionID = a.SessionID
	switch a.Harness {
	case model.HarnessCodex:
		spec.CodexFullAccess = a.CodexFullAccess && !prof.ReadOnly
		spec.MCP = &runner.MCPServer{Command: f.opts.Executable, Args: []string{"mcp"}, EnvKeys: []string{EnvURL, EnvAgent, EnvToken}}
	case model.HarnessOpenCode:
		spec.Provider = prof.Provider
		if spec.Provider == model.FaeProvider {
			spec.OpenCodeDataDir = filepath.Join(f.opts.Paths.AgentDir(a.ID), "opencode-data")
			spec.FaeRuntimeContextCap = f.opts.FaeRuntimeContextCap
		}
		// opencode 1.19 refuses `-s <id>` for a session it did not create ("Session not found",
		// exit 1), so only a conversation that exists (confirmed) is resumed by id. A new one
		// starts without -s and the runner reads its id back from opencode's database.
		spec.SessionID = ""
		if r.sessionConfirmed {
			spec.SessionID = r.a.SessionID
		}
		spec.MCP = &runner.MCPServer{Command: f.opts.Executable, Args: []string{"mcp"}, EnvKeys: []string{EnvURL, EnvAgent, EnvToken}}
	case model.HarnessGemini, "agy", "antigravity":
		spec.AgyFullAccess = a.AgyFullAccess && !prof.ReadOnly
		spec.MCP = &runner.MCPServer{Command: f.opts.Executable, Args: []string{"mcp"}, EnvKeys: []string{EnvURL, EnvAgent, EnvToken}}
	case model.HarnessPi: // pi takes the id of a new conversation up front, like Claude
		spec.MCP = &runner.MCPServer{Command: f.opts.Executable, Args: []string{"mcp"}, EnvKeys: []string{EnvURL, EnvAgent, EnvToken}}
		if r.a.SessionID == "" {
			r.a.SessionID = harness.NewPiSession()
		}
		spec.SessionID = r.a.SessionID
	}
	return spec
}

// instructionsEvery is how often a harness without a system prompt is reminded of tmx's
// instructions (with its next prompt or mail) while it works.
const instructionsEvery = 4 * time.Hour

// withInstructions puts tmx's instructions in front of a prompt for a harness that takes none
// at launch (opencode, pi): a new conversation's first message, the first after a resume, and
// one every instructionsEvery.
func (f *Fleet) withInstructions(id, prompt string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.agents[id]
	if !ok {
		return prompt
	}
	return f.instructionsLocked(r) + prompt
}

// instructionsLocked is the "[tmx instructions]" block to put before what is sent to r now,
// or "" (not due yet); giving it clears the due flag. A harness that takes tmx's instructions
// at launch gets only the short note of a resumed start (resumedNoteLocked).
func (f *Fleet) instructionsLocked(r *agentRec) string {
	if r.a.Harness == model.HarnessShell {
		return ""
	}
	info, ok := harness.Get(r.a.Harness)
	if !ok {
		return ""
	}
	if info.SystemPrompt {
		if !r.instructionsDue {
			return ""
		}
		r.instructionsDue, r.instructionsAt = false, time.Now()
		return f.resumedNoteLocked(r.a)
	}
	if r.sessionConfirmed && !r.instructionsDue && time.Since(r.instructionsAt) < instructionsEvery {
		return ""
	}
	r.instructionsDue, r.instructionsAt = false, time.Now()
	return "[tmx instructions]\n" + f.systemPromptLocked(r.a) + "\n\n"
}

// resumedNoteLocked is what an agent whose harness takes tmx's instructions at launch (Claude,
// Codex) is told with the first message after a start on its EARLIER conversation: its whole
// instructions as they are now (who it is, its project and lead, its brief, tmx's rules for
// agents) and, for Claude, where the project's CLAUDE.md is.
//
// The launch instructions are passed again at every start, but they do not reach a
// conversation that already exists: two live checks on hosta-go (17 Nov; an imported agent
// and one launched by tmx, both started again on their conversations) found no per-agent text
// in the agent's system instructions at all, only the description of tmx's tools. So the
// instructions are also said in the conversation itself, once per start, in front of the next
// prompt or mail. The cost is that text in the conversation at every restart; an agent that
// does not know who it is, whom it answers to and what it may do costs more.
func (f *Fleet) resumedNoteLocked(a model.Agent) string {
	text := f.systemPromptLocked(a)
	if a.Harness == model.HarnessClaude || a.Harness == "" {
		if p := projectMemoryFor(a.Cwd).prompt(); p != "" {
			text += "\n" + p
		}
	}
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return "[tmx instructions: you were started again on your earlier conversation; these are your instructions as they are now]\n" + text + "\n\n"
}

// typedTerminal identifies native harness composers. Mail is sent regardless of state.
func typedTerminal(a model.Agent) bool {
	return a.Mode == model.ModeTerminal && a.Harness != model.HarnessShell
}

// Harnesses lists the harnesses and whether each is installed here (GET /api/harnesses).
func (f *Fleet) Harnesses() []harness.Status {
	out := harness.Statuses()
	for i := range out {
		if out[i].Name == model.HarnessClaude && f.opts.ClaudeCommand != "" {
			out[i].Installed, out[i].Path = true, f.opts.ClaudeCommand
		}
	}
	return out
}

// claudeConversationExists: Claude has a transcript of the agent's conversation (resuming
// it with --session-id would fail: "Session ID … is already in use"). root is the agents'
// CLAUDE_CONFIG_DIR.
func claudeConversationExists(root string, a model.Agent) bool {
	return harness.ClaudeTranscriptExistsIn(root, a.Cwd, a.SessionID)
}

// carryClaudeTranscript copies an agent's conversation that a daemon before the isolated
// CLAUDE_CONFIG_DIR left in ~/.claude/projects into root, so it can still be resumed. Copied,
// not moved: the owner's folder is left as it was.
func carryClaudeTranscript(root string, a model.Agent) {
	if a.SessionID == "" || claudeConversationExists(root, a) {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil || !harness.ClaudeTranscriptExists(home, a.Cwd, a.SessionID) {
		return
	}
	b, err := os.ReadFile(filepath.Join(harness.ClaudeProjectDir(home, a.Cwd), a.SessionID+".jsonl"))
	if err != nil {
		return
	}
	dst := harness.ClaudeProjectDirIn(root, a.Cwd)
	if os.MkdirAll(dst, 0o700) == nil {
		_ = config.WriteFileAtomic(filepath.Join(dst, a.SessionID+".jsonl"), b, 0o600)
	}
}
