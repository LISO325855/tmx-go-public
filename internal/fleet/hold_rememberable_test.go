package fleet

import (
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/guard"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// P0, 4 Nov 04:27 (tmx-lead, go-reviewer-o4): only the Rememberable rules may be remembered.
// Every other rule, with its switch set to Held, still brings a card after an Allow, needs the
// proof again, and is never written to allowed_before. One case per rule, so a future rule is
// covered by default.
func TestOnlyRememberableRulesAreRemembered(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	head := mustAgent(t, f, "head")
	me := callerFor(t, f, e.chat, head.ID)

	var all []guard.Rule
	all = append(all, guard.Rules()...)
	all = append(all, guard.FixedRules()...)
	checked := 0
	for _, r := range all {
		if guard.Rememberable[r.ID] {
			continue
		}
		if !r.Fixed {
			if _, err := f.SetRuleState(OwnerCaller, r.ID, guard.Hold); err != nil {
				t.Fatalf("%s: switch to Held: %v", r.ID, err)
			}
		} else if r.Outcome != guard.Hold {
			continue // a fixed Block never becomes a hold at all
		}
		req := HoldRequest{Tool: "Bash", Command: "placeholder-" + r.ID, Rule: r.ID, Cwd: "/w"}
		hold := func() (model.Approval, error) {
			switch r.ID {
			case guard.RuleP2PInvite:
				return f.HoldP2P(me, ToolP2PInvite, "Create an invite")
			case guard.RuleP2PJoin:
				return f.HoldP2P(me, ToolP2PJoin, "Join another tmx")
			case guard.RuleP2PInviteWA:
				return f.HoldP2P(me, ToolP2PInviteWA, "Send a join invite by WhatsApp")
			default:
				return f.Hold(me, req)
			}
		}
		first, err := hold()
		if err != nil || first.State != model.ApprovalPending {
			t.Fatalf("%s: first hold: %+v %v", r.ID, first, err)
		}
		if _, err := f.Decide(OwnerCaller, first.ID, DecideRequest{Decision: "allow"}); err != nil {
			t.Fatalf("%s: allow: %v", r.ID, err)
		}
		again, err := hold()
		if err != nil || again.State != model.ApprovalPending {
			t.Fatalf("%s: the same call after an Allow did not bring a card: %+v %v", r.ID, again, err)
		}
		if f.AllowedBefore(again) {
			t.Fatalf("%s: the same call after an Allow skips the proof", r.ID)
		}
		f.CancelHold(me, again.ID)
		checked++
	}
	if n := len(f.GuardSettings().AllowedBefore); n != 0 {
		t.Fatalf("non-rememberable rules were recorded: %d entries", n)
	}
	// The 9 Sep class and the lockout rows are among them, by name.
	for _, id := range []string{guard.RuleDisk, guard.RuleDeleteRoot, guard.RulePermsRoot, guard.RuleBoot, guard.RulePermsOutside,
		guard.RuleElevation, guard.RuleHelloKey, guard.RuleNetwork, guard.RuleTmxCLI, guard.RuleBrowserUnsafe,
		guard.RuleP2PInvite, guard.RuleP2PJoin, guard.RuleP2PInviteWA} {
		if guard.Rememberable[id] {
			t.Errorf("%s is rememberable", id)
		}
	}
	if checked < 9 {
		t.Fatalf("only %d rules checked", checked)
	}
}

// A scripted browser run asks once (17 Nov): the script the owner allowed runs again without a
// card; another script, another folder or another kind of agent still asks.
func TestScriptedBrowserHoldIsRememberedAfterOneAllow(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	head := mustAgent(t, f, "head")
	me := callerFor(t, f, e.chat, head.ID)
	heldRule(t, f, guard.RuleBrowserScript) // no rule is held by default (build5's guard): the owner set this one to Held
	run := HoldRequest{Tool: "Bash", Command: "npx playwright test e2e/login.spec.ts", Rule: guard.RuleBrowserScript, Cwd: "/w"}
	first, err := f.Hold(me, run)
	if err != nil || first.State != model.ApprovalPending {
		t.Fatalf("the first run brings a card: %+v %v", first, err)
	}
	if _, err := f.Decide(OwnerCaller, first.ID, DecideRequest{Decision: "allow"}); err != nil {
		t.Fatal(err)
	}
	second, err := f.Hold(me, run)
	if err != nil || second.State != model.ApprovalAllowed || second.Answer != AnswerSame {
		t.Fatalf("the same script again: %+v %v", second, err)
	}
	other := run
	other.Command = "npx playwright test e2e/checkout.spec.ts"
	if h, _ := f.Hold(me, other); h.State != model.ApprovalPending {
		t.Fatal("another script ran without a card")
	}
	elsewhere := run
	elsewhere.Cwd = "/other"
	if h, _ := f.Hold(me, elsewhere); h.State != model.ApprovalPending {
		t.Fatal("the same script in another folder ran without a card")
	}
	// A denied script is not remembered.
	denied := HoldRequest{Tool: "Bash", Command: "node scrape.js --remote-debugging-port=9222", Rule: guard.RuleBrowserScript, Cwd: "/w"}
	d1, _ := f.Hold(me, denied)
	if _, err := f.Decide(OwnerCaller, d1.ID, DecideRequest{Decision: "deny"}); err != nil {
		t.Fatal(err)
	}
	if h, _ := f.Hold(me, denied); h.State != model.ApprovalPending {
		t.Fatal("a denied script ran without a card")
	}
}
