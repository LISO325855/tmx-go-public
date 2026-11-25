package cli

import "github.com/blackcode-ch/tmx-go/internal/config"

// rulesSettings reads the rules configuration in force (config.Config.EffectiveRules) from
// config.json, so a change (the owner's page, an edit of the file) reaches the node at its
// next look at the source without a restart; start is the configuration the daemon started
// with, used whenever the file cannot be read.
func rulesSettings(paths config.Paths, start config.Config) func() config.RulesConfig {
	return func() config.RulesConfig {
		if c, err := config.Load(paths); err == nil {
			return c.EffectiveRules()
		}
		return start.EffectiveRules()
	}
}
