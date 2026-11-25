package guard

// The fixed parts of the owner's rule switches (3 Nov 22:14): what stays blocked whatever the
// switch says, and the browser rules. Pure string matching on the command; nothing is run or
// looked up.

import (
	"encoding/json"
	"path"
	"regexp"
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/agentpolicy"
)

// tmxRuntime are process names tmx itself runs on: the daemons, the agents' harnesses and the
// shells their tools run in. Killing one of them by name kills every agent's tools (7 Oct).
var tmxRuntime = map[string]bool{
	"tmx": true, "tmxg": true, "tmx-go": true, "tmxd": true,
	"claude": true, "codex": true, "gemini": true, "node": true, "bun": true, "deno": true,
	"python": true, "python3": true, "pythonw": true, "py": true,
	"bash": true, "sh": true, "zsh": true, "pwsh": true, "powershell": true, "cmd": true,
	"conhost": true, "openconsole": true, "windowsterminal": true, "wt": true,
	// the owner's way in: killing these by name can lock the owner out (reviewer, #232)
	"tailscale": true, "tailscaled": true, "tailscale-ipn": true, "sshd": true,
}

var (
	nameToken    = regexp.MustCompile(`[A-Za-z0-9._-]+`)
	unknownName  = regexp.MustCompile(`\$[A-Za-z_{]|%[A-Za-z_][A-Za-z0-9_]*%|[*?]`)
	shutdownWord = regexp.MustCompile(`(?i)\b(shutdown(\.exe)?|restart-computer|stop-computer|reboot|poweroff|halt|logoff|init\s+[06]|systemctl\s+(poweroff|reboot|halt))\b`)
	startupPlace = regexp.MustCompile(`(?i)(/sc\s+(onstart|onlogon)|-atstartup|-atlogon|currentversion\\+run(once)?\b|currentversion/run(once)?\b|start menu[\\/]+programs[\\/]+startup|shell:(common )?startup|@reboot|launchagents|launchdaemons|/etc/systemd|\.config/systemd|systemctl\s+enable|rc\.local|/etc/init\.d|[\\/]autostart[\\/])`)
)

// killsTmxRuntime reports whether a kill-by-name command (its text) may hit tmx's own runtime:
// one of its names is a tmx runtime name, or a name is only known at run time.
func killsTmxRuntime(text string) bool {
	if unknownName.MatchString(text) {
		return true
	}
	for _, w := range nameToken.FindAllString(text, -1) {
		w = strings.TrimSuffix(strings.ToLower(w), ".exe")
		if tmxRuntime[w] {
			return true
		}
	}
	return false
}

// shutdownAtStartup reports whether text sets a shutdown, restart or logout to run at startup or
// logon: a boot loop.
func shutdownAtStartup(text string) bool {
	return shutdownWord.MatchString(text) && startupPlace.MatchString(text)
}

// A Claude Code settings change that could switch off the guard: disableAllHooks, tmx's hook
// (its command, endpoint or token), and for an edit, any change that touches a "hooks" block
// (deleting a block leaves an innocent new_string, so the old text counts too).
var (
	guardHookText = regexp.MustCompile(`(?i)disableAllHooks|hook\s+pretooluse|/api/guard|TMXG_|tmxg?(\.exe)?["'\\]*\s+hook`)
	hooksKey      = regexp.MustCompile(`(?i)"?\bhooks\b"?\s*:`)
)

func touchesGuardHook(tool string, input []byte) bool {
	if guardHookText.Match(input) {
		return true
	}
	if tool == "Write" {
		return false // a whole new file: judged by its content alone
	}
	var in struct {
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
		Edits     []struct {
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		} `json:"edits"`
	}
	_ = json.Unmarshal(input, &in)
	parts := []string{in.OldString, in.NewString}
	for _, e := range in.Edits {
		parts = append(parts, e.OldString, e.NewString)
	}
	for _, s := range parts {
		if hooksKey.MatchString(s) || strings.Contains(strings.ToLower(s), "pretooluse") {
			return true
		}
	}
	return false
}

// The one browser launch tmx lets through when browser automation is allowed: the bundled
// Chromium (Playwright's) or the Companion profile, with exactly one --disable-features that
// includes WebAuthenticationUseNativeWinApi (Chromium honours only the last such flag), and no
// remote debugging (that is CDP automation).
var (
	directBrowser = regexp.MustCompile(`(?i)(^|[\s;&|"'\\/(])(chrome|chromium|msedge|firefox)(\.exe)?(["'\s)]|$)`)
	// A launcher or library driving a browser. The bundled browser's own folder
	// (ms-playwright\chromium-…) is not one: playwright counts only as a word of its own.
	scriptedBrowser = regexp.MustCompile(`(?i)(\bnpx\b|\bnode(\.exe)?\b|python\s+-m|(^|[\s"'])@?playwright(\s|/|$)|puppeteer|\bcdp\b|remote-debugging|mcp__)`)
	disableFeatures = regexp.MustCompile(`(?i)--disable-features[= ]("[^"]*"|'[^']*'|\S+)`)
	bundledBrowser  = regexp.MustCompile(`(?i)(ms-playwright[\\/]+chromium|--user-data-dir[= ]\S*companion)`)
)

func safeBrowserLaunch(command string) bool {
	flags := disableFeatures.FindAllStringSubmatch(command, -1)
	if len(flags) != 1 || !strings.Contains(strings.ToLower(flags[0][1]), "webauthenticationusenativewinapi") {
		return false
	}
	return bundledBrowser.MatchString(command) && !strings.Contains(strings.ToLower(command), "remote-debugging")
}

var (
	psHereString = regexp.MustCompile(`(?s)@(['"])[ \t]*\r?\n.*?\r?\n[ \t]*(['"])@`)
	psRunsText   = regexp.MustCompile(`(?i)\b(iex|invoke-expression|powershell(\.exe)?|pwsh(\.exe)?|cmd(\.exe)?)\b`)
)

// commandText is command as the shell runs it, for the browser rules: without the text a
// command only carries along. That is the body of a heredoc (POSIX) or of a here-string
// (PowerShell), which is a file's content or a script's source, not a command line: `python -
// <<EOF`, `cat > notes.md <<EOF` and `Set-Content x @'...'@` writing the word "browser" launch
// no browser (the owner, 17 Nov: his own held card). A heredoc fed to a shell stays: its lines are
// commands. The other rules read heredocs their own way (analyzer.simple); this is only what
// checkBrowser matches words in.
func commandText(command string, sh Shell) string {
	switch sh {
	case ShellPowerShell:
		if !strings.Contains(command, "@'") && !strings.Contains(command, `@"`) {
			return command
		}
		if psRunsText.MatchString(command) {
			return command // the text may be run (iex, a nested shell): keep every word
		}
		return psHereString.ReplaceAllString(command, "@$1$2@")
	case ShellPOSIX:
		if !strings.Contains(command, "<<") {
			return command
		}
		var b strings.Builder
		docs := false
		for _, c := range lex(command, ShellPOSIX) {
			b.WriteString(c.raw)
			b.WriteByte('\n')
			if c.heredoc == "" {
				continue
			}
			docs = true
			for _, w := range c.words {
				if s, ok := w.literal(); ok && isShell(strings.TrimSuffix(strings.ToLower(path.Base(strings.ReplaceAll(s, `\`, "/"))), ".exe")) {
					b.WriteString(c.heredoc)
					break
				}
			}
		}
		if !docs {
			return command // "<<" was something else (a here-string, arithmetic): match it whole
		}
		return b.String()
	}
	return command
}

// checkBrowser applies the browser rules' states to an actual launch: a browser executable, or
// an automation library or tool that is the thing being run. browser-automation Blocked: a
// launch is a Block. Held: it waits for the owner. Allowed: a direct launch passes only when
// safe (else the fixed browser-unsafe-launch), scripted automation follows its own switch
// (browser-unverified: a virtual authenticator can't be seen in a command line).
//
// A word is not a launch (the owner, 17 Nov: `go test -run TestBrowser...` was denied for its test
// name). A browser word in an argument, a test name, a path, a commit message or file content
// matches no rule, whatever the states are; the former rule browser-mention is gone.
func checkBrowser(ctx Context, command string) Verdict {
	state := ctx.StateOf(RuleBrowser)
	if ctx.States == nil && ctx.AllowBrowserAutomation {
		state = Allow // the older host setting, for callers that pass no states
	}
	switch agentpolicy.ClassifyBrowserCommand(ctx.OS, false, command) {
	case agentpolicy.BrowserCommand:
		switch state {
		case Allow:
			if scriptedBrowser.MatchString(command) || !directBrowser.MatchString(command) {
				v := ruleVerdict(RuleBrowserScript, "scripted browser automation")
				if v.Outcome = ctx.StateOf(RuleBrowserScript); v.Outcome == Allow {
					return Verdict{Outcome: Allow}
				}
				return v
			}
			if !safeBrowserLaunch(command) {
				return ruleVerdict(RuleBrowserUnsafe, "a browser launch without the bundled Chromium and the WebAuthn flag")
			}
			return Verdict{Outcome: Allow}
		case Hold:
			v := ruleVerdict(RuleBrowser, "")
			v.Outcome = Hold
			return v
		}
		return ruleVerdict(RuleBrowser, "")
	}
	return Verdict{Outcome: Allow} // agentpolicy.BrowserMention included: a word, not a launch
}

func ruleVerdict(id, detail string) Verdict {
	r := ruleByID[id]
	return Verdict{Outcome: r.Outcome, Rule: r.ID, RuleName: r.Name, Why: r.Why, Detail: detail}
}
