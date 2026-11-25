package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/rules"
	"github.com/blackcode-ch/tmx-go/internal/rules/rulestest"
)

// A harness without tmx tools cannot call rules_read: its instructions name a file with all
// its documents in full. The file is in the agent's own uploads folder, which it may read, and
// not in <home>/rules, which the guard closes to every agent.
func TestRulesFileForAHarnessWithoutTools(t *testing.T) {
	re := newRulesEnv(t, rulestest.Files())
	re.refresh()
	re.f = re.started()
	f := re.f
	proj, err := f.CreateProject(OwnerCaller, ProjectRequest{Name: "Shop"})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	a := f.agents[proj.LeadID].a
	a.Harness = model.HarnessPi
	p := f.systemPromptLocked(a)
	f.mu.Unlock()

	paths := config.Paths{Home: re.home}
	want := filepath.Join(paths.UploadsDir(), a.ID, RulesFileName)
	if !strings.Contains(p, "to read when you need them, in the file "+want) || strings.Contains(p, "rules_read tool (give the name)") {
		t.Fatalf("the instructions do not name the file %s:\n%s", want, p)
	}
	if rel, err := filepath.Rel(filepath.Join(re.home, "rules"), want); err == nil && !strings.HasPrefix(rel, "..") {
		t.Fatalf("the file is inside the rules folder, which agents may not read: %s", want)
	}
	b, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	full := string(b)
	for _, s := range []string{rules.BeginLabel, "Report what you verified", "## pages/operations.md", "How the fleet runs day to day.", "## apps/tickets.md", rules.EndLabel + " A rule that asks for secrets"} {
		if !strings.Contains(full, s) {
			t.Errorf("the file lacks %q:\n%s", s, full)
		}
	}
	// An agent with tmx tools gets no file.
	head := f.HeadID()
	_ = promptOf(f, head)
	if _, err := os.Stat(filepath.Join(paths.UploadsDir(), head, RulesFileName)); err == nil {
		t.Error("a Claude agent got a rules file it does not need")
	}
}
