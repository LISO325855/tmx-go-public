package api

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/gate"
	"github.com/blackcode-ch/tmx-go/internal/guard"
)

// putRuleStates: PUT /api/safety/guard/rules {states: {rule: state, ...}}. Several rule
// switches saved together with ONE fresh owner proof (the owner, 17 Nov: seven prompts in a row
// for seven rules, "never again"). The confirmation names every change, so what the owner
// confirms is exactly what is saved: the proof is bound to this list (actionKey), and a
// different list needs its own. Rules that already have the asked state are left out before
// the owner is asked; with nothing left there is nothing to confirm.
func (s *Server) putRuleStates(w http.ResponseWriter, r *http.Request) {
	var req struct {
		States map[string]guard.Outcome `json:"states"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.States) == 0 || len(req.States) > len(guard.Rules()) {
		writeError(w, http.StatusBadRequest, "states: one entry per rule to change")
		return
	}
	cur, owners := s.f.GuardStates(), s.f.GuardSettings().RuleStates
	changes := map[string]guard.Outcome{}
	var said, kept []string
	for _, id := range slices.Sorted(maps.Keys(req.States)) {
		rule, ok := guard.RuleByID(id)
		if !ok || rule.Fixed || !guard.ValidState(req.States[id]) {
			writeError(w, http.StatusBadRequest, "unknown or fixed rule, or a state other than block, hold or allow: "+id)
			return
		}
		_, his := owners[id]
		switch {
		case cur[id] != req.States[id]:
			changes[id] = req.States[id]
			said = append(said, fmt.Sprintf("“%s” to %s", rule.Name, req.States[id]))
		case !his:
			// The state it has by default, chosen by the owner: recorded as his, so that later
			// defaults leave the rule alone (as the single switch does).
			changes[id] = req.States[id]
			kept = append(kept, fmt.Sprintf("“%s” (%s)", rule.Name, req.States[id]))
		}
	}
	if len(changes) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"guard": s.f.GuardSettings(), "rules": s.ruleViews()})
		return
	}
	var parts []string
	switch {
	case len(said) == 1:
		parts = append(parts, "set the guard rule "+said[0])
	case len(said) > 1:
		parts = append(parts, fmt.Sprintf("change %d guard rules: %s", len(said), strings.Join(said, "; ")))
	}
	if len(kept) > 0 {
		parts = append(parts, fmt.Sprintf("keep %d as it is, as your own choice: %s", len(kept), strings.Join(kept, "; ")))
	}
	reason := strings.Join(parts, ", and ")
	if !s.requireOwner(w, r, gate.Action{Reason: reason, Key: actionKey(r, map[string]any{"states": changes}), Always: true, Fresh: true}) {
		return
	}
	c, _ := caller(r)
	out, err := s.f.SetRuleStates(c, changes)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"guard": out, "rules": s.ruleViews()})
}
