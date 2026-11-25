package fleet

import (
	"os"
	"path/filepath"
	"strings"
)

// The project's CLAUDE.md. tmx starts Claude without any setting source (--setting-sources
// ""): the owner's ~/.claude hooks and permissions must not reach agents. That also switches
// off Claude Code's own loading of the folder's CLAUDE.md, so an agent started in a project
// did not know the project's rules.
//
// Claude Code loads CLAUDE.md by itself only together with the "project" setting source. That
// source is NEVER used here (security review, 17 Nov), because it brings much more than the
// file:
//   - the folder's .claude/settings.json: its hooks run commands, its permission rules allow
//     tools without the owner, and disableAllHooks there switches tmx's guard off;
//   - the folder's .claude skills, commands and agents, whose frontmatter can carry hooks,
//     allowed-tools, a permission mode and MCP servers of their own, outside the guard;
//   - and Claude Code reloads settings while it runs: a .claude/settings.json written in the
//     middle of a session (by the agent itself, or by anything in the folder) would load its
//     hooks and permissions at once. Checking the folder at start cannot rule that out.
// So the agent gets the project's rules the plain way: its instructions name the CLAUDE.md
// files of the working folder and its parents and tell it to read them before its first step.
// Nothing of the folder's settings, skills, commands or agents is applied. The limit: this
// depends on the agent reading the files; imports inside a CLAUDE.md are followed only if the
// agent follows them.

// projectMemory is what a Claude start in cwd tells the agent about the project's CLAUDE.md.
type projectMemory struct {
	// Files are the CLAUDE.md files of cwd and its parent folders, outermost first.
	Files []string
}

// projectMemoryFor looks at cwd and its parents, as Claude Code reads CLAUDE.md files up the
// tree.
func projectMemoryFor(cwd string) projectMemory {
	var m projectMemory
	dir := filepath.Clean(cwd)
	for range 64 { // a depth no real path reaches; guards against a path that never ends
		for _, name := range []string{"CLAUDE.md", filepath.Join(".claude", "CLAUDE.md")} {
			p := filepath.Join(dir, name)
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Size() > 0 {
				m.Files = append([]string{p}, m.Files...)
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return m
}

// prompt is the line for the agent's instructions, or "" when the folder has no CLAUDE.md.
func (m projectMemory) prompt() string {
	if len(m.Files) == 0 {
		return ""
	}
	return "This project has its own rules in " + strings.Join(m.Files, " and ") +
		". They were not loaded for you: read them before your first step and follow them like your brief."
}
