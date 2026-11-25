package config

import "testing"

func TestEffectiveRules(t *testing.T) {
	old := DefaultRulesSource
	DefaultRulesSource = "https://example.invalid/rules.git"
	defer func() { DefaultRulesSource = old }()

	// Nothing configured: the defaults, and no source (trial, and any Go test binary).
	for _, c := range []Config{{}, {Profile: ProfileProduction}} {
		r := c.EffectiveRules()
		if r.Source != "" || r.Branch != DefaultRulesBranch || r.FetchMinutes != DefaultRulesFetchMinutes {
			t.Errorf("profile %q: %+v", c.Profile, r)
		}
	}
	c := Config{Rules: &RulesConfig{Source: "  D:/rules  ", Branch: " next ", Pin: " ABCDEF ", FetchMinutes: 3,
		Signers: []RulesSigner{{ID: "head", Key: "k"}}}}
	r := c.EffectiveRules()
	if r.Source != "D:/rules" || r.Branch != "next" || r.Pin != "abcdef" || r.FetchMinutes != 3 || len(r.Signers) != 1 {
		t.Errorf("configured: %+v", r)
	}
	r.Signers[0].ID = "changed"
	if c.Rules.Signers[0].ID != "head" {
		t.Error("EffectiveRules hands out the saved signers themselves")
	}
	c.Rules.Off = true
	if r := c.EffectiveRules(); r.Source != "" {
		t.Errorf("off: %+v", r)
	}
}
