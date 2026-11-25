package claude

import (
	"github.com/blackcode-ch/tmx-go/internal/runner"
	"testing"
)

func TestTrialDoesNotInheritPythonHooks(t *testing.T) {
	args := TerminalArgs(runner.Spec{SettingsPath: "go-settings.json"})
	for i, s := range args {
		if s == "--setting-sources" && i+1 < len(args) && args[i+1] == "" {
			return
		}
	}
	t.Fatalf("user settings not disabled: %v", args)
}

// The only source ever named is "project", and only when the fleet asked for it: never the
// user's or the local settings.
func TestSettingSourcesAreNoneOrProjectOnly(t *testing.T) {
	for _, tc := range []struct {
		spec runner.Spec
		want string
	}{
		{runner.Spec{}, ""},
		{runner.Spec{ProjectSource: true}, "project"},
	} {
		for _, args := range [][]string{ChatArgs(tc.spec), TerminalArgs(tc.spec)} {
			found := false
			for i, s := range args {
				if s == "--setting-sources" {
					if found || i+1 >= len(args) || args[i+1] != tc.want {
						t.Fatalf("setting sources for %+v: %q", tc.spec, args)
					}
					found = true
				}
			}
			if !found {
				t.Fatalf("no --setting-sources for %+v: %q", tc.spec, args)
			}
		}
	}
}
