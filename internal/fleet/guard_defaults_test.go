package fleet

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/guard"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// logOf returns the rule log's entries for one rule.
func logOf(s guard.Settings, rule string) []guard.RuleChange {
	var out []guard.RuleChange
	for _, ch := range s.RuleLog {
		if ch.Rule == rule {
			out = append(out, ch)
		}
	}
	return out
}

// migrated starts a daemon on the stored guard settings g and returns what it then holds and
// what it wrote to config.json (through Options.SaveGuard).
func migrated(t *testing.T, g *guard.Settings) (live, stored guard.Settings, f *Fleet) {
	t.Helper()
	e := newEnv(t)
	e.guard = g
	f = e.fleet()
	saves := 0
	f.opts.SaveGuard = func(s guard.Settings) error {
		b, err := json.Marshal(s) // exactly what config.json would hold
		if err != nil {
			return err
		}
		stored = guard.Settings{}
		saves++
		return json.Unmarshal(b, &stored)
	}
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	if saves == 0 && (g == nil || g.DefaultsVersion < guard.DefaultsVersion) {
		t.Fatal("the migration was not saved")
	}
	return f.GuardSettings(), stored, f
}

// v2Defaults is defaults version 2 (the owner, 17 Nov 16:05): Blocked = the four rules that kill
// the machine; administrator rights Held; every other switchable rule Allowed.
func v2Defaults() map[string]guard.Outcome {
	return map[string]guard.Outcome{
		guard.RuleDisk: guard.Block, guard.RuleDeleteRoot: guard.Block, guard.RulePermsRoot: guard.Block, guard.RuleBoot: guard.Block,
		guard.RuleHelloKey: guard.Allow, guard.RuleNetwork: guard.Allow, guard.RuleTmxCLI: guard.Allow,
		guard.RuleDeleteOutside: guard.Allow, guard.RuleElevation: guard.Hold, // the one rule Held by default
		guard.RuleBrowser:  guard.Allow,
		guard.RuleShutdown: guard.Allow, guard.RuleTmxState: guard.Allow, guard.RulePipeShell: guard.Allow,
		guard.RuleWriteOutside: guard.Allow, guard.RuleClaudeConfig: guard.Allow, guard.RuleDesktop: guard.Allow,
		guard.RulePermsOutside: guard.Allow, guard.RuleGitRewrite: guard.Allow,
		guard.RuleBrowserScript: guard.Allow, guard.RuleP2PInvite: guard.Allow, guard.RuleP2PJoin: guard.Allow, guard.RuleP2PInviteWA: guard.Allow,
	}
}

// Defaults version 2 on a fresh install. What differs from the strict built-in outcome is
// logged once, by tmx.
func TestDefaultsV2FreshInstall(t *testing.T) {
	live, stored, f := migrated(t, nil)
	if live.DefaultsVersion != 2 || stored.DefaultsVersion != 2 {
		t.Fatalf("defaults version: live %d, stored %d", live.DefaultsVersion, stored.DefaultsVersion)
	}
	if len(live.RuleStates) != 0 {
		t.Fatalf("the migration wrote rule states (they are the owner's alone): %v", live.RuleStates)
	}
	want := v2Defaults()
	states := f.GuardStates()
	if len(states) != len(want) {
		t.Fatalf("%d rule states, want %d: %v", len(states), len(want), states)
	}
	for id, o := range want {
		if states[id] != o {
			t.Errorf("%s is %s, want %s", id, states[id], o)
		}
		r, _ := guard.RuleByID(id)
		entries := logOf(live, id)
		switch {
		case r.Outcome == o && len(entries) != 0:
			t.Errorf("%s did not change, yet it is logged: %+v", id, entries)
		case r.Outcome != o && (len(entries) != 1 || entries[0].Old != r.Outcome || entries[0].New != o || entries[0].By != "tmx" || entries[0].At == ""):
			t.Errorf("%s changed %s -> %s, log: %+v", id, r.Outcome, o, entries)
		}
	}
	if len(logOf(live, guard.LegacyMasterSwitch)) != 0 {
		t.Error("a fresh install logged a master switch it never had")
	}
	// The never-rules are fixed Blocks, untouched by any default; none of them is logged on a
	// fresh install (kill-by-name was a Block before the switches too).
	for _, r := range guard.FixedRules() {
		if r.Outcome != guard.Block || f.ruleStateOf(r.ID) != guard.Block {
			t.Errorf("fixed rule %s is %s", r.ID, f.ruleStateOf(r.ID))
		}
		if len(logOf(live, r.ID)) != 0 {
			t.Errorf("fixed rule %s is logged: %+v", r.ID, logOf(live, r.ID))
		}
	}
	for _, ch := range live.RuleLog {
		if ch.New == guard.Hold {
			t.Errorf("a default made a rule Held: %+v", ch)
		}
	}
}

// An install on defaults version 1: exactly the rules whose state moved are logged, each with
// its true old value (what version 1 did, not the strict outcome); nothing else is touched.
func TestDefaultsV1ToV2(t *testing.T) {
	before := []guard.RuleChange{{Rule: guard.RuleKillByName, Old: guard.Block, New: guard.Allow, At: "2026-11-03T22:14:00Z", By: "tmx"}}
	live, stored, f := migrated(t, &guard.Settings{DefaultsVersion: 1, RuleLog: before})
	if live.DefaultsVersion != 2 || stored.DefaultsVersion != 2 {
		t.Fatalf("defaults version: live %d, stored %d", live.DefaultsVersion, stored.DefaultsVersion)
	}
	// rule -> old -> new, the whole table of the upgrade.
	moved := map[string][2]guard.Outcome{
		guard.RuleTmxCLI:        {guard.Hold, guard.Allow},
		guard.RuleHelloKey:      {guard.Hold, guard.Allow},
		guard.RuleNetwork:       {guard.Hold, guard.Allow},
		guard.RuleDeleteOutside: {guard.Hold, guard.Allow},
		guard.RulePermsOutside:  {guard.Hold, guard.Allow},
		guard.RuleGitRewrite:    {guard.Hold, guard.Allow},
		guard.RuleBrowserScript: {guard.Hold, guard.Allow},  // was a fixed hold
		guard.RuleP2PInvite:     {guard.Hold, guard.Allow},  // was a fixed hold
		guard.RuleP2PJoin:       {guard.Hold, guard.Allow},  // was a fixed hold
		guard.RuleP2PInviteWA:   {guard.Hold, guard.Allow},  // was a fixed hold
		guard.RuleKillByName:    {guard.Allow, guard.Block}, // lost its switch: a fixed Block
	}
	if len(live.RuleLog) != 1+len(moved) || live.RuleLog[0] != before[0] {
		t.Fatalf("log: %+v", live.RuleLog)
	}
	for _, ch := range live.RuleLog[1:] {
		want, ok := moved[ch.Rule]
		if !ok || ch.Old != want[0] || ch.New != want[1] || ch.By != "tmx" || ch.At == "" {
			t.Errorf("log entry %+v, want %v", ch, want)
		}
		delete(moved, ch.Rule)
	}
	if len(moved) != 0 {
		t.Errorf("not logged: %v", moved)
	}
	states := f.GuardStates()
	for id, o := range v2Defaults() {
		if states[id] != o {
			t.Errorf("%s is %s, want %s", id, states[id], o)
		}
	}
	if len(states) != len(v2Defaults()) || f.ruleStateOf(guard.RuleKillByName) != guard.Block {
		t.Errorf("states: %v", states)
	}
	if len(stored.RuleLog) != len(live.RuleLog) || len(stored.RuleStates) != 0 {
		t.Fatalf("stored: %+v", stored)
	}
	// Once: a second start changes and logs nothing.
	n := len(live.RuleLog)
	f.mu.Lock()
	f.applyRuleDefaultsLocked()
	f.mu.Unlock()
	if got := f.GuardSettings(); len(got.RuleLog) != n {
		t.Fatalf("the defaults were applied twice: %+v", got.RuleLog)
	}
}

// kill-by-name lost its switch. What the owner had set it to on version 1 is logged with his
// value as the old state, and his entry is dropped; it blocks whatever he had chosen.
func TestKillByNameBecomesFixed(t *testing.T) {
	for _, was := range []guard.Outcome{guard.Allow, guard.Hold, guard.Block} {
		live, stored, f := migrated(t, &guard.Settings{DefaultsVersion: 1, RuleStates: map[string]guard.Outcome{guard.RuleKillByName: was, guard.RuleShutdown: guard.Hold}})
		if f.ruleStateOf(guard.RuleKillByName) != guard.Block {
			t.Fatalf("owner had %s: kill-by-name is %s", was, f.ruleStateOf(guard.RuleKillByName))
		}
		if _, ok := live.RuleStates[guard.RuleKillByName]; ok || stored.RuleStates[guard.RuleKillByName] != "" || live.RuleStates[guard.RuleShutdown] != guard.Hold {
			t.Fatalf("owner had %s: rule states %v (stored %v)", was, live.RuleStates, stored.RuleStates)
		}
		entries := logOf(live, guard.RuleKillByName)
		if was == guard.Block {
			if len(entries) != 0 {
				t.Fatalf("it was blocked already, yet logged: %+v", entries)
			}
			continue
		}
		if len(entries) != 1 || entries[0].Old != was || entries[0].New != guard.Block || entries[0].By != "tmx" {
			t.Fatalf("owner had %s: log %+v", was, entries)
		}
		if _, err := f.SetRuleState(OwnerCaller, guard.RuleKillByName, guard.Allow); KindOf(err) != KindInvalid {
			t.Fatalf("kill-by-name still has a switch: %v", err)
		}
	}
}

// Rules the owner set keep their value through the migration, whichever way they point, and
// are not logged as changed by tmx.
func TestDefaultsV2KeepOwnerRules(t *testing.T) {
	owner := map[string]guard.Outcome{
		guard.RuleGitRewrite:   guard.Hold,  // v2 would allow it: he keeps it held
		guard.RuleNetwork:      guard.Hold,  // his access: held because he said so
		guard.RulePermsOutside: guard.Block, // stricter than both defaults
		guard.RuleDisk:         guard.Hold,  // weaker than the default: his call
		guard.RuleElevation:    guard.Allow,
		guard.RuleDesktop:      guard.Hold,
	}
	for _, from := range []int{0, 1} {
		live, stored, f := migrated(t, &guard.Settings{DefaultsVersion: from, RuleStates: owner})
		states := f.GuardStates()
		for id, o := range owner {
			if states[id] != o || live.RuleStates[id] != o || stored.RuleStates[id] != o {
				t.Errorf("from v%d: the owner's %s=%s became %s (stored %s)", from, id, o, states[id], stored.RuleStates[id])
			}
			if len(logOf(live, id)) != 0 {
				t.Errorf("from v%d: the owner's rule %s is logged as changed: %+v", from, id, logOf(live, id))
			}
		}
		if len(live.RuleStates) != len(owner) {
			t.Errorf("from v%d: rule states grew: %v", from, live.RuleStates)
		}
		// Rules he did not set get the v2 default.
		if states[guard.RuleBoot] != guard.Block || states[guard.RuleHelloKey] != guard.Allow || states[guard.RuleDeleteOutside] != guard.Allow || states[guard.RuleWriteOutside] != guard.Allow {
			t.Errorf("from v%d: unset rules: %v", from, states)
		}
		for _, ch := range live.RuleLog {
			if ch.By != "tmx" {
				t.Errorf("from v%d: log entry not by tmx: %+v", from, ch)
			}
		}
	}
}

// Setting a rule to the state it already has makes it the owner's: later defaults leave it.
func TestOwnerPinsARuleAtItsDefault(t *testing.T) {
	_, _, f := migrated(t, nil)
	before := len(f.GuardSettings().RuleLog)
	out, err := f.SetRuleState(OwnerCaller, guard.RuleBoot, guard.Block)
	if err != nil || out.RuleStates[guard.RuleBoot] != guard.Block {
		t.Fatalf("pin: %v %v", err, out.RuleStates)
	}
	if len(out.RuleLog) != before {
		t.Fatalf("a pin that changed nothing was logged: %+v", out.RuleLog)
	}
	if again, _ := f.SetRuleState(OwnerCaller, guard.RuleBoot, guard.Block); len(again.RuleLog) != before {
		t.Fatal("the same state twice was logged")
	}
}

// The removed master switch in a stored config: read once, logged once (only if it protected
// fewer agents than all), never written again, and it stops nothing from starting guarded.
func TestMasterSwitchMigration(t *testing.T) {
	off, on := false, true
	for _, tc := range []struct {
		name string
		g    guard.Settings
		was  string // "" = nothing to log
	}{
		{"switched off", guard.Settings{LegacyEnabled: &off, DefaultsVersion: 1}, "off"},
		{"off wins over the list", guard.Settings{LegacyEnabled: &off, LegacyOffFor: []string{"worker"}}, "off"},
		{"off for some", guard.Settings{LegacyEnabled: &on, LegacyOffFor: []string{"Worker", " lead ", "worker", ""}, DefaultsVersion: 1}, "off for worker, lead"},
		{"on for everyone", guard.Settings{LegacyEnabled: &on, LegacyOffFor: []string{}, DefaultsVersion: 1}, ""},
		{"no switch stored", guard.Settings{DefaultsVersion: 1}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			live, stored, f := migrated(t, &tc.g)
			entries := logOf(live, guard.LegacyMasterSwitch)
			if tc.was == "" && len(entries) != 0 {
				t.Fatalf("logged: %+v", entries)
			}
			if tc.was != "" && (len(entries) != 1 || string(entries[0].Old) != tc.was || entries[0].New != "always on" || entries[0].By != "tmx") {
				t.Fatalf("log: %+v, want old %q", entries, tc.was)
			}
			for name, s := range map[string]guard.Settings{"live": live, "stored": stored} {
				if s.LegacyEnabled != nil || s.LegacyOffFor != nil {
					t.Fatalf("%s settings still carry the switch: %+v", name, s)
				}
			}
			b, _ := json.Marshal(stored)
			var raw map[string]any
			if err := json.Unmarshal(b, &raw); err != nil || raw["enabled"] != nil || raw["off_for"] != nil {
				t.Fatalf("config.json would still hold the switch: %s", b)
			}
			// Every level starts with the hook, whatever the switch said.
			if _, err := f.CreateProject(OwnerCaller, ProjectRequest{Name: "alpha"}); err != nil {
				t.Fatal(err)
			}
			w, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "w1", ProjectID: "alpha", Level: model.LevelWorker})
			if err != nil {
				t.Fatalf("worker launch: %v", err)
			}
			for _, name := range []string{"head", "alpha-lead", w.Name} {
				a := mustAgent(t, f, name)
				if a.State == model.StateError {
					t.Fatalf("%s did not start: %s", name, a.Error)
				}
				if guardWarnings(t, f, a.ID) != 0 {
					t.Fatalf("%s started unguarded", name)
				}
			}
		})
	}
}

// The pairing tools were fixed holds; they follow their switchable rule now. Allowed (the
// default): the call is allowed at once, with a timeline row and no card. Held for me: a
// card. Blocked: refused.
func TestPairingToolsFollowTheirRule(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	head := mustAgent(t, f, "head")
	c := AgentCaller(head.ID)
	for tool, rule := range map[string]string{ToolP2PInvite: guard.RuleP2PInvite, ToolP2PJoin: guard.RuleP2PJoin, ToolP2PInviteWA: guard.RuleP2PInviteWA} {
		ap, err := f.HoldP2P(c, tool, "what it would do")
		if err != nil || ap.State != model.ApprovalAllowed || ap.Answer != AnswerRule || ap.Kind != model.ApprovalGuard {
			t.Fatalf("%s allowed by default: %+v %v", tool, ap, err)
		}
		if pending, _ := f.Approvals(model.ApprovalPending); len(pending) != 0 {
			t.Fatalf("%s: a card was raised: %+v", tool, pending)
		}
		tl, _ := f.Timeline("head", 0, 0)
		if last := tl[len(tl)-1]; last.Kind != model.EntrySystem || last.Tool != tool || last.Meta["guard"] == nil {
			t.Fatalf("%s: no timeline row: %+v", tool, last)
		}
		if got, err := f.WaitHold(t.Context(), c, ap.ID, 0); err != nil || got.State != model.ApprovalAllowed {
			t.Fatalf("%s: the allowed call cannot be redeemed: %+v %v", tool, got, err)
		}
		holdRules(t, f, rule)
		held, err := f.HoldP2P(c, tool, "what it would do")
		if err != nil || held.State != model.ApprovalPending {
			t.Fatalf("%s held: %+v %v", tool, held, err)
		}
		f.CancelHold(c, held.ID)
		if _, err := f.SetRuleState(OwnerCaller, rule, guard.Allow); err != nil {
			t.Fatal(err)
		}
	}
	// The hook route still cannot raise a pairing hold, and a worker cannot pair at all.
	if _, err := f.Hold(c, HoldRequest{Tool: ToolP2PJoin, Command: ToolP2PJoin, Rule: guard.RuleP2PJoin}); KindOf(err) != KindForbidden {
		t.Fatalf("pairing hold through the hook route: %v", err)
	}
}

// A card offers "Allow always" only for a call that can be remembered.
func TestNoAllowAlwaysForRunTimeCommands(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	holdRules(t, f, guard.RuleDeleteOutside)
	head := mustAgent(t, f, "head")
	me := callerFor(t, f, e.chat, head.ID)
	for cmd, noAlways := range map[string]bool{
		"rm -rf /srv/data":   false,
		`rm -rf "$DIR"`:      true,
		"rm -rf /srv/data/*": true,
		"rm -rf `cat list`":  true,
		"rd /s /q %TARGET%":  true,
	} {
		ap, err := f.Hold(me, HoldRequest{Tool: "Bash", Command: cmd, Rule: guard.RuleDeleteOutside, Cwd: "/w"})
		if err != nil {
			t.Fatal(err)
		}
		var info model.GuardHold
		if err := json.Unmarshal(ap.Input, &info); err != nil || info.NoAlways != noAlways {
			t.Errorf("%q: no_always=%v, want %v", cmd, info.NoAlways, noAlways)
		}
		// The card does not offer Always for such a call. Asked all the same (an older page, the
		// API), the owner's decision still resolves the card: allowed this once, nothing
		// remembered, and the answer says so (tmx-lead, 17 Nov: a refused Always after the PIN
		// left the card open and the agent waiting).
		got, err := f.Decide(OwnerCaller, ap.ID, DecideRequest{Decision: DecisionAllowAlways, Scope: "any"})
		if err != nil || got.State != model.ApprovalAllowed || strings.HasPrefix(got.Answer, AllowedOnceNotAlways) != noAlways {
			t.Errorf("%q: Allow always answered %+v %v, the card said no_always=%v", cmd, got, err, noAlways)
		}
		again, err := f.Hold(me, HoldRequest{Tool: "Bash", Command: cmd, Rule: guard.RuleDeleteOutside, Cwd: "/w"})
		if err != nil || (again.State == model.ApprovalPending) != noAlways {
			t.Errorf("%q: asked again -> %s (%v); remembered only when the card offered Always (no_always=%v)", cmd, again.State, err, noAlways)
		}
		f.CancelHold(me, again.ID)
	}
}
