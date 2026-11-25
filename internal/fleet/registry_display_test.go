package fleet

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

func TestShellCommandRedaction(t *testing.T) {
	secret := "registry-test-sentinel"
	quotedPrefix := "'prefix'" + secret
	for _, arg := range []string{
		"--code " + secret, "--token " + secret, "--password=" + secret, "-p '" + secret + " with spaces'",
		"--key \"" + secret + " with spaces\"", "API_KEY=" + secret,
		"TOKEN='" + secret + " with spaces'", "DATABASE_PASSWORD=\"" + secret + " with spaces\"",
		"https://user:" + secret + "@example.test/path", "--token '" + secret,
		"--token " + strings.Repeat("x", 190) + secret,
		`--password "before \"` + secret + `\" after"`,
		"--password \"before `\"" + secret + "`\" after\"",
		"--password " + quotedPrefix,
		"API_KEY=prefix'" + secret + "'suffix",
	} {
		got := shellCommand(`"C:\Program Files\bin\worker.exe" ` + arg + " --verbose")
		if strings.Contains(got, secret) || !(strings.Contains(got, "[redacted]") || strings.Contains(got, "[hidden]")) || !strings.HasPrefix(got, "worker.exe ") {
			t.Fatal("display must redact secret values and remove the executable directory")
		}
	}
	for _, token := range []string{"ghp_" + strings.Repeat("a", 30), "sk-" + strings.Repeat("b", 30), "-----BEGIN " + "PRIVATE KEY-----"} {
		if got := shellCommand("worker --value '" + token + "'"); strings.Contains(got, token) || !strings.Contains(got, "[redacted]") {
			t.Fatal("exact-shape credential was not redacted")
		}
	}
	if got := shellCommand("/usr/bin/node server.js --port 8080"); got != "node server.js --port 8080" {
		t.Fatalf("ordinary arguments changed: %q", got)
	}
}

func TestShellCommandWindowsParameterRedaction(t *testing.T) {
	const secret = "registry-test-sentinel"
	for _, name := range []string{"Password", "Token", "ApiKey", "Secret", "Key", "ClientCredential", "Passwd", "PASSWORD", "tOkEn", "Pass", "Auth", "Pwd", "ClientAuth", "AccessToken"} {
		for _, prefix := range []string{"--", "-", "/"} {
			for _, separator := range []string{" ", ":", "=", ": ", "= ", "", ";", "\n"} {
				for _, value := range []string{secret, `"` + secret + ` with spaces"`, `'` + secret + ` with spaces'`, `"` + secret} {
					t.Run(prefix+name+separator+value, func(t *testing.T) {
						got := shellCommand("worker.ps1 " + prefix + name + separator + value + " --later must-also-disappear")
						want := "worker.ps1 " + prefix + name + " [hidden]"
						if got != want {
							t.Fatal("Windows credential parameter was not fully redacted")
						}
					})
				}
			}
		}
	}
	const ordinary = "worker.ps1 -Port 8080 /Verbose -Profile default"
	if got := shellCommand(ordinary); got != ordinary {
		t.Fatalf("ordinary Windows parameters changed: %q", got)
	}
}

func TestShellCommandRedactsBeforeHiddenTail(t *testing.T) {
	command := "worker https://user:fixture@example.test/path ghp_" + strings.Repeat("a", 30) + " --Password: \"fixture"
	if got := shellCommand(command); got != "worker https://[redacted]@example.test/path [redacted] --Password [hidden]" {
		t.Fatalf("prefix redaction changed: %q", got)
	}
}

// The render check consumes this actual Registry result, not a hand-redacted UI mock.
func TestRegistrySafeDisplayFixture(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	(&fakeOS{}).use(f)
	a := launch(t, f, "display-shell", "")
	brief := `/private/bin/worker --token registry-test-sentinel --port 8080`
	f.mu.Lock()
	f.agents[a.ID].a.Harness = model.HarnessShell
	f.agents[a.ID].a.Brief = brief
	f.mu.Unlock()
	rows := registry(t, f, OwnerCaller)
	row := entry(rows, "shell:"+a.ID)
	if row == nil || row.Command != "worker --token [hidden]" {
		t.Fatal("registry did not return the safe display command")
	}
	f.mu.Lock()
	unchanged := f.agents[a.ID].a.Brief == brief
	f.mu.Unlock()
	if !unchanged {
		t.Fatal("display redaction changed the private restart command")
	}
	raw, err := json.Marshal([]model.MachineProcess{*row})
	if err != nil || strings.Contains(string(raw), "registry-test-sentinel") {
		t.Fatal("registry JSON contains the sentinel")
	}
	if dest := os.Getenv("TMX_REGISTRY_RENDER_FIXTURE"); dest != "" {
		if err := os.WriteFile(dest, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
