package config

import (
	"os"
	"slices"
	"testing"
)

func TestUIProfileIsOffUnlessConfigSaysClassic(t *testing.T) {
	for _, c := range []struct {
		name string
		ui   *UIConfig
		want string
		warn bool
	}{
		{"absent", nil, "", false},
		{"empty", &UIConfig{}, "", false},
		{"classic", &UIConfig{Profile: "classic"}, UIProfileClassic, false},
		{"case and spaces", &UIConfig{Profile: " Classic "}, UIProfileClassic, false},
		{"unknown", &UIConfig{Profile: "retro"}, "", true},
	} {
		cfg := Config{UI: c.ui}
		if got := cfg.UIProfile(); got != c.want {
			t.Errorf("%s: UIProfile() = %q, want %q", c.name, got, c.want)
		}
		if got := cfg.UIProfileWarning() != ""; got != c.warn {
			t.Errorf("%s: warning = %v, want %v", c.name, got, c.warn)
		}
	}
}

func TestLoadReadsTheUIProfileAndWarnsAboutAnUnknownOne(t *testing.T) {
	home := t.TempDir()
	t.Setenv(EnvHome, home)
	p := Paths{Home: home}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(p.ConfigFile(), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(`{"ui": {"profile": "classic"}}`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.UIProfile() != UIProfileClassic {
		t.Fatalf("UIProfile() = %q after loading classic", c.UIProfile())
	}
	if slices.Contains(c.Warnings, c.UIProfileWarning()) && c.UIProfileWarning() != "" {
		t.Fatalf("classic must not warn: %v", c.Warnings)
	}

	write(`{"ui": {"profile": "retro"}}`)
	c, err = Load(p)
	if err != nil {
		t.Fatalf("an unknown UI profile must not stop the daemon: %v", err)
	}
	if c.UIProfile() != "" {
		t.Fatalf("UIProfile() = %q for an unknown name", c.UIProfile())
	}
	if w := c.UIProfileWarning(); w == "" || !slices.Contains(c.Warnings, w) {
		t.Fatalf("no warning for an unknown UI profile: %v", c.Warnings)
	}

	write(`{}`)
	c, err = Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.UIProfile() != "" || c.UIProfileWarning() != "" {
		t.Fatalf("no ui block: profile %q warning %q", c.UIProfile(), c.UIProfileWarning())
	}
}
