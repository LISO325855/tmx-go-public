package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Which folders get the read-first instruction, and for which files.
func TestProjectMemoryFiles(t *testing.T) {
	root := t.TempDir()

	plain := filepath.Join(root, "plain")
	writeFile(t, filepath.Join(plain, "CLAUDE.md"), "# Plain rules\n")
	m := projectMemoryFor(plain)
	if len(m.Files) != 1 || !strings.Contains(m.prompt(), filepath.Join(plain, "CLAUDE.md")) || !strings.Contains(m.prompt(), "read them before your first step") {
		t.Fatalf("a folder with CLAUDE.md: %+v %q", m, m.prompt())
	}

	// CLAUDE.md of a parent folder and .claude/CLAUDE.md count, outermost first.
	sub := filepath.Join(plain, "pkg", "deep")
	writeFile(t, filepath.Join(sub, ".claude", "CLAUDE.md"), "deep rules")
	if m := projectMemoryFor(sub); len(m.Files) != 2 || m.Files[0] != filepath.Join(plain, "CLAUDE.md") || m.Files[1] != filepath.Join(sub, ".claude", "CLAUDE.md") {
		t.Fatalf("parent and .claude files: %+v", m)
	}

	none := filepath.Join(root, "none")
	writeFile(t, filepath.Join(none, "README.md"), "x")
	if m := projectMemoryFor(none); len(m.Files) != 0 || m.prompt() != "" {
		t.Fatalf("a folder without CLAUDE.md: %+v", m)
	}
	empty := filepath.Join(root, "empty")
	writeFile(t, filepath.Join(empty, "CLAUDE.md"), "")
	if m := projectMemoryFor(empty); len(m.Files) != 0 {
		t.Fatalf("an empty CLAUDE.md: %+v", m)
	}
}

// A real start: Claude is NEVER started with the project setting source, whatever the folder
// holds (its settings, skills, commands and agents would apply, and settings reload while it
// runs); the agent's instructions name the project's CLAUDE.md instead.
func TestClaudeStartNeverLoadsTheProjectSourceAndPointsAtClaudeMd(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	project := workerProject(t, f)

	for name, settings := range map[string]string{
		"rules":   "", // no project settings at all
		"emptyok": "{}",
		"hooked":  `{"permissions":{"allow":["Bash(*)"]},"hooks":{"PreToolUse":[]}}`,
	} {
		dir := filepath.Join(t.TempDir(), name)
		writeFile(t, filepath.Join(dir, "CLAUDE.md"), "Always answer in Swiss German.\n")
		if settings != "" {
			writeFile(t, filepath.Join(dir, ".claude", "settings.json"), settings)
		}
		a, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: project, Name: name, Cwd: dir})
		if err != nil {
			t.Fatal(err)
		}
		spec := e.chat.Last(a.ID).Spec
		if spec.ProjectSource {
			t.Fatalf("%s: Claude was started with the project setting source", name)
		}
		if !strings.Contains(spec.SystemPrompt, filepath.Join(dir, "CLAUDE.md")) || !strings.Contains(spec.SystemPrompt, "read them before your first step") {
			t.Fatalf("%s: the instructions do not point at CLAUDE.md: %q", name, spec.SystemPrompt[max(0, len(spec.SystemPrompt)-300):])
		}
	}

	bare := filepath.Join(t.TempDir(), "bare")
	c, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: project, Name: "bare", Cwd: bare})
	if err != nil {
		t.Fatal(err)
	}
	if spec := e.chat.Last(c.ID).Spec; spec.ProjectSource || strings.Contains(spec.SystemPrompt, "CLAUDE.md") {
		t.Fatalf("a folder without CLAUDE.md changed: source %v", spec.ProjectSource)
	}
}
