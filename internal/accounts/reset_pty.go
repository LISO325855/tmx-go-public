package accounts

import (
	"context"
	"strings"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/runner/claude"
	"github.com/blackcode-ch/tmx-go/internal/runner/term/screen"
)

// This is the sole live mutation boundary. No test or read-only run may even
// launch a helper containing /limit-reset. Claims remain in Claude's own client.
func liveResetClaim(ctx context.Context, s *scope, a Account, kind string, owner bool) string {
	if resetTestProcess() || s.o.ResetDryRun || s.resetDryRun {
		return "disabled"
	}
	if kind != "fable" && !(kind == "weekly" && owner) {
		return "unavailable"
	}
	l, ok := resetUsage(a, kind)
	if !ok || l.UsedPercent < 99 || l.ResetsAt <= s.now() || !freshResetAccount(a, s.now()) || resetRemaining(a, kind, s.now(), true) <= 0 {
		return "ineligible"
	}
	path, err := s.lookPath(Claude)
	if err != nil {
		return "unavailable"
	}
	ctx, cancel := context.WithTimeout(ctx, resetClaimTimeout)
	defer cancel()
	auth := s.claudeRun(ctx, s.o.CheckTimeout, path, "auth", "status", "--json")
	st, ok := parseClaudeStatus(auth.out)
	if auth.err != nil || !ok || !st.LoggedIn || st.Email != a.Account.Email {
		return "ineligible"
	}
	// Read-only revalidation immediately before entering the interactive helper.
	offers := s.claudeFreeResets(ctx, a.Account.Email)
	verify := a
	verify.FreeResets = offers
	if offers.accountID != a.FreeResets.accountID || resetRemaining(verify, kind, s.now(), true) <= 0 {
		return "ineligible"
	}
	exited := make(chan struct{})
	ticket, err := claude.BeginStartHeld(ctx, s.env(), claude.HolderOther, 120*time.Second, func() { cancel(); <-exited })
	if err != nil {
		return "unavailable"
	}
	defer ticket.Release()
	defer close(exited)
	if !resetCanConfirm(a, verify, kind, s.now()) {
		return "ineligible"
	}
	helper, err := startLogin(Claude, s.workDir(), path, []string{"--strict-mcp-config", "--no-session-persistence", "/limit-reset " + kind}, s.env(), s.o.ResetDryRun || s.resetDryRun)
	if err != nil {
		return "unavailable"
	}
	defer helper.stop(s.o.StopGrace)
	sub := helper.attach()
	defer helper.detach(sub)
	view := screen.New(loginCols, loginRows, func(b []byte) { _ = helper.write(b) })
	defer view.Close()
	view.Write(sub.Replay)
	confirmed, moved := false, false
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return "unavailable"
		case b, open := <-sub.C():
			if !open {
				return "unavailable"
			}
			view.Write(b)
		case <-tick.C:
			text := strings.Join(view.Lines(), "\n")
			if result := resetScreenResult(text, kind); result != "" {
				return result
			}
			if !confirmed {
				action := resetScreenConfirmation(text, kind)
				if action == "yes" {
					current := s.resetConfirmationRead(ctx, path)
					if !resetCanConfirm(a, current, kind, s.now()) {
						return "ineligible"
					}
					// Consume any redraw produced while the read-only probes ran.
					for len(sub.c) > 0 {
						b, open := <-sub.C()
						if !open {
							return "unavailable"
						}
						view.Write(b)
					}
					if resetScreenConfirmation(strings.Join(view.Lines(), "\n"), kind) != "yes" || !resetCanConfirm(a, current, kind, s.now()) {
						return "ineligible"
					}
					if helper.write([]byte("\r")) != nil {
						return "unavailable"
					}
					confirmed = true
				} else if action == "no" && !moved {
					if helper.write([]byte("\x1b[A")) != nil {
						return "unavailable"
					}
					moved = true
				}
			}
		}
	}
}

// The helper owns the account's start ticket and is idle on its confirmation.
// These read-only commands run within that held ticket; reacquiring it deadlocks.
func (s *scope) resetConfirmationRead(ctx context.Context, path string) Account {
	auth := s.run(ctx, s.o.CheckTimeout, path, "auth", "status", "--json")
	st, ok := parseClaudeStatus(auth.out)
	if auth.err != nil || !ok || !st.LoggedIn {
		return Account{}
	}
	observedAt := s.now()
	u := s.run(ctx, s.o.UsageTimeout, path, claudeUsageArgs...)
	text, ok := claudeResultText(u.out)
	if u.err != nil || !ok {
		return Account{}
	}
	limits, apiKey := parseClaudeUsage(text, s.o.Now())
	if apiKey {
		return Account{}
	}
	return Account{Harness: string(Claude), Connected: true, Account: st.identity(), CheckedAt: observedAt, Limits: limits, FreeResets: s.claudeFreeResets(ctx, st.Email)}
}

// Both the initiating event and the confirmation read must still be fresh,
// refer to the same known unexpired quota window, and identify the same account.
func resetCanConfirm(event, current Account, kind string, now int64) bool {
	before, ok := resetUsage(event, kind)
	after, found := resetUsage(current, kind)
	return ok && found && freshResetAccount(event, now) && freshResetAccount(current, now) &&
		current.CheckedAt >= event.CheckedAt && current.Account.Email == event.Account.Email &&
		current.FreeResets.accountID == event.FreeResets.accountID &&
		before.UsedPercent >= 99 && before.ResetsAt > now && after.ResetsAt == before.ResetsAt && after.UsedPercent >= 99 &&
		resetRemaining(current, kind, now, true) > 0
}

// Exact stock confirmation text only. A changed, paid, wrong-kind or ambiguous
// screen gets no keystrokes. In particular, never press Enter on a generic menu.
func resetScreenConfirmation(text, kind string) string {
	label := "Fable reset"
	if kind == "weekly" {
		label = "weekly reset"
	} else if kind != "fable" {
		return ""
	}
	if !strings.Contains(text, "Use a "+label+"?") && !strings.Contains(text, "use your "+label+" anyway?") {
		return ""
	}
	if !strings.Contains(text, "Yes, use my "+label) || !strings.Contains(text, "No, keep it") {
		return ""
	}
	if strings.Contains(strings.ToLower(text), "extra usage") || strings.Contains(strings.ToLower(text), "purchase") {
		return ""
	}
	selected := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "❯") && !strings.HasPrefix(line, ">") {
			continue
		}
		if selected != "" {
			return ""
		}
		if strings.Contains(line, "Yes, use my "+label) {
			selected = "yes"
		} else if strings.Contains(line, "No, keep it") {
			selected = "no"
		} else {
			return ""
		}
	}
	return selected
}

func resetScreenResult(text, kind string) string {
	if strings.Contains(strings.ToLower(text), "cooldown") {
		return "cooldown"
	}
	if strings.Contains(text, "Couldn't confirm") || strings.Contains(text, "Still couldn't confirm") {
		return "unavailable"
	}
	if kind == "fable" && strings.Contains(text, "Fable reset used") {
		return "reset"
	}
	if kind == "weekly" && strings.Contains(text, "Weekly limits reset") {
		return "reset"
	}
	if strings.Contains(text, "Reset used") {
		return "already_used"
	}
	if strings.Contains(text, "Couldn't reset") {
		return "unavailable"
	}
	if strings.Contains(text, "No Fable reset to use") || strings.Contains(text, "No weekly reset to use") {
		return "ineligible"
	}
	return ""
}
