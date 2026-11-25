package fleet

import (
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/guard"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// The hold flow bugs of 17 Nov: a hold nobody answers ends by itself even when its hook never
// comes back; a lead or the head takes back a worker's hold; a stop ends the agent's holds
// before its process is asked to end.

func holdFixture(t *testing.T) (*env, *Fleet, model.Agent, model.Agent, model.Agent) {
	t.Helper()
	e := newEnv(t)
	f := e.started()
	if _, err := f.CreateProject(OwnerCaller, ProjectRequest{Name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	head, lead := mustAgent(t, f, "head"), mustAgent(t, f, "alpha-lead")
	w, err := f.LaunchAgent(callerFor(t, f, e.chat, lead.ID), LaunchRequest{Name: "w1"})
	if err != nil {
		t.Fatal(err)
	}
	heldRule(t, f, guard.RuleElevation)
	return e, f, head, lead, w
}

// heldRule makes rule a hold for this test, whatever its default is. A rule without a switch
// (fixed) keeps its built-in outcome.
func heldRule(t *testing.T, f *Fleet, rule string) {
	t.Helper()
	if r, ok := guard.RuleByID(rule); ok && r.Fixed {
		return
	}
	if _, err := f.SetRuleState(OwnerCaller, rule, guard.Hold); err != nil {
		t.Fatal(err)
	}
}

func TestAHoldNobodyAnswersEndsWithoutItsHook(t *testing.T) {
	old := holdDeadline
	holdDeadline = 80 * time.Millisecond
	t.Cleanup(func() { holdDeadline = old })
	e, f, _, _, w := holdFixture(t)
	me := callerFor(t, f, e.chat, w.ID)

	ap, err := f.Hold(me, HoldRequest{Tool: "Bash", Command: "sudo true", Rule: guard.RuleElevation, Cwd: "/w"})
	if err != nil || ap.State != model.ApprovalPending {
		t.Fatalf("hold: %+v %v", ap, err)
	}
	// The hook never calls CancelHold (it was killed). The daemon ends the hold.
	eventually(t, "the hold ends by itself", func() bool {
		cur, err := f.ApprovalByID(ap.ID)
		return err == nil && cur.State == model.ApprovalExpired
	})
	if got := f.PendingHolds(me); len(got) != 0 {
		t.Fatalf("still pending: %+v", got)
	}
	if a := mustAgent(t, f, "w1"); a.State == model.StateWaiting {
		t.Fatalf("the agent still shows as waiting: %s", a.State)
	}
	// A waiting hook is woken, not left to its own timer.
	if got, err := f.WaitHold(t.Context(), me, ap.ID, time.Minute); err != nil || got.State != model.ApprovalExpired {
		t.Fatalf("wait after the deadline: %+v %v", got, err)
	}

	// A hold that was answered in time is not touched by the deadline.
	ap2, err := f.Hold(me, HoldRequest{Tool: "Bash", Command: "sudo id", Rule: guard.RuleElevation, Cwd: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Decide(OwnerCaller, ap2.ID, DecideRequest{Decision: "allow"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if cur, _ := f.ApprovalByID(ap2.ID); cur.State != model.ApprovalAllowed {
		t.Fatalf("the deadline changed an answered hold: %+v", cur)
	}
}

func TestALeadOrTheHeadWithdrawsAWorkersHold(t *testing.T) {
	e, f, head, lead, w := holdFixture(t)
	me := callerFor(t, f, e.chat, w.ID)
	// A second worker in another project: not in the first lead's part of the tree.
	if _, err := f.CreateProject(OwnerCaller, ProjectRequest{Name: "beta"}); err != nil {
		t.Fatal(err)
	}
	otherLead := mustAgent(t, f, "beta-lead")
	other, err := f.LaunchAgent(callerFor(t, f, e.chat, otherLead.ID), LaunchRequest{Name: "w2"})
	if err != nil {
		t.Fatal(err)
	}

	hold := func(cmd string) model.Approval {
		t.Helper()
		ap, err := f.Hold(me, HoldRequest{Tool: "Bash", Command: cmd, Rule: guard.RuleElevation, Cwd: "/w"})
		if err != nil || ap.State != model.ApprovalPending {
			t.Fatalf("hold: %+v %v", ap, err)
		}
		return ap
	}
	a1 := hold("sudo one")
	// Not: a worker (even of the same project), a lead of another project, the owner route.
	for name, c := range map[string]Caller{"another worker": AgentCaller(other.ID), "another project's lead": AgentCaller(otherLead.ID), "the owner": OwnerCaller} {
		if _, err := f.WithdrawHold(c, a1.ID); KindOf(err) != KindForbidden {
			t.Fatalf("%s withdrew the hold: %v", name, err)
		}
	}
	if cur, _ := f.ApprovalByID(a1.ID); cur.State != model.ApprovalPending {
		t.Fatalf("a refused withdrawal changed the hold: %+v", cur)
	}
	got, err := f.WithdrawHold(AgentCaller(lead.ID), a1.ID)
	if err != nil || got.State != model.ApprovalExpired || got.Answer != AnswerWithdrawn {
		t.Fatalf("the lead withdraws its worker's hold: %+v %v", got, err)
	}
	a2 := hold("sudo two")
	if got, err = f.WithdrawHold(AgentCaller(head.ID), a2.ID); err != nil || got.State != model.ApprovalExpired {
		t.Fatalf("the head withdraws a worker's hold: %+v %v", got, err)
	}
	if a := mustAgent(t, f, "w1"); a.State == model.StateWaiting {
		t.Fatalf("the worker still waits: %s", a.State)
	}
	// Nothing was allowed: the same command asks again.
	if again := hold("sudo two"); again.State != model.ApprovalPending {
		t.Fatalf("a withdrawn hold was remembered as allowed: %+v", again)
	}
}

func TestStopEndsTheAgentsHoldsFirst(t *testing.T) {
	e, f, _, lead, w := holdFixture(t)
	me := callerFor(t, f, e.chat, w.ID)
	ap, err := f.Hold(me, HoldRequest{Tool: "Bash", Command: "sudo true", Rule: guard.RuleElevation, Cwd: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	// The hook is waiting for the owner, as it does inside the harness.
	woke := make(chan model.Approval, 1)
	go func() {
		got, _ := f.WaitHold(t.Context(), me, ap.ID, time.Minute)
		woke <- got
	}()
	time.Sleep(30 * time.Millisecond)
	done := make(chan error, 1)
	go func() {
		_, err := f.StopAgent(AgentCaller(lead.ID), "w1")
		done <- err
	}()
	select {
	case got := <-woke:
		if got.State != model.ApprovalExpired {
			t.Fatalf("the waiting hook got %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stop did not end the hold: its hook is still waiting")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the stop did not return")
	}
	if a := mustAgent(t, f, "w1"); a.Desired != "stopped" {
		t.Fatalf("after the stop: %+v", a)
	}
	if got := f.PendingHolds(me); len(got) != 0 {
		t.Fatalf("holds left after the stop: %+v", got)
	}
}

// "The same thing never asks twice" holds for a command that is the same thing: an exact
// command allowed once runs again without a card. A command with a variable, a wildcard or a
// subshell is not the same thing next time, so allowing it once is not remembered.
func TestAnAllowedCallIsRememberedOnlyWhenItsMeaningIsFixed(t *testing.T) {
	e, f, _, _, w := holdFixture(t)
	me := callerFor(t, f, e.chat, w.ID)
	heldRule(t, f, guard.RuleDeleteOutside)
	if !guard.Rememberable[guard.RuleDeleteOutside] {
		t.Skip("recursive deletes outside the folder are not a rememberable rule here")
	}
	for cmd, remembered := range map[string]bool{
		"rm -rf /srv/data/old":  true,
		`rm -rf "$DIR"`:         false,
		"rm -rf /srv/data/*":    false,
		"rm -rf `cat list`":     false,
		"rm -rf $(cat targets)": false,
	} {
		req := HoldRequest{Tool: "Bash", Command: cmd, Rule: guard.RuleDeleteOutside, Cwd: "/w"}
		ap, err := f.Hold(me, req)
		if err != nil || ap.State != model.ApprovalPending {
			t.Fatalf("%q: first hold %+v %v", cmd, ap, err)
		}
		if _, err := f.Decide(OwnerCaller, ap.ID, DecideRequest{Decision: "allow"}); err != nil {
			t.Fatalf("%q: allow once: %v", cmd, err)
		}
		again, err := f.Hold(me, req)
		if err != nil {
			t.Fatal(err)
		}
		if got := again.State == model.ApprovalAllowed; got != remembered {
			t.Errorf("%q: the second time it was %s; remembered should be %v", cmd, again.State, remembered)
		}
		if f.AllowedBefore(again) != remembered && again.State == model.ApprovalPending {
			t.Errorf("%q: AllowedBefore = %v", cmd, f.AllowedBefore(again))
		}
		if again.State == model.ApprovalPending {
			f.CancelHold(me, again.ID)
		}
	}
}

// A pairing approval is the owner's decision and outlives the agent that asked: the head may
// not end a lead's (review, 17 Nov); the lead that asked still may.
func TestAnAgentAboveDoesNotWithdrawAPairingApproval(t *testing.T) {
	e, f, head, lead, _ := holdFixture(t)
	leadC := callerFor(t, f, e.chat, lead.ID)
	heldRule(t, f, guard.RuleP2PInvite) // a card, whatever the rule's default
	ap, err := f.HoldP2P(leadC, ToolP2PInvite, "Invite Bob")
	if err != nil {
		t.Fatal(err)
	}
	if ap.State != model.ApprovalPending {
		t.Skipf("the pairing approval did not wait (%s): nothing to withdraw", ap.State)
	}
	if _, err := f.WithdrawHold(AgentCaller(head.ID), ap.ID); KindOf(err) != KindForbidden {
		t.Fatalf("the head withdrew a lead's pairing approval: %v", err)
	}
	if cur, _ := f.ApprovalByID(ap.ID); cur.State != model.ApprovalPending {
		t.Fatalf("the pairing approval changed: %+v", cur)
	}
	if got, err := f.WithdrawHold(leadC, ap.ID); err != nil || got.State != model.ApprovalExpired {
		t.Fatalf("the lead withdraws its own pairing approval: %+v %v", got, err)
	}
}
