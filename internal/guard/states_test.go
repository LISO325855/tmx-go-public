package guard

import "testing"

// The owner's rule switches (3 Nov 22:14): every switchable rule × every state, and the fixed
// carve-outs whatever the switches say. Findings are injected directly: no command strings here
// (those live in switches_cmd_test.go, Linux CI only).

func TestEveryRuleEveryState(t *testing.T) {
	for _, r := range Rules() {
		for _, st := range []Outcome{Block, Hold, Allow} {
			states := DefaultStates()
			states[r.ID] = st
			a := newAnalyzer(Context{States: states})
			a.found = append(a.found, finding{rule: r.ID, detail: "x"})
			v := a.verdict()
			if v.Outcome != st {
				t.Errorf("%s set to %s: got %s", r.ID, st, v.Outcome)
			}
			if st != Allow && v.Rule != r.ID {
				t.Errorf("%s set to %s: verdict names %q", r.ID, st, v.Rule)
			}
		}
	}
}

// Two holds: the non-rememberable one names the verdict, in either order (go-reviewer-o4, #241).
// These rules default to Allowed, so they are switched to Held here to make real ties.
func TestHoldTiePrefersTheNonRememberable(t *testing.T) {
	held := DefaultStates()
	held[RuleWriteOutside], held[RulePermsOutside], held[RuleGitRewrite] = Hold, Hold, Hold
	for _, order := range [][]string{{RuleWriteOutside, RulePermsOutside}, {RulePermsOutside, RuleWriteOutside}} {
		a := newAnalyzer(Context{States: held})
		for _, r := range order {
			a.found = append(a.found, finding{rule: r})
		}
		if v := a.verdict(); v.Outcome != Hold || v.Rule != RulePermsOutside {
			t.Fatalf("%v: got %s/%s, want hold/%s", order, v.Outcome, v.Rule, RulePermsOutside)
		}
	}
	// Two rememberable holds: the first stays.
	a := newAnalyzer(Context{States: held})
	a.found = []finding{{rule: RuleWriteOutside}, {rule: RuleGitRewrite}}
	if v := a.verdict(); v.Outcome != Hold || v.Rule != RuleWriteOutside {
		t.Fatalf("two rememberable holds: %s/%s", v.Outcome, v.Rule)
	}
}

// An allowed rule never hides another rule's block or hold in the same call.
func TestPerFindingNotPerCall(t *testing.T) {
	states := DefaultStates()
	states[RuleShutdown] = Allow
	states[RuleDisk] = Block
	states[RuleElevation] = Hold
	a := newAnalyzer(Context{States: states})
	a.found = []finding{{rule: RuleShutdown}, {rule: RuleElevation}, {rule: RuleDisk}}
	if v := a.verdict(); v.Outcome != Block || v.Rule != RuleDisk {
		t.Fatalf("got %+v, want the disk block", v)
	}
	a.found = []finding{{rule: RuleShutdown}, {rule: RuleElevation}}
	if v := a.verdict(); v.Outcome != Hold || v.Rule != RuleElevation {
		t.Fatalf("got %+v, want the elevation hold", v)
	}
}

// Fixed carve-outs ignore every switch, including one that names them.
func TestFixedRulesIgnoreStates(t *testing.T) {
	for _, r := range FixedRules() {
		states := DefaultStates()
		states[r.ID] = Allow // forged: a fixed rule has no switch
		for id := range states {
			states[id] = Allow
		}
		a := newAnalyzer(Context{States: states})
		a.found = []finding{{rule: r.ID}}
		if v := a.verdict(); v.Outcome != r.Outcome {
			t.Errorf("fixed %s: got %s, want %s", r.ID, v.Outcome, r.Outcome)
		}
		if _, ok := DefaultStates()[r.ID]; ok {
			t.Errorf("fixed %s has a switch", r.ID)
		}
	}
}

// No states (nil) = the strict built-in outcomes. The defaults are version 2 (the owner, 17 Nov):
// every switchable rule is named here, so a new rule cannot get a default nobody decided.
func TestDefaultsAndStrict(t *testing.T) {
	if DefaultsVersion != 2 {
		t.Fatalf("defaults version %d: decide the new defaults here and in earlierDefaults", DefaultsVersion)
	}
	want := map[string]Outcome{
		// Blocked: what kills the machine. Nothing else.
		RuleDisk: Block, RuleDeleteRoot: Block, RulePermsRoot: Block, RuleBoot: Block,
		// Allowed: every other rule. Nothing is Held by default (the owner, 17 Nov 16:05).
		RuleHelloKey: Allow, RuleNetwork: Allow, RuleTmxCLI: Allow, RuleDeleteOutside: Allow, RuleElevation: Hold, // the one rule Held by default
		RuleBrowser: Allow, RuleShutdown: Allow, RuleTmxState: Allow,
		RulePipeShell: Allow, RuleWriteOutside: Allow, RuleClaudeConfig: Allow, RuleDesktop: Allow,
		RulePermsOutside: Allow, RuleGitRewrite: Allow,
		// The fixed holds of version 1: switchable rules now, Allowed like the rest.
		RuleBrowserScript: Allow, RuleP2PInvite: Allow, RuleP2PJoin: Allow, RuleP2PInviteWA: Allow,
	}
	d := DefaultStates()
	for id, o := range d {
		if o == Hold && id != RuleElevation {
			t.Errorf("%s is Held by default: only administrator rights may be", id)
		}
	}
	if len(d) != len(want) {
		t.Errorf("%d switchable rules, %d decided defaults", len(d), len(want))
	}
	for id, o := range want {
		if d[id] != o {
			t.Errorf("default of %s = %s, want %s", id, d[id], o)
		}
	}
	for id := range d {
		if _, ok := want[id]; !ok {
			t.Errorf("rule %s has no decided default", id)
		}
	}
	// The never-rules have no switch and are all Blocks: no fixed rule holds.
	fixed := []string{RuleKillByName, RuleKillTmx, RuleShutdownStart, RuleTmxSecrets, RuleClaudeHooks, RuleBrowserUnsafe}
	if len(FixedRules()) != len(fixed) {
		t.Errorf("%d fixed rules, want %d", len(FixedRules()), len(fixed))
	}
	for _, id := range fixed {
		r, ok := RuleByID(id)
		if !ok || !r.Fixed || r.Outcome != Block || (Context{States: map[string]Outcome{id: Allow}}).StateOf(id) != Block {
			t.Errorf("fixed rule %s: %+v", id, r)
		}
		if _, ok := d[id]; ok {
			t.Errorf("fixed rule %s has a switch", id)
		}
	}
	for _, r := range Rules() {
		if (Context{}).StateOf(r.ID) != r.Outcome {
			t.Errorf("strict %s changed", r.ID)
		}
	}
	if (Context{States: map[string]Outcome{RuleDisk: "maybe"}}).StateOf(RuleDisk) != Block {
		t.Error("an invalid state was taken")
	}
	if (Context{}).StateOf("no-such-rule") != Block {
		t.Error("an unknown rule did not block")
	}
}

// DefaultAt: what an install had applied before, so the upgrade log tells the truth.
func TestDefaultAt(t *testing.T) {
	for _, r := range append(Rules(), FixedRules()...) {
		if o, ok := DefaultAt(0, r.ID); !ok || o != r.Outcome {
			t.Errorf("version 0 of %s = %s, want the strict outcome %s", r.ID, o, r.Outcome)
		}
		today := r.Default
		if r.Fixed {
			today = r.Outcome
		}
		for _, v := range []int{DefaultsVersion, DefaultsVersion + 1} {
			if o, ok := DefaultAt(v, r.ID); !ok || o != today {
				t.Errorf("version %d of %s = %s, want what it does today, %s", v, r.ID, o, today)
			}
		}
	}
	// Version 1 (the owner's brief, 3 Nov) differed from version 2 in exactly these: seven rules
	// were Held, four were fixed holds, and kill-by-name had a switch, Allowed.
	changed := map[string]Outcome{
		RuleTmxCLI: Hold, RuleElevation: Hold, RuleHelloKey: Hold, RuleNetwork: Hold,
		RuleDeleteOutside: Hold, RulePermsOutside: Hold, RuleGitRewrite: Hold,
		RuleBrowserScript: Hold, RuleP2PInvite: Hold, RuleP2PJoin: Hold, RuleP2PInviteWA: Hold,
		RuleKillByName: Allow,
	}
	for _, r := range append(Rules(), FixedRules()...) {
		want, was := changed[r.ID]
		if !was {
			if want = r.Default; r.Fixed {
				want = r.Outcome
			}
		}
		if o, _ := DefaultAt(1, r.ID); o != want {
			t.Errorf("version 1 of %s = %s, want %s", r.ID, o, want)
		}
		if HadSwitch(1, r.ID) != (r.ID == RuleKillByName) || HadSwitch(0, r.ID) || HadSwitch(DefaultsVersion, r.ID) {
			t.Errorf("HadSwitch of %s", r.ID)
		}
	}
	if _, ok := DefaultAt(1, "no-such-rule"); ok {
		t.Error("an unknown rule has a default")
	}
}

// Settings: switches overlay the defaults; fixed rules and junk never enter.
func TestEffectiveStates(t *testing.T) {
	s := Settings{RuleStates: map[string]Outcome{RuleDisk: Allow, RuleKillTmx: Allow, RuleKillByName: Allow, "nope": Allow,
		RuleBoot: "x", RuleElevation: Hold, RuleP2PJoin: Hold}}
	e := s.Effective()
	if e[RuleDisk] != Allow || e[RuleBoot] != Block || e[RuleElevation] != Hold || e[RuleP2PJoin] != Hold {
		t.Fatalf("effective: %v", e)
	}
	for _, id := range []string{RuleKillTmx, RuleKillByName} {
		if _, ok := e[id]; ok {
			t.Fatalf("the fixed rule %s got a state", id)
		}
	}
	if _, ok := e["nope"]; ok {
		t.Fatal("an unknown rule got a state")
	}
}
