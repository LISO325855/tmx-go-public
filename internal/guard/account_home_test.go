package guard

import (
	"encoding/json"
	"testing"
)

// tmx's state folder: reading is free everywhere (17 Nov); writing is blocked on the secrets,
// sign-ins, settings and database (tmx-secrets), except in the agent's own data folders of its own
// account home, where the harness keeps its conversations, memory, plans and todos. Harmless command strings only, so
// this file runs on a dev PC too (the other command tests are CI-only on Windows).

func accountCtx(os string) (ctx Context, home, other string) {
	if os == "windows" {
		state := `C:\Users\owner\AppData\Local\tmx-go`
		return Context{OS: "windows", Home: `C:\Users\owner`, Own: `C:\Users\owner\Projects\billing`,
			Workspace: `C:\Users\owner\Projects`, StateDir: state, Temp: []string{`C:\Users\owner\AppData\Local\Temp`},
			States: DefaultStates()}, state + `\ai-accounts\homes\claude\second`, state + `\ai-accounts\homes\claude\third`
	}
	state := "/home/dev/.local/state/tmx-go"
	return Context{OS: "linux", Home: "/home/dev", Own: "/home/dev/work/app", Workspace: "/home/dev/work",
		StateDir: state, Temp: []string{"/tmp"}, States: DefaultStates()}, state + "/ai-accounts/homes/claude/second", state + "/ai-accounts/homes/claude/third"
}

func TestOwnAccountHomeData(t *testing.T) {
	type tc struct {
		cmd  string
		want Outcome
	}
	run := func(t *testing.T, ctx Context, sh Shell, cases []tc) {
		t.Helper()
		for _, c := range cases {
			v := CheckCommand(ctx, sh, c.cmd)
			if v.Outcome != c.want || c.want == Block && v.Rule != RuleTmxSecrets {
				t.Errorf("%q: got %s/%s (%s), want %s", c.cmd, v.Outcome, v.Rule, v.Detail, c.want)
			}
		}
	}

	ctx, home, other := accountCtx("linux")
	own := []tc{
		{"cat " + home + "/projects/-home-dev-work-app/memory/MEMORY.md", Allow},
		{"ls " + home + "/projects", Allow},
		{"echo note >> " + home + "/projects/-home-dev-work-app/memory/notes.md", Allow},
		{"cat " + home + "/todos/x.json", Allow},
		{"cat " + home + "/plans/plan.md", Allow},
	}
	// Reading is free everywhere in tmx's state (the owner, 17 Nov: the read block is removed, not
	// narrowed); writes, deletes and permission changes outside the agent's own data stay blocked.
	closed := []tc{
		{"cat " + home + "/.credentials.json", Allow},
		{"ls " + home, Allow},
		{"cat " + other + "/projects/-home-dev-work-app/memory/MEMORY.md", Allow},
		{"cat /home/dev/.local/state/tmx-go/ai-accounts/homes/codex/work/auth.json", Allow},
		{"cat /home/dev/.local/state/tmx-go/secrets/owner-token", Allow},
		{"cp /home/dev/.local/state/tmx-go/secrets/owner-token /tmp/x", Allow},
		{"tar czf /tmp/s.tgz /home/dev/.local/state/tmx-go/secrets", Block}, // archive tools never touch the state folder (18 Nov)
		{"ls /home/dev/.local/state/tmx-go/whatsapp/media", Allow},
		{"cp /tmp/s.json " + home + "/settings.json", Block},
		{"echo x > " + home + "/.credentials.json", Block},
		{"echo x > " + home + "/projects/../.credentials.json", Block},
		{"echo x >> " + other + "/projects/-home-dev-work-app/memory/MEMORY.md", Block},
		{"echo x > /home/dev/.local/state/tmx-go/secrets/owner-token", Block},
		{"rm /home/dev/.local/state/tmx-go/secrets/owner-token", Block},
		{"rm /home/dev/.local/state/tmx-go/tmx.db-wal", Block},
		{"echo '{}' > /home/dev/.local/state/tmx-go/config.json", Block},
		{"chmod 777 /home/dev/.local/state/tmx-go/secrets", Block},
		{"mv /home/dev/.local/state/tmx-go /tmp/gone", Block},
	}
	// Without an account home, the agent's own data folder is closed to writes like the rest.
	t.Run("no account home", func(t *testing.T) {
		all := []tc{{own[2].cmd, Block}}
		run(t, ctx, ShellPOSIX, append(all, closed...))
	})
	t.Run("own account home", func(t *testing.T) {
		ctx.AccountHome = home
		run(t, ctx, ShellPOSIX, append(append([]tc{}, own...), closed...))
	})
	// Only an account home inside the state counts: a wider folder opens nothing.
	for name, wide := range map[string]string{
		"all homes": "/home/dev/.local/state/tmx-go/ai-accounts/homes", "one harness": "/home/dev/.local/state/tmx-go/ai-accounts/homes/claude",
		"the state": "/home/dev/.local/state/tmx-go", "the secrets": "/home/dev/.local/state/tmx-go/secrets", "elsewhere": "/home/dev",
		"below an account": home + "/projects",
	} {
		t.Run("wide: "+name, func(t *testing.T) {
			ctx.AccountHome = wide
			run(t, ctx, ShellPOSIX, []tc{
				{"echo x > " + other + "/.credentials.json", Block},
				{"echo x > " + home + "/.credentials.json", Block},
				{"echo x > /home/dev/.local/state/tmx-go/secrets/owner-token", Block},
			})
		})
	}

	t.Run("windows", func(t *testing.T) {
		ctx, home, other := accountCtx("windows")
		ctx.AccountHome = home
		run(t, ctx, ShellPowerShell, []tc{
			{`Get-Content ` + home + `\projects\C--Users-owner-Projects-billing\memory\MEMORY.md`, Allow},
			{`Get-ChildItem ` + home + `\projects`, Allow},
			{`Get-Content ` + home + `\.credentials.json`, Allow},
			{`Get-Content ` + other + `\projects\x\memory\MEMORY.md`, Allow},
			{`Get-Content C:\Users\owner\AppData\Local\tmx-go\secrets\owner-token`, Allow},
			{`Set-Content C:\Users\owner\AppData\Local\tmx-go\secrets\owner-token x`, Block},
			{`Set-Content ` + other + `\projects\x\memory\MEMORY.md x`, Block},
		})
	})

	// The file tools: Claude writes its memory with Write.
	t.Run("file tools", func(t *testing.T) {
		ctx, home, other := accountCtx("linux")
		ctx.AccountHome = home
		write := func(p string) Verdict {
			in, _ := json.Marshal(map[string]string{"file_path": p, "content": "a note"})
			return Check(ctx, Call{Tool: "Write", Input: in})
		}
		if v := write(home + "/projects/-home-dev-work-app/memory/notes.md"); v.Outcome != Allow {
			t.Errorf("own memory file: %+v", v)
		}
		for _, p := range []string{home + "/.credentials.json", home + "/settings.json", other + "/projects/x/memory/notes.md"} {
			if v := write(p); v.Outcome != Block {
				t.Errorf("%s: %+v", p, v)
			}
		}
	})
}
