package config

import "testing"

func TestFleetPublicUpdateDefault(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	for _, tc := range []struct{ version, repo string }{
		{"fleet-v1.0.3-303-gabcd1234", PublicUpdateRepo},
		{"fleet-v1.0.3", PublicUpdateRepo},
		{"0.1.0-dev", DefaultUpdateRepo},
	} {
		Version = tc.version
		if got := (*UpdateConfig)(nil).Resolved().Repo; got != tc.repo {
			t.Fatalf("%s: repo %s, want %s", tc.version, got, tc.repo)
		}
		if got := (&UpdateConfig{Repo: "custom/releases"}).Resolved().Repo; got != "custom/releases" {
			t.Fatalf("explicit repo replaced: %s", got)
		}
	}
}
