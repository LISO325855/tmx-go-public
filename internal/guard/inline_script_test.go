package guard

import "testing"

// Runs on every machine (no build tag: it carries no destructive command strings; the tagged
// files of this package are kept off Windows development PCs).
//
// An inline script fed to Python that only edits a file is not scripted browser automation,
// whatever its lines begin with (the false hold of 17 Nov). Whether an inline script that
// really drives a browser is still caught is tested where that is decided
// (agentpolicy/heredoc_test.go); the guard's own reading of here-documents may leave their
// bodies out altogether, so it is not asserted here.
func TestInlineScriptIsNotABrowserLaunchOnAnyMachine(t *testing.T) {
	ctx := Context{OS: "windows", Home: `C:\Users\owner`, Own: `C:\Users\owner\Projects\billing`,
		Workspace: `C:\Users\owner\Projects`, StateDir: `C:\Users\owner\AppData\Local\tmx-go`,
		Temp: []string{`C:\Users\owner\AppData\Local\Temp`},
		Git:  func(string) (string, string) { return "main", "main" }}
	ctx.States = DefaultStates()
	// Browser automation allowed, scripted automation held: stated here, so the test says the
	// same whatever the defaults of the day are.
	ctx.States[RuleBrowser], ctx.States[RuleBrowserScript] = Allow, Hold

	edit := "python - <<'EOF'\np='internal/guard/guard.go'\ns=open(p).read()\nbrowserRule = s.count('chrome')\nopen(p,'w').write(s)\nEOF"
	if v := CheckCommand(ctx, ShellPOSIX, edit); v.Outcome != Allow {
		t.Errorf("an editing script: %s/%s (%s)", v.Outcome, v.Rule, v.Detail)
	}
	// A browser started as a command is still held.
	if v := CheckCommand(ctx, ShellPOSIX, "npx playwright test"); v.Outcome != Hold || v.Rule != RuleBrowserScript {
		t.Errorf("npx playwright: %s/%s (%s)", v.Outcome, v.Rule, v.Detail)
	}
}
