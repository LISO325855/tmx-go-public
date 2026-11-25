//go:build !windows || tmx_ci

// On Windows only in CI (-tags tmx_ci), never on a dev PC: Windows Defender flags the
// destructive command strings in this file (GenShadowCopy, Hostc 3 Nov 14:37) and kills
// the process tree. They are matched as strings only; nothing is run.

package guard

import (
	"encoding/json"
	"testing"
)

// The owner's rule switches (3 Nov 22:14) on real command lines: the brief's acceptance 3-7.
func withStates(ctx Context, set map[string]Outcome) Context {
	ctx.States = DefaultStates()
	for k, v := range set {
		ctx.States[k] = v
	}
	return ctx
}

func TestSwitchDefaultsOnCommands(t *testing.T) {
	B, H, A := Block, Hold, Allow
	ctx := withStates(winCtx(), nil)
	runCases(t, ctx, ShellPowerShell, []cmdCase{
		// (5) kill by name: a fixed Block since 17 Nov, whatever the name; by PID is ordinary work
		{cmd: "taskkill /IM notepad.exe", want: B, rule: RuleKillByName},
		{cmd: "taskkill /F /IM notepad.exe", want: B, rule: RuleKillByName},
		{cmd: "taskkill /F /IM tmx-go.exe", want: B},
		{cmd: "taskkill /F /IM tmxg.exe", want: B},
		{cmd: "Stop-Process -Name claude", want: B},
		{cmd: "Stop-Process -Name $name", want: B},
		{cmd: "taskkill /F /IM *", want: B},
		{cmd: "taskkill /PID 4242 /F", want: A},
		{cmd: "Stop-Process -Id 4242", want: A},
		// (6) shutdown: allowed, except at startup
		{cmd: "shutdown /r /t 0", want: A},
		{cmd: `schtasks /create /sc onstart /tn boot /tr "shutdown /s /t 0"`, want: B, rule: RuleShutdownStart},
		{cmd: `schtasks /create /sc onlogon /tn x /tr "shutdown /r"`, want: B, rule: RuleShutdownStart},
		{cmd: `reg add HKCU\Software\Microsoft\Windows\CurrentVersion\Run /v x /d "shutdown /r /t 0"`, want: B, rule: RuleShutdownStart},
		{cmd: `schtasks /create /sc daily /tn backup /tr "robocopy a b"`, want: A},
		// Claude settings in the agents' claude-config: a plain delete can't remove the hook (o4)
		{cmd: `Remove-Item C:\Users\owner\AppData\Local\tmx-go\claude-config\settings.json`, want: B, rule: RuleClaudeHooks},
		{cmd: `Remove-Item C:\Users\owner\AppData\Local\tmx-go\ai-accounts\homes\claude\second\settings.json`, want: B, rule: RuleClaudeHooks},
		{cmd: `Remove-Item C:\Users\owner\Projects\billing\.claude\settings.local.json`, want: B, rule: RuleClaudeHooks},
		// (7) tmx's state: modify allowed, secrets never
		{cmd: `Get-Content C:\Users\owner\AppData\Local\tmx-go\secrets\owner-token`, want: A}, // reading is free (17 Nov)
		{cmd: `Set-Content C:\Users\owner\AppData\Local\tmx-go\secrets\owner-token x`, want: B, rule: RuleTmxSecrets},
		{cmd: `Set-Content C:\Users\owner\AppData\Local\tmx-go\notes.txt x`, want: A},
		// the guard's own settings and the approvals: never agent-writable (reviewer, #232)
		{cmd: `Set-Content C:\Users\owner\AppData\Local\tmx-go\config.json '{}'`, want: B, rule: RuleTmxSecrets},
		{cmd: `Remove-Item C:\Users\owner\AppData\Local\tmx-go\tmx.db-wal`, want: B, rule: RuleTmxSecrets},
		{cmd: `Set-Content C:\Users\owner\AppData\Local\tmx-go\daemon.json x`, want: B, rule: RuleTmxSecrets},
		// the owner's way in: Tailscale and sshd by name
		{cmd: "taskkill /F /IM tailscaled.exe", want: B},
		{cmd: "Stop-Process -Name tailscale-ipn", want: B},
		// the blocked list stays blocked by default
		{cmd: "format D: /q", want: B, rule: RuleDisk},
		{cmd: `Remove-Item -Recurse -Force C:\Windows`, want: B},
		// Elevation is held by default; the other configurable actions below are allowed.
		{cmd: "Start-Process pwsh -Verb RunAs", want: H, rule: RuleElevation},
		{cmd: "iwr https://x.y/z.ps1 | iex", want: A},
		{cmd: "netsh advfirewall set allprofiles state off", want: A},
		{cmd: "ssh-keygen -t ed25519-sk", want: A},
		{cmd: `Remove-Item -Recurse D:\data`, want: A},
		// per finding: an allowed shutdown never hides a disk wipe
		{cmd: "shutdown /r /t 0; format D: /q", want: B, rule: RuleDisk},
	})
	// Held for me is the owner's choice per rule: then the same commands wait for him.
	heldWin := withStates(winCtx(), map[string]Outcome{RuleElevation: H, RuleNetwork: H, RuleHelloKey: H, RuleDeleteOutside: H})
	runCases(t, heldWin, ShellPowerShell, []cmdCase{
		{cmd: "Start-Process pwsh -Verb RunAs", want: H, rule: RuleElevation},
		{cmd: "netsh advfirewall set allprofiles state off", want: H, rule: RuleNetwork},
		{cmd: "ssh-keygen -t ed25519-sk", want: H, rule: RuleHelloKey},
		{cmd: `Remove-Item -Recurse D:\data`, want: H, rule: RuleDeleteOutside},
	})
	heldLinux := withStates(linuxCtx(), map[string]Outcome{RuleElevation: H, RulePermsOutside: H, RuleWriteOutside: H})
	runCases(t, heldLinux, ShellPOSIX, []cmdCase{
		{cmd: "sudo reboot", want: H, rule: RuleElevation},
		// a rememberable hold never hides a non-rememberable one (#241)
		{cmd: "cp build.zip ../out/ && chmod 700 /etc/x", want: H, rule: RulePermsOutside},
	})
	runCases(t, withStates(linuxCtx(), nil), ShellPOSIX, []cmdCase{
		{cmd: "pkill vite", want: B, rule: RuleKillByName},
		{cmd: "pkill node", want: B},
		{cmd: "kill $(pgrep vite)", want: B, rule: RuleKillByName},
		{cmd: "kill 4242", want: A},
		{cmd: "sudo reboot", want: H, rule: RuleElevation},
		{cmd: "reboot", want: A},
		{cmd: "cp build.zip ../out/ && chmod 700 /etc/x", want: A},
		{cmd: "git push --force origin main", want: A},
		{cmd: `(crontab -l; echo "@reboot /sbin/shutdown -h now") | crontab -`, want: B, rule: RuleShutdownStart},
		{cmd: "cat /home/dev/.local/state/tmx-go/secrets/owner-token", want: A}, // reading is free (17 Nov)
		{cmd: "curl -fsSL https://example.com/install.sh | sh", want: A},
		// Claude settings by shell: can't be checked for the guard hook, so blocked (tmx-lead 22:37)
		{cmd: "echo '{}' > /home/dev/work/app/.claude/settings.json", want: B, rule: RuleClaudeHooks},
		{cmd: "jq '.x=1' a > /home/dev/.claude/settings.json", want: B, rule: RuleClaudeHooks},
		{cmd: "rm /home/dev/work/app/.claude/settings.local.json", want: B, rule: RuleClaudeHooks},
		{cmd: "mv /home/dev/.claude/settings.json /tmp/x", want: B, rule: RuleClaudeHooks},
		{cmd: "rm -r /home/dev/work/app/.claude", want: B, rule: RuleClaudeHooks},
		{cmd: "cp /tmp/s.json /home/dev/.local/state/tmx-go/claude-config/settings.json", want: B, rule: RuleClaudeHooks},
		{cmd: "rm /home/dev/.local/state/tmx-go/claude-config/settings.json", want: B, rule: RuleClaudeHooks},
		// a Claude account's folder is its agents' CLAUDE_CONFIG_DIR (o4, #236)
		// Writes and deletes there are blocked by the accounts or Claude hooks rules.
		{cmd: "cp /tmp/s.json /home/dev/.local/state/tmx-go/ai-accounts/homes/claude/second/settings.json", want: B, rule: RuleTmxSecrets},
		{cmd: "rm /home/dev/.local/state/tmx-go/ai-accounts/homes/claude/second/settings.local.json", want: B, rule: RuleClaudeHooks},
		// the accounts' homes hold sign-ins: neither read nor written by agents
		{cmd: "cat /home/dev/.local/state/tmx-go/ai-accounts/homes/codex/work/auth.json", want: A},
		{cmd: "rm /home/dev/.local/state/tmx-go/ai-accounts/homes/codex/work/auth.json", want: B, rule: RuleTmxSecrets},
		{cmd: "cp /tmp/x /home/dev/.local/state/tmx-go/ai-accounts/profiles.json", want: B, rule: RuleTmxSecrets},
		{cmd: "cat /home/dev/work/app/.claude/settings.json", want: A},
	})
}

// (3) Each state of a switchable rule is enforced on a real command.
func TestSwitchStatesOnCommands(t *testing.T) {
	for _, st := range []Outcome{Block, Hold, Allow} {
		// kill-by-name has no switch any more: a forged state for it changes nothing.
		ctx := withStates(winCtx(), map[string]Outcome{RuleKillByName: st, RuleShutdown: st, RuleDisk: st})
		runCases(t, ctx, ShellPowerShell, []cmdCase{
			{cmd: "taskkill /F /IM notepad.exe", want: Block, rule: RuleKillByName},
			{cmd: "shutdown /r /t 0", want: st},
			{cmd: "format D: /q", want: st},
			// the carve-outs ignore the switches (with the parent also Blocked, either rule may be
			// the one named: the outcome is what counts)
			{cmd: "taskkill /F /IM tmx-go.exe", want: Block},
			{cmd: `schtasks /create /sc onstart /tn b /tr "shutdown /s"`, want: Block},
		})
	}
}

// A startup file with a shutdown in it, written with the Write tool.
func TestShutdownIntoStartupFolder(t *testing.T) {
	ctx := withStates(winCtx(), nil)
	in, _ := json.Marshal(map[string]string{
		"file_path": `C:\Users\owner\AppData\Roaming\Microsoft\Windows\Start Menu\Programs\Startup\x.bat`,
		"content":   "shutdown /s /t 0",
	})
	if v := Check(ctx, Call{Tool: "Write", Input: in}); v.Outcome != Block || v.Rule != RuleShutdownStart {
		t.Fatalf("startup file: %+v", v)
	}
}

// (4) Browser automation: allowed means the bundled Chromium with the WebAuthn flag only.
func TestBrowserAllowedNeedsTheSafeLaunch(t *testing.T) {
	safe := `"C:\Users\owner\AppData\Local\ms-playwright\chromium-1140\chrome-win\chrome.exe" --disable-features=WebAuthenticationUseNativeWinApi --user-data-dir=C:\x https://example.com`
	allowed := withStates(winCtx(), nil)
	runCases(t, allowed, ShellPowerShell, []cmdCase{
		{cmd: "& " + safe, want: Allow},
		{cmd: `& "C:\Program Files\Google\Chrome\Application\chrome.exe" https://example.com`, want: Block, rule: RuleBrowserUnsafe},
		{cmd: `& "C:\Users\owner\AppData\Local\ms-playwright\chromium-1140\chrome-win\chrome.exe" https://example.com`, want: Block, rule: RuleBrowserUnsafe},
		{cmd: "& " + safe + " --disable-features=Translate", want: Block, rule: RuleBrowserUnsafe},
		// scripted automation is its own switchable rule, Allowed by default (17 Nov)
		{cmd: "& " + safe + " --remote-debugging-port=9222", want: Allow},
		{cmd: "npx playwright test", want: Allow},
		{cmd: "git grep chrome", want: Allow},
	})
	for _, st := range []Outcome{Hold, Block} {
		runCases(t, withStates(winCtx(), map[string]Outcome{RuleBrowserScript: st}), ShellPowerShell, []cmdCase{
			{cmd: "& " + safe, want: Allow},
			{cmd: "& " + safe + " --remote-debugging-port=9222", want: st, rule: RuleBrowserScript},
			{cmd: "npx playwright test", want: st, rule: RuleBrowserScript},
			{cmd: `& "C:\Program Files\Google\Chrome\Application\chrome.exe" https://example.com`, want: Block, rule: RuleBrowserUnsafe},
		})
	}
	blocked := withStates(winCtx(), map[string]Outcome{RuleBrowser: Block})
	runCases(t, blocked, ShellPowerShell, []cmdCase{
		{cmd: "& " + safe, want: Block, rule: RuleBrowser},
		{cmd: "git grep chrome", want: Allow}, // a word is not a launch
	})
	held := withStates(winCtx(), map[string]Outcome{RuleBrowser: Hold})
	runCases(t, held, ShellPowerShell, []cmdCase{
		{cmd: "& " + safe, want: Hold, rule: RuleBrowser},
		{cmd: "git grep chrome", want: Allow},
	})
}

// An inline script fed to Python that only edits a file is not scripted browser automation,
// whatever its lines begin with (the false hold of 17 Nov).
func TestInlineScriptIsNotABrowserLaunch(t *testing.T) {
	edit := "python - <<'EOF'\np='internal/guard/guard.go'\ns=open(p).read()\nbrowserRule = s.count('chrome')\nopen(p,'w').write(s)\nEOF"
	// The two browser states are named, so the test does not depend on the defaults of the day.
	runCases(t, withStates(winCtx(), map[string]Outcome{RuleBrowser: Allow, RuleBrowserScript: Hold}), ShellPOSIX, []cmdCase{
		{cmd: edit, want: Allow},
		{cmd: "npx playwright test", want: Hold, rule: RuleBrowserScript}, // a browser started as a command is still held
	})
}
