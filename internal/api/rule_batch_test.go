package api_test

import (
	"net/http"
	"strings"
	"testing"

	tc "github.com/blackcode-ch/tmx-go/internal/client"
	"github.com/blackcode-ch/tmx-go/internal/guard"
)

// Several rule switches are saved with ONE confirmation that names each of them; the proof is
// good for exactly that list; nobody but the owner may; a bad entry changes nothing. The rules
// are taken from the catalogue as it is, so the test does not depend on the defaults of the day.
func TestRuleBatchNeedsOneConfirmation(t *testing.T) {
	s := newPeerSetup(t)
	d := s.d
	owner := tc.New(d.URL, d.OwnerToken)
	agent := tc.New(d.URL, d.AgentToken(t, "head"))
	const path = "/api/safety/guard/rules"
	body := func(m map[string]string) map[string]any { return map[string]any{"states": m} }

	rules, before := guard.Rules(), d.Fleet.GuardStates()
	if len(rules) < 5 || len(guard.FixedRules()) == 0 {
		t.Fatalf("the catalogue: %d switchable, %d fixed", len(rules), len(guard.FixedRules()))
	}
	other := func(st guard.Outcome) string { // a state the rule does not have now
		if st == guard.Allow {
			return string(guard.Hold)
		}
		return string(guard.Allow)
	}
	a, b, c, same, spare := rules[0].ID, rules[1].ID, rules[2].ID, rules[3].ID, rules[4].ID
	want := map[string]string{a: other(before[a]), b: other(before[b]), c: other(before[c])}
	batch := body(map[string]string{a: want[a], b: want[b], c: want[c], same: string(before[same])}) // one rule keeps its state

	if st, _ := call(t, agent, "PUT", path, batch, nil); st != http.StatusForbidden {
		t.Fatalf("an agent saved a batch: %d", st)
	}
	if got := peerDo(t, s.url, "PUT", path, "hosta", s.pair, nil, batch, nil); got < 400 {
		t.Fatalf("a peer saved a batch: %d", got)
	}
	for name, bad := range map[string]map[string]any{
		"empty":      body(map[string]string{}),
		"fixed rule": body(map[string]string{a: want[a], guard.FixedRules()[0].ID: "allow"}),
		"bad state":  body(map[string]string{a: "maybe"}),
		"unknown":    body(map[string]string{"no-such-rule": "allow"}),
	} {
		if st, _ := call(t, owner, "PUT", path, bad, nil); st != http.StatusBadRequest {
			t.Fatalf("%s: %d", name, st)
		}
	}
	if d.Fleet.GuardStates()[a] != before[a] {
		t.Fatal("a refused batch changed a rule")
	}

	st, conf := call(t, owner, "PUT", path, batch, nil)
	if st != http.StatusPreconditionRequired {
		t.Fatalf("a batch without proof: %d", st)
	}
	// One prompt, naming the three real changes, and the rule that keeps its state as the
	// owner's own choice from now on (so that later defaults leave it alone).
	if !strings.Contains(conf.Reason, "change 3 guard rules") || strings.Count(conf.Reason, " to ") != 3 || !strings.Contains(conf.Reason, "keep 1 as it is") {
		t.Fatalf("the confirmation: %q", conf.Reason)
	}
	proven := confirmed(t, owner, conf)
	// The proof is for this list only.
	another := body(map[string]string{a: want[a], spare: other(before[spare])})
	if st, _ := call(t, proven, "PUT", path, another, nil); st != http.StatusPreconditionRequired {
		t.Fatalf("a proof for one list saved another: %d", st)
	}
	if d.Fleet.GuardStates()[spare] != before[spare] {
		t.Fatal("another list was saved with this proof")
	}
	logBefore := len(d.Fleet.GuardSettings().RuleLog)
	var out struct {
		Guard guard.Settings `json:"guard"`
	}
	if st, _ := call(t, proven, "PUT", path, batch, &out); st != 0 {
		t.Fatalf("the batch with its proof: %d", st)
	}
	after := d.Fleet.GuardStates()
	for id, state := range want {
		if string(after[id]) != state {
			t.Fatalf("%s is %s after the batch, want %s", id, after[id], state)
		}
	}
	if after[same] != before[same] || after[spare] != before[spare] {
		t.Fatalf("a rule outside the changes moved: %s %s", after[same], after[spare])
	}
	if n := len(out.Guard.RuleLog) - logBefore; n != 3 {
		t.Fatalf("logged changes: %d (a rule that keeps its state is not a change)", n)
	}
	for _, id := range []string{a, b, c, same} {
		if _, his := out.Guard.RuleStates[id]; !his {
			t.Fatalf("%s is not recorded as the owner's: %v", id, out.Guard.RuleStates)
		}
	}
	if _, his := out.Guard.RuleStates[spare]; his {
		t.Fatalf("a rule outside the batch was recorded as the owner's: %v", out.Guard.RuleStates)
	}
	for _, ch := range out.Guard.RuleLog[logBefore:] {
		if ch.By != "owner" || want[ch.Rule] != string(ch.New) || ch.Old != before[ch.Rule] {
			t.Fatalf("a logged change: %+v", ch)
		}
	}
	// Saving the same again changes nothing and asks nothing.
	if st, _ := call(t, owner, "PUT", path, batch, nil); st != 0 {
		t.Fatalf("a batch with nothing to change asked for a proof: %d", st)
	}
}
