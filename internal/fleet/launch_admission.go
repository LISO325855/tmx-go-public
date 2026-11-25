package fleet

import (
	"runtime"

	"github.com/blackcode-ch/tmx-go/internal/harness"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// Overridden only by fake-runner tests of platform-independent fleet behavior.
var launchGOOS = runtime.GOOS

// launchAdmissionLocked is shared by creation, adoption, restart, resume, and
// the final spawn path. Keep it free of filesystem/store/process mutations.
func (f *Fleet) launchAdmissionLocked(a model.Agent) error {
	// A shell is exempt here: the owner's terminal must remain usable to recover from agent
	// policy failures. A shell an agent launches or starts is admitted where the agent acts
	// (agentShellAdmissionLocked): this function is also the daemon's own start path, which
	// cannot tell whose shell it is.
	if a.Harness == model.HarnessShell {
		return nil
	}
	if f.opts.Elevated() {
		return errf(KindForbidden, "refusing to launch from an elevated process")
	}
	if err := harness.ValidateLaunchCwd(a.Cwd); err != nil {
		return errf(KindForbidden, "%s", err)
	}
	// The command guard never refuses a launch (the owner, 17 Nov): an agent the guard cannot
	// check starts with a warning in its timeline (guardWarningLocked, written by
	// guardSpecLocked). What refuses here is the machine's own safety: an elevated daemon and
	// an unsafe working folder.
	return nil
}

// guardWarning says why the command guard does not check an agent.
type guardWarning struct {
	Reason string `json:"reason"`          // guardWarnHarness | guardWarnHooksDisabled
	Where  string `json:"where,omitempty"` // the settings file that sets disableAllHooks
	Text   string `json:"-"`
}

// Reasons of a guardWarning.
const (
	guardWarnHarness       = "harness"        // no guard adapter for this harness (or this mode of it)
	guardWarnHooksDisabled = "hooks_disabled" // a Claude settings file switches every hook off
)

// guardWarningLocked reports whether a would run without the command guard, and why (nil = the
// guard checks it). It reads settings files and changes nothing; the launch goes ahead either way.
func (f *Fleet) guardWarningLocked(a model.Agent) *guardWarning {
	if a.Harness == model.HarnessShell {
		return nil // the owner's terminal, not an agent
	}
	if a.Harness != model.HarnessClaude {
		if f.codexParityEligibleLocked(a) {
			return nil
		}
		why := "tmx has no command guard for the " + string(a.Harness) + " harness"
		if a.Harness == model.HarnessCodex {
			switch {
			case launchGOOS != "windows":
				why = "tmx's command guard for Codex exists on Windows only"
			case a.CodexFullAccess:
				why = "tmx's command guard for Codex does not cover full-access mode"
			case f.profileForLocked(a).ReadOnly:
				why = "tmx's command guard for Codex does not cover a read-only profile"
			default:
				why = "tmx's command guard for Codex could not be set up (the tmx executable is unknown)"
			}
		}
		return &guardWarning{Reason: guardWarnHarness,
			Text: "This agent runs without the command guard: " + why + ". Nothing it runs is checked, blocked or held for you."}
	}
	// o4 / tmx-lead, 3 Nov: also the Claude config folders the launch uses (the daemon's own
	// CLAUDE_CONFIG_DIR; per-account folders join via claudeConfigDirsLocked).
	if where := hooksDisabledIn(a.Cwd, f.claudeConfigDirsLocked(a)...); where != "" {
		return &guardWarning{Reason: guardWarnHooksDisabled, Where: where,
			Text: "This agent runs without the command guard: " + where + " sets disableAllHooks, which switches off every Claude Code hook, the guard's included. Nothing it runs is checked, blocked or held for you; Claude's own permission prompts stay on. Remove that setting, then restart the agent."}
	}
	return nil
}

// claudeConfigDirsLocked: the CLAUDE_CONFIG_DIR folders a Claude launch of a may read settings
// from: its account's home (#236), and the daemon's own folder (the default account; checked
// in any case, it is cheap).
func (f *Fleet) claudeConfigDirsLocked(a model.Agent) []string {
	dirs := []string{f.agentClaudeConfigDirLocked(a)}
	if d := f.opts.Paths.ClaudeConfigDir(); d != dirs[0] {
		dirs = append(dirs, d)
	}
	return dirs
}

func (f *Fleet) codexParityEligibleLocked(a model.Agent) bool {
	return launchGOOS == "windows" && a.Harness == model.HarnessCodex && !a.CodexFullAccess && !f.profileForLocked(a).ReadOnly && f.opts.Executable != ""
}
