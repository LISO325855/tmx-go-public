package fleet

import (
	"maps"
	"slices"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/guard"
)

// SetRuleStates switches several guard rules in one step (owner only): the Safety page saves a
// batch of changes with ONE confirmation instead of one per rule. All or nothing: an unknown or
// fixed rule or an invalid state changes nothing. Like a single switch (SetRuleState), a rule
// set to the state it already has by default becomes the owner's all the same: the entry in
// RuleStates is what keeps later defaults from moving it. Only a real change is logged; a rule
// that is the owner's already and has the asked state is skipped.
func (f *Fleet) SetRuleStates(c Caller, states map[string]guard.Outcome) (guard.Settings, error) {
	if !c.Owner || c.Peer != "" || c.Companion || c.AgentID != "" {
		return guard.Settings{}, errf(KindForbidden, "only the owner flips a guard rule")
	}
	for id, st := range states {
		if r, ok := guard.RuleByID(id); !ok || r.Fixed {
			return guard.Settings{}, errf(KindInvalid, "%q is not a switchable guard rule", id)
		}
		if !guard.ValidState(st) {
			return guard.Settings{}, errf(KindInvalid, "state must be block, hold or allow")
		}
	}
	f.mu.Lock()
	s := f.safetyLocked().settings
	cur := s.Effective()
	next := maps.Clone(s.RuleStates)
	if next == nil {
		next = map[string]guard.Outcome{}
	}
	at := time.Now().UTC().Format(time.RFC3339)
	var changes []guard.RuleChange
	pinned := 0
	for _, id := range slices.Sorted(maps.Keys(states)) {
		_, owners := s.RuleStates[id]
		if cur[id] == states[id] && owners {
			continue
		}
		next[id] = states[id]
		if cur[id] == states[id] {
			pinned++ // the state stays, the rule is the owner's from now on
			continue
		}
		changes = append(changes, guard.RuleChange{Rule: id, Old: cur[id], New: states[id], At: at, By: "owner"})
	}
	if len(changes) == 0 && pinned == 0 {
		f.mu.Unlock()
		return f.GuardSettings(), nil
	}
	s.RuleStates = next
	s.RuleLog = appendRuleLog(s.RuleLog, changes...)
	err := f.saveGuardLocked(s)
	f.mu.Unlock()
	if err != nil {
		return guard.Settings{}, err
	}
	for _, ch := range changes {
		f.log.Info("guard rule switched", "rule", ch.Rule, "old", ch.Old, "new", ch.New, "batch", len(changes))
	}
	return f.GuardSettings(), nil
}
