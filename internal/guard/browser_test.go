package guard

import (
	"encoding/json"
	"regexp"
	"testing"
)

func TestBrowserDispatchRefused(t *testing.T) {
	for _, call := range []Call{
		{Tool: "Bash", Input: json.RawMessage(`{"command":"npx playwright test"}`)},
		{Tool: "PowerShell", Input: json.RawMessage(`{"command":"Start-Process chrome.exe"}`)},
		{Tool: "mcp__playwright__browser_navigate", Input: json.RawMessage(`{}`)},
		{Tool: "mcp__agent-browser-guard-x__browser_navigate", Input: json.RawMessage(`{}`)},
		{Tool: "mcp__agent-browser__browser_navigate", Input: json.RawMessage(`{}`)},
		{Tool: "mcp__browser__navigate", Input: json.RawMessage(`{}`)},
		{Tool: "mcp__cdp__attach", Input: json.RawMessage(`{}`)},
	} {
		if !regexp.MustCompile(Matcher).MatchString(call.Tool) {
			t.Errorf("hook misses %s", call.Tool)
		}
		if got := Check(Context{OS: "windows"}, call); got.Outcome != Block {
			t.Errorf("allowed %s", call.Tool)
		}
	}
}

func TestAgentBrowserGuardDispatchAllowed(t *testing.T) {
	for _, tool := range []string{
		"mcp__agent-browser-guard__browser_navigate",
		"mcp__agent-browser-guard__browser_snapshot",
		"mcp__agent-browser-guard__browser_click",
	} {
		if got := Check(Context{OS: "windows"}, Call{Tool: tool, Input: json.RawMessage(`{}`)}); got.Outcome != Allow {
			t.Errorf("%s: want Allow, got %v (%s)", tool, got.Outcome, got.Rule)
		}
	}
}

// A word is not a launch (the owner, 17 Nov): a command that merely names a browser or automation
// tool (in an argument, a file name, a test name, a path, a commit message) matches no browser
// rule: not with the strict built-in outcomes, not with the defaults, not with every browser
// rule Blocked or Held. The first case is the one that was denied on Hosta: a test NAME.
func TestBrowserWordIsNotALaunch(t *testing.T) {
	strictest := DefaultStates()
	strictest[RuleBrowser], strictest[RuleBrowserScript] = Block, Hold
	held := DefaultStates()
	held[RuleBrowser], held[RuleBrowserScript] = Hold, Block
	for _, command := range []string{
		`go test ./internal/guard/ -run 'TestDefaultsV2|TestBrowserRulesIgnoreSourceText|TestCommandText'`,
		`go test ./internal/guard -run TestBrowser`,
		`git grep chrome`,
		`grep -rn "playwright\|puppeteer\|chromium" internal/`,
		`cat internal/guard/browser_test.go`,
		`ls ui/e2e/browser && cat docs/chrome-notes.md`,
		`cd /c/work/browser-tests && go vet ./...`,
		`npx browserslist`,
		`git commit -m "fix the browser guard"`,
		`echo "check the browser"`,
		`gh pr create --title "playwright: retry flaky chromium launch"`,
	} {
		for name, ctx := range map[string]Context{
			"strict":   {OS: "windows"},
			"defaults": {OS: "windows", States: DefaultStates()},
			"blocked":  {OS: "windows", States: strictest},
			"held":     {OS: "windows", States: held},
		} {
			for _, sh := range []Shell{ShellPOSIX, ShellPowerShell} {
				if got := CheckCommand(ctx, sh, command); got.Outcome != Allow {
					t.Errorf("%s: %q: want Allow, got %v (%s)", name, command, got.Outcome, got.RuleName)
				}
			}
		}
	}
	// The rule that held such words is gone, switch and all.
	if _, ok := RuleByID(RuleBrowserMention); ok {
		t.Error("browser-mention is still a rule")
	}
	// An actual launch still counts, with the same states.
	for _, command := range []string{`npx playwright test`, `chrome.exe https://example.com`, `Start-Process chrome.exe`} {
		if got := CheckCommand(Context{OS: "windows", States: strictest}, ShellPowerShell, command); got.Outcome != Block || got.Rule != RuleBrowser {
			t.Errorf("%q with browser automation Blocked: got %v/%s", command, got.Outcome, got.Rule)
		}
		if got := CheckCommand(Context{OS: "windows"}, ShellPowerShell, command); got.Outcome != Block {
			t.Errorf("%q strict: got %v/%s", command, got.Outcome, got.Rule)
		}
	}
	if got := CheckCommand(Context{OS: "windows"}, ShellPOSIX, "go test ./internal/agentpolicy"); got.Outcome != Allow {
		t.Errorf("unrelated command: want Allow, got %v", got.Outcome)
	}
}

func TestBrowserPermissionIsWeakening(t *testing.T) {
	s := DefaultSettings()
	var next Settings
	if err := json.Unmarshal([]byte(`{"enabled":true,"allow_browser_automation":true}`), &next); err != nil {
		t.Fatal(err)
	}
	if !s.Weaker(next) {
		t.Fatal("browser permission must require owner gate")
	}
	if next.Weaker(s) {
		t.Fatal("revoking browser permission is not weakening")
	}
}
