package accounts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"time"
)

// ResetStatus is safe to display. Intent is stored before invoking the claim
// transport; an interrupted or ambiguous attempt is never automatically retried.
type ResetStatus struct {
	State     string `json:"state"`
	Kind      string `json:"kind,omitempty"`
	Message   string `json:"message"`
	AttemptAt int64  `json:"attempt_at,omitempty"`
}

type resetEvent struct {
	Window int64 `json:"window"`
	Armed  bool  `json:"armed"`
}

type resetAttempt struct {
	Events map[string]resetEvent `json:"events,omitempty"`
	ResetStatus
	Email     string  `json:"email"`
	AccountID string  `json:"account_id"`
	Used      float64 `json:"used"`
	Remaining int     `json:"remaining"`
	Window    int64   `json:"window"`
}

type resetClaim func(context.Context, *scope, Account, string, bool) string

// resetBridge is the injected mutation boundary. Production uses the guarded
// PTY implementation; tests supply a fake without starting Claude.
type resetBridge interface {
	Claim(context.Context, *scope, Account, string, bool) string
}

func (f resetClaim) Claim(ctx context.Context, s *scope, a Account, kind string, owner bool) string {
	return f(ctx, s, a, kind, owner)
}

// OwnerReset is called only by the owner API. Weekly is a one-shot switch,
// never a persistent automatic policy. Account email confirmation prevents a
// stale page from applying an owner's choice to a replacement login.
func (s *Service) OwnerReset(ctx context.Context, name, email, kind string, dryRun bool) (Payload, error) {
	if kind != "weekly" && kind != "fable" {
		return Payload{}, ErrBadName
	}
	e := s.entry(string(Claude), name)
	if e == nil {
		return Payload{}, ErrUnknown
	}
	if !s.owns(e) {
		return Payload{}, ErrNotOwned
	}
	e.loginMu.Lock()
	defer e.loginMu.Unlock()
	s.mu.Lock()
	if s.closed || e.login.State == LoginRunning {
		s.mu.Unlock()
		return Payload{}, ErrSigningIn
	}
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()
	a := s.checkMode(e, dryRun)
	if email == "" || a.Account.Email != email {
		return Payload{}, ErrNotOwned
	}
	a = s.trackReset(ctx, e, a, kind, true, dryRun)
	s.mu.Lock()
	e.acct, e.applied = a, s.o.Now()
	s.changedLocked()
	s.mu.Unlock()
	s.publish()
	return s.Payload(), nil
}

func resetUsage(a Account, kind string) (Limit, bool) {
	id := "week_fable"
	if kind == "weekly" {
		id = "week"
	}
	for _, l := range a.Limits {
		if l.ID == id && !math.IsNaN(l.UsedPercent) && !math.IsInf(l.UsedPercent, 0) && l.UsedPercent >= 0 && l.UsedPercent <= 100 {
			return l, true
		}
	}
	return Limit{}, false
}

func resetRemaining(a Account, kind string, now int64, usable bool) int {
	r := a.FreeResets
	if r == nil || !r.Known || r.CheckedAt > now || now-r.CheckedAt > (2*time.Minute).Milliseconds() {
		return -1
	}
	count := 0
	for _, g := range r.Grants {
		if g.Type == kind && (!usable || g.UsableNow) && (g.ExpiresAt == 0 || g.ExpiresAt > now) {
			count += g.Remaining
		}
	}
	return count
}

func freshResetAccount(a Account, now int64) bool {
	return a.Harness == string(Claude) && a.Connected && a.Account.Method == MethodSubscription && a.Account.Email != "" && a.FreeResets != nil && a.FreeResets.accountID != "" && a.Error == "" && a.LimitsNote == "" && a.CheckedAt <= now && now-a.CheckedAt <= (2*time.Minute).Milliseconds()
}

func (s *Service) resetFile(accountID string) string {
	sum := sha256.Sum256([]byte(accountID))
	return filepath.Join(s.o.Dir, "reset-attempt-"+hex.EncodeToString(sum[:16])+".json")
}

func (s *Service) saveReset(path string, a resetAttempt) error {
	return saveResetWithSync(path, a, s.resetSyncDir)
}
func saveReset(path string, a resetAttempt) error {
	return saveResetWithSync(path, a, syncResetDirectory)
}
func saveResetWithSync(path string, a resetAttempt, syncDir func(string) error) error {
	b, err := json.Marshal(a)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path+".tmp", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := renameReset(path+".tmp", path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

// trackReset's automatic caller always passes fable. Weekly is available only
// through the owner-only one-shot switch. dryRun returns before any claim call.
func (s *Service) trackReset(ctx context.Context, e *entry, a Account, kind string, owner, dryRun bool) (out Account) {
	if kind != "fable" && !(kind == "weekly" && owner) {
		return a
	}
	now := s.now()
	if !freshResetAccount(a, now) {
		return a
	}
	usage, ok := resetUsage(a, kind)
	if !ok {
		return a
	}
	status := func(message string) Account {
		a.Reset = &ResetStatus{State: "needs_you", Kind: kind, Message: message}
		return a
	}
	if dryRun || s.o.ResetDryRun {
		a.Reset = &ResetStatus{State: "dry_run", Kind: kind, Message: "Read-only check: no reset requested."}
		return a
	}
	path := s.resetFile(a.FreeResets.accountID)
	// O_EXCL also prevents a second daemon sharing this account store from claiming.
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return status("A reset attempt is locked. Check the account before manual recovery.")
	}
	syncErr := lock.Sync()
	closeErr := lock.Close()
	if syncErr != nil || closeErr != nil || s.resetSyncDir(filepath.Dir(path)) != nil {
		return status("Reset lock could not be made durable; no reset requested. Manual recovery required.")
	}
	keepLock := false
	defer func() {
		if keepLock {
			return
		}
		if os.Remove(path+".lock") != nil || s.resetSyncDir(filepath.Dir(path)) != nil {
			s.log.Error("reset lock cleanup could not be made durable")
			// Restore a fail-closed marker if deletion happened but could not be
			// flushed. Never report success after a metadata durability failure.
			if f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600); err == nil {
				_ = f.Sync()
				_ = f.Close()
				_ = s.resetSyncDir(filepath.Dir(path))
			}
			out.Reset = &ResetStatus{State: "needs_you", Kind: kind, Message: "Reset lock cleanup was not durable. Manual reconciliation required."}
		}
	}()
	var prior resetAttempt
	events := map[string]resetEvent{}
	b, err := readBoundedFile(path, 16384)
	if err == nil {
		if json.Unmarshal(b, &prior) != nil || prior.Email != a.Account.Email || prior.AccountID != a.FreeResets.accountID {
			return status("Reset history needs manual reconciliation for this account.")
		}
		events = prior.Events
		if events == nil {
			// Old journals consumed an event but did not record its rearm state.
			events = map[string]resetEvent{prior.Kind: {Window: prior.Window, Armed: false}}
		}
		updateResetEvents(events, a)
		prior.Events = events
		if s.saveReset(path, prior) != nil {
			keepLock = true
			return status("Reset event state could not be saved; no reset requested.")
		}
		if prior.State != "succeeded" {
			// A timeout/crash may have spent a grant. Read only until both counters prove it.
			oldUsage, found := resetUsage(a, prior.Kind)
			remaining := resetRemaining(a, prior.Kind, now, false)
			if found && a.FreeResets != nil && a.FreeResets.CheckedAt > prior.AttemptAt && oldUsage.UsedPercent < prior.Used && remaining >= 0 && remaining < prior.Remaining && oldUsage.ResetsAt == prior.Window {
				prior.State, prior.Message = "succeeded", "Free "+prior.Kind+" reset verified."
				if s.saveReset(path, prior) != nil {
					keepLock = true
					return status("Reset verified but history could not be saved. Manual recovery required.")
				}
			}
			a.Reset = &prior.ResetStatus
			return a
		}
		a.Reset = &prior.ResetStatus
		if now-prior.AttemptAt < (15 * time.Minute).Milliseconds() {
			return a
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return status("Reset history could not be read. Manual recovery required.")
	}
	// Every claim, including the first and an owner request, requires genuine fresh
	// 99% usage. Weekly cannot be selected by this threshold alone.
	if usage.UsedPercent < 99 {
		if owner {
			return status("A free reset can only be requested after a fresh 99% usage reading.")
		}
		return a
	}
	if usage.ResetsAt <= now {
		return status("No current quota window confirmed; no reset requested.")
	}
	if event, seen := events[kind]; seen && !event.Armed && !owner {
		return a
	}
	remaining := resetRemaining(a, kind, now, false)
	if remaining <= 0 || resetRemaining(a, kind, now, true) <= 0 {
		return status("No usable free " + kind + " reset confirmed. Check the account manually.")
	}
	events[kind] = resetEvent{Window: usage.ResetsAt, Armed: false}
	attempt := resetAttempt{Events: events, ResetStatus: ResetStatus{State: "needs_you", Kind: kind, AttemptAt: now, Message: "Reset outcome pending verification. Check the account before retrying."}, Email: a.Account.Email, AccountID: a.FreeResets.accountID, Used: usage.UsedPercent, Remaining: remaining, Window: usage.ResetsAt}
	ctx, finishReset, err := s.beginReset(ctx)
	if err != nil {
		return status(err.Error())
	}
	defer finishReset()
	if s.saveReset(path, attempt) != nil {
		keepLock = true
		return status("Reset intent could not be saved; no reset requested.")
	}
	claim := s.resetClaim
	outcome := claim.Claim(ctx, s.scope(e), a, kind, owner)
	s.log.Info("free reset attempt", "account", e.name, "kind", kind, "outcome", outcome)
	// Never infer success from a claimed success; even an ambiguous call is followed
	// by a fresh read, and cannot send a second request during reconciliation.
	after := s.check(e)
	after.Reset = &attempt.ResetStatus
	afterUsage, found := resetUsage(after, kind)
	afterRemaining := resetRemaining(after, kind, s.now(), false)
	if freshResetAccount(after, s.now()) && after.Account.Email == a.Account.Email && found && after.FreeResets != nil && after.FreeResets.accountID == a.FreeResets.accountID && after.FreeResets.CheckedAt > attempt.AttemptAt && afterUsage.UsedPercent < usage.UsedPercent && afterUsage.ResetsAt == usage.ResetsAt && afterRemaining >= 0 && afterRemaining < remaining {
		attempt.State, attempt.Message = "succeeded", "Free "+kind+" reset verified."
		updateResetEvents(attempt.Events, after)
	} else {
		attempt.Message = "Free reset not verified (" + outcome + "). Check usage and offers manually; automatic retry is blocked."
	}
	if s.saveReset(path, attempt) != nil {
		keepLock = true
		attempt.State, attempt.Message = "needs_you", "Reset outcome could not be saved. Manual reconciliation required."
	}
	after.Reset = &attempt.ResetStatus
	return after
}

// Rearm only from a fresh observation below 99 or an identified new window.
// A cooldown by itself never creates another threshold event.
func updateResetEvents(events map[string]resetEvent, a Account) {
	for kind, event := range events {
		l, ok := resetUsage(a, kind)
		if !ok {
			continue
		}
		if l.UsedPercent < 99 || (l.ResetsAt > event.Window && l.ResetsAt > a.CheckedAt) {
			events[kind] = resetEvent{Window: l.ResetsAt, Armed: true}
		}
	}
}
