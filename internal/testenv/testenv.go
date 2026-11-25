// Package testenv keeps tests away from a live Tiamatrix install.
//
// An agent's shell carries TMXG_HOME, TMXG_URL, TMXG_PORT and an agent token that point at the
// daemon it runs under. A `go test` started from such a shell inherits them, and code under
// test that reads them (the guard hook, the CLI, the config) would talk to that live node or
// write into its home. Every test package imports this package for its side effect (the file
// zz_testenv_test.go in each); its init runs before any test and:
//
//   - removes the variables that point at an install or carry a credential,
//   - sets TMXG_HOME to a folder under the temp dir that belongs to this test process,
//   - marks the process tree (TMXG_TEST_SANDBOX), so a program a test starts itself, with a
//     home, URL or token the test chose on purpose, keeps what it was given.
//
// The switches tests are turned on with (TMXG_LIVE, TMXG_SOAK, TMXG_FAKE_*, TMX_VOICE_*, ...)
// are left alone: they are set on purpose by whoever runs the tests, and CI uses them.
package testenv

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Marker is set once the environment of a test process tree has been cleaned.
const Marker = "TMXG_TEST_SANDBOX"

// pointers are the variables that say where an install is or who the caller is.
var pointers = map[string]bool{
	"TMXG_HOME": true, "TMXG_URL": true, "TMXG_PORT": true, "TMXG_PROFILE": true,
	"TMXG_AGENT_ID": true, "TMXG_AGENT_NAME": true, "TMXG_COMPANION_DATA": true,
	"TMX_HOME": true, "TMX_URL": true, "TMX_HOST": true, "TMX_PORT": true, "TMX_PY_HOME": true,
}

func init() { Clean() }

// Clean does the work of init; it does nothing in a process tree that is already marked.
func Clean() {
	if os.Getenv(Marker) != "" {
		return
	}
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if Removed(name) {
			_ = os.Unsetenv(name)
		}
	}
	_ = os.Setenv("TMXG_HOME", filepath.Join(os.TempDir(), "tmxg-test-home-"+strconv.Itoa(os.Getpid())))
	_ = os.Setenv(Marker, "1")
}

// Removed reports whether a variable is taken out of a test's environment: one that points at
// an install, or any TMX variable that carries a token, a secret or a key.
func Removed(name string) bool {
	up := strings.ToUpper(name) // Windows: names are case-insensitive
	if !strings.HasPrefix(up, "TMX") {
		return false
	}
	return pointers[up] || strings.Contains(up, "TOKEN") || strings.Contains(up, "SECRET") || strings.HasSuffix(up, "_KEY")
}
