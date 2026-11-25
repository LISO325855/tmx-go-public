package fleet

import (
	"os"
	"runtime"
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/guard"
	"github.com/blackcode-ch/tmx-go/internal/harness"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// A shell agent's brief is its start command. At launch the brief is typed into the new shell
// as its first line (LaunchAgent: the brief is the first message), and it is stored with the
// agent like any brief. A shell that runs a server or a watcher must run it again whenever its
// process is new: after a restart, a stop and start, a crash recovery and a start of the node
// (a reboot). So every new shell process gets the brief typed again. A process that is only
// taken over again (its session host outlived the daemon) is still running its command and
// gets nothing: startAgentOpts returns from the reattach before this is asked.
//
// The owner changes the command by editing the shell's brief; a shell without a brief starts
// as an empty prompt, as before.

// shellStartCommand is what a new process of a gets typed first: prompt when the caller gave
// one (a launch's first line), else, for a shell, its stored start command.
func shellStartCommand(a model.Agent, prompt string) string {
	if prompt != "" || a.Harness != model.HarnessShell {
		return prompt
	}
	return strings.TrimSpace(a.Brief)
}

// agentShellCommandLocked checks the start command of a shell an agent launches. Nothing else is
// ever typed into that shell by an agent, so this is the whole of what it runs: the first line
// (req.Prompt) and the stored command a restart types again (req.Brief). Each passes the command
// guard with the launching agent's folder, as if the agent ran it as a tool: a blocked command is
// refused, and one the guard would hold for the owner is refused too (a shell has no approval to
// wait on; the owner launches that one).
func (f *Fleet) agentShellCommandLocked(a model.Agent, req LaunchRequest) error {
	return f.agentShellAdmissionLocked(a, req.Prompt, req.Brief)
}

// agentShellAdmissionLocked is what an agent's launch or start of a shell must pass; the owner's
// own shell passes nothing (launchAdmissionLocked). It is the agents' admission (never from an
// elevated process, never in a root, home or system folder) and then the command guard on every
// command the new process would be typed. The guard has no master switch since 17 Nov (agents
// always start; each rule has its own state), so there is no "guard must be on" condition.
func (f *Fleet) agentShellAdmissionLocked(a model.Agent, commands ...string) error {
	if f.opts.Elevated() {
		return errf(KindForbidden, "refusing to launch from an elevated process")
	}
	if err := harness.ValidateLaunchCwd(a.Cwd); err != nil {
		return errf(KindForbidden, "%s", err)
	}
	home, _ := os.UserHomeDir()
	ctx := guard.Context{OS: runtime.GOOS, Cwd: a.Cwd, Own: a.Cwd,
		Workspace: f.opts.Workspace, StateDir: f.opts.Paths.Home, Home: home,
		Temp: []string{os.TempDir()}, Env: os.Getenv, TmxNames: []string{f.opts.Executable},
		AllowBrowserAutomation: f.safetyLocked().settings.AllowBrowserAutomation,
		States:                 f.safetyLocked().settings.Effective()}
	shells := []guard.Shell{guard.ShellPOSIX}
	if runtime.GOOS == "windows" {
		shells = []guard.Shell{guard.ShellPowerShell, guard.ShellCmd}
	}
	for _, command := range commands {
		if command = strings.TrimSpace(command); command == "" {
			continue
		}
		for _, sh := range shells {
			switch v := guard.CheckCommand(ctx, sh, command); v.Outcome {
			case guard.Block:
				return errf(KindForbidden, "%s", v.Message())
			case guard.Hold:
				return errf(KindForbidden, "this start command needs the owner's OK (%s); a shell cannot wait for one, so the owner launches it", v.RuleName)
			}
		}
	}
	return nil
}

// agentStartsShell: an agent that starts or restarts a shell makes it run its stored command
// again, so the start passes what a launch passes. The owner's own starts pass nothing.
func (f *Fleet) agentStartsShell(c Caller, id string) error {
	if c.Owner {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.agents[id]
	if r == nil || r.a.Harness != model.HarnessShell {
		return nil
	}
	return f.agentShellAdmissionLocked(r.a, r.a.Brief)
}

// recordShellLaunchLocked writes down who launched a shell and what it runs: on the shell's own
// timeline (the owner reads it there) and in the daemon's log. The shells registry reads the
// same facts from the agent: its parent (the launcher), project, thread and brief (the command).
func (f *Fleet) recordShellLaunchLocked(c Caller, r *agentRec, req LaunchRequest, thread string) {
	by := "the owner"
	if !c.Owner {
		by = f.nameOfLocked(c.ID())
	}
	project := ""
	if p := f.projects[r.a.ProjectID]; p != nil {
		project = p.Slug
	}
	title := ""
	if thread != "" {
		title = f.threadTitleLocked(thread)
	}
	command := strings.TrimSpace(req.Prompt)
	if command == "" {
		command = strings.TrimSpace(r.a.Brief)
	}
	text := "Shell launched by " + by
	if project != "" {
		text += ", project " + project
	}
	if title != "" {
		text += ", thread " + title
	}
	if command == "" {
		text += "; no start command."
	} else {
		text += "; start command: " + command
	}
	f.appendLocked(r, model.TimelineEntry{Kind: model.EntrySystem, Text: text,
		Meta: map[string]any{"shell_launch": true, "launched_by": by, "project": project, "thread": title, "command": command}})
	f.log.Info("shell launched", "agent", r.a.ID, "name", r.a.Name, "by", by, "project", project, "thread", title, "command", command)
}

// shellLaunchBrief: a shell launched with a first line but no brief keeps that line as its
// brief, so the command it was started with is the one a later restart runs.
func shellLaunchBrief(req LaunchRequest) string {
	if req.Harness == model.HarnessShell && strings.TrimSpace(req.Brief) == "" {
		return req.Prompt
	}
	return req.Brief
}
