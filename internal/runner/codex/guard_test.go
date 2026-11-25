package codex

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/guard"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
)

func TestWindowsGuardApprovalRequests(t *testing.T) {
	for _, test := range []struct {
		name, command string
		decision      string
	}{
		{"process", `C:\Windows\System32\taskkill.exe /IM node.exe`, "decline"},
		{"disk", "Clear-Disk -Number 1", "decline"},
		{"partition", "Remove-Partition -DiskNumber 1", "decline"},
		{"boot", `bcdboot C:\Windows`, "decline"},
		{"root ACL", `icacls C:\ /reset /T`, "decline"},
		{"system ACL", `icacls C:\Windows /reset /T`, "decline"},
		{"elevation", "Start-Process pwsh -Verb RunAs", ""},
		{"firewall", "New-NetFirewallRule -DisplayName x -LocalPort 8800", ""},
		{"tailscale", "Stop-Service Tailscale", ""},
		{"allow", "git status", "accept"},
		{"allow variant", "git diff --stat", "accept"},
		{"empty", "", "decline"},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := parser{log: slog.New(slog.NewTextHandler(io.Discard, nil)), pending: map[string]pendingReq{}, items: map[string]wireItem{}, guardContext: &guard.Context{OS: "windows", Cwd: `C:\work\project`, Own: `C:\work\project`}}
			params, _ := json.Marshal(map[string]string{"command": test.command, "cwd": `C:\work\project`})
			var out out
			p.serverRequest(&out, wireMsg{ID: json.RawMessage("17"), Method: mCommandApproval, Params: params})
			if test.decision != "" {
				if len(out.replies) != 1 || len(p.pending) != 0 {
					t.Fatalf("command did not receive an immediate %s", test.decision)
				}
				if !strings.Contains(string(out.replies[0]), `"`+test.decision+`"`) {
					t.Fatalf("reply %s, want %s", out.replies[0], test.decision)
				}
				for _, event := range out.events {
					if event.Kind == runner.EvApproval {
						t.Fatal("guard decision produced a redundant prompt")
					}
				}
			} else {
				found := false
				for _, event := range out.events {
					if event.Kind == runner.EvApproval {
						found = true
					}
				}
				if !found || p.state != model.StateWaiting {
					t.Fatal("hold did not reach owner approval path")
				}
			}
		})
	}
}

func TestGuardConfigMustPassBeforeThreadStarts(t *testing.T) {
	p := parser{log: slog.New(slog.NewTextHandler(io.Discard, nil)), initID: "init", guardExecutable: "tmx.exe", nextID: func() string { return "config" }, threadParams: map[string]any{"cwd": "work"}}
	var result out
	p.response(&result, wireMsg{ID: json.RawMessage(`"init"`), Result: json.RawMessage(`{}`)})
	if len(result.replies) != 2 || p.threadReqID != "" {
		t.Fatal("thread started before config check")
	}
	result = out{}
	p.response(&result, wireMsg{ID: json.RawMessage(`"config"`), Result: json.RawMessage(`{"config":{}}`)})
	if !result.close || p.threadReqID != "" {
		t.Fatal("unverified config started thread")
	}
}
