package config

import (
	"strings"
	"testing"
)

// RulesConfig is config.json "rules": where the fleet's standing rules and app guides come
// from (internal/rules, DESIGN.md "Fleet rules"). nil = the defaults: no source in the trial
// profile, DefaultRulesSource in the production profile.
type RulesConfig struct {
	// Source is the git repository the rules are read from: an https or ssh address, or a
	// folder on this machine. "" = DefaultRulesSource (production profile only).
	Source string `json:"source,omitempty"`
	// Branch whose tip the node follows; "" = main.
	Branch string `json:"branch,omitempty"`
	// Pin holds this node at one commit (40 hex digits) whatever the branch says; "" = follow
	// the branch.
	Pin string `json:"pin,omitempty"`
	// Signers are the keys whose signed commits count. Empty = commits are taken unsigned and
	// every agent is told the signature was not checked.
	Signers []RulesSigner `json:"signers,omitempty"`
	// FetchMinutes between looks at the source; 0 = 10.
	FetchMinutes int `json:"fetch_minutes,omitempty"`
	// Off turns the rules source off on this node, also in the production profile.
	Off bool `json:"off,omitempty"`
}

// RulesSigner is one pinned signing key of the rules source.
type RulesSigner struct {
	// ID names the key to agents and in the log ("head", "fleet-gate").
	ID string `json:"id"`
	// Key: an authorized_keys line ("ssh-ed25519 AAAA...") or the raw 32-byte Ed25519 key in
	// standard base64 (the form of FleetKey).
	Key string `json:"key"`
}

// DefaultRulesSource is the source of a production node whose config names none. The public
// build has none; the private build sets it in rules_private.go.
var DefaultRulesSource string

// DefaultRulesBranch is the branch a node follows when its config names none.
const DefaultRulesBranch = "main"

// DefaultRulesFetchMinutes is how often a node looks at the source.
const DefaultRulesFetchMinutes = 10

// EffectiveRules is the rules configuration in force: the saved one with the defaults filled
// in. Its Source is "" when this node reads no rules.
func (c Config) EffectiveRules() RulesConfig {
	var r RulesConfig
	if c.Rules != nil {
		r = *c.Rules
		r.Signers = append([]RulesSigner(nil), c.Rules.Signers...)
	}
	r.Source = strings.TrimSpace(r.Source)
	// The default never applies inside a Go test binary (as testmode.go for paths): a test that
	// starts a production-profile daemon must not reach for the fleet's repository.
	if r.Source == "" && c.Production() && !testing.Testing() {
		r.Source = DefaultRulesSource
	}
	if r.Off {
		r.Source = ""
	}
	if r.Branch = strings.TrimSpace(r.Branch); r.Branch == "" {
		r.Branch = DefaultRulesBranch
	}
	if r.FetchMinutes <= 0 {
		r.FetchMinutes = DefaultRulesFetchMinutes
	}
	r.Pin = strings.ToLower(strings.TrimSpace(r.Pin))
	return r
}
