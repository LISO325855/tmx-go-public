package config

import "strings"

// UIProfileClassic is the UI profile that makes the dashboard a one-to-one copy of the Python
// tmx dashboard (the owner, 17 Nov 2026): everything Python did not have is hidden, and Claude
// agents run in Claude Code's own screen by default, as every Python agent did (the chat view
// is then a reading of that screen, and Chat / Terminal only change the view).
//
// It is ONE switch, off unless config.json says so: nothing is removed from the code, so a
// machine without it keeps today's dashboard and defaults.
const UIProfileClassic = "classic"

// UIConfig is config.json "ui".
type UIConfig struct {
	// Profile: "" (the default dashboard) or "classic".
	Profile string `json:"profile,omitempty"`
}

// UIProfile is the UI profile in force: UIProfileClassic or "". An unknown name is "" (the
// default dashboard) with a warning (UIProfileWarning), never a daemon that does not start.
func (c Config) UIProfile() string {
	if c.UI != nil && strings.EqualFold(strings.TrimSpace(c.UI.Profile), UIProfileClassic) {
		return UIProfileClassic
	}
	return ""
}

// UIProfileWarning says that config.json names a UI profile this build does not know.
func (c Config) UIProfileWarning() string {
	if c.UI == nil || strings.TrimSpace(c.UI.Profile) == "" || c.UIProfile() != "" {
		return ""
	}
	return `config.json "ui.profile" is not a known UI profile (want "` + UIProfileClassic + `" or nothing); the default dashboard is used`
}
