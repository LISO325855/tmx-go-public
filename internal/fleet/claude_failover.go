package fleet

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/blackcode-ch/tmx-go/internal/harness"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

type claudeAccountOrigin struct {
	account, session string
	confirmed        bool
	resume, rejected bool
}

func (f *Fleet) SetClaudeFailoverSelector(selectAccount func(account, model string) string) {
	f.mu.Lock()
	f.claudeAlternate = selectAccount
	f.mu.Unlock()
}

// FailoverClaudeAccount responds to a limit observation, never to a reset timer.
func (f *Fleet) FailoverClaudeAccount(account string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || f.claudeAlternate == nil || f.opts.Accounts == nil {
		return
	}
	for _, r := range f.agents {
		a := r.a
		if AccountHarness(a.Harness) != model.HarnessClaude || !sameClaudeAccount(a.Account, account) ||
			a.Desired != "running" || r.proc == nil || r.starting || r.stopping || r.accountSwitch || r.accountOrigin != nil || r.waiting > 0 {
			continue
		}
		to := f.claudeAlternate(a.Account, a.Model)
		if to == "" || sameClaudeAccount(to, a.Account) {
			continue
		}
		next := a
		next.Account = to
		if err := f.launchAdmissionLocked(next); err != nil {
			f.log.Warn("claude account failover admission", "agent", a.Name, "err", err)
			continue
		}
		source, destination := f.agentClaudeConfigDirLocked(a), f.agentClaudeConfigDirLocked(next)
		if source == destination {
			continue
		}
		r.accountSwitch = true
		f.goTracked(func() { f.switchClaudeAccount(r, next, source, destination) })
	}
}

func sameClaudeAccount(a, b string) bool {
	return a == b || (a == "" || a == "default") && (b == "" || b == "default")
}

func (f *Fleet) switchClaudeAccount(r *agentRec, next model.Agent, source, destination string) {
	defer func() {
		f.mu.Lock()
		r.accountSwitch = false
		f.mu.Unlock()
	}()
	id := next.ID
	stopping := false
	f.stopProcessIf(f.ctx, id, true, func(current *agentRec) bool {
		stopping = current == r && !f.closed && r.a.Desired == "running" && r.proc != nil && !r.stopping
		return stopping
	})
	if !stopping {
		return
	}
	f.mu.Lock()
	if f.agents[id] != r || f.closed || r.a.Desired != "running" || r.proc != nil || r.hostUnknown || r.hostStopping {
		f.mu.Unlock()
		return
	}
	original, confirmed := r.a, r.sessionConfirmed
	f.mu.Unlock()

	carried, err := copyClaudeAccountConversation(source, destination, original)
	if err != nil || confirmed && !carried {
		if err == nil {
			err = errors.New("the source account has no transcript for the confirmed conversation")
		}
		f.log.Warn("claude account failover left account unchanged", "agent", original.Name, "err", err)
		f.restartClaudeFailoverSource(r, original, confirmed)
		return
	}
	f.mu.Lock()
	if f.agents[id] != r || f.closed || r.a.Desired != "running" || r.proc != nil {
		f.mu.Unlock()
		return
	}
	r.a.Account, r.sessionConfirmed = next.Account, carried
	r.accountOrigin = &claudeAccountOrigin{account: original.Account, session: original.SessionID, confirmed: confirmed, resume: carried}
	f.mu.Unlock()
	opts := startOpts{StartOptions: StartOptions{Force: true}, accountSwitch: true}
	if err := f.startAgentOpts(id, "", opts); err != nil {
		f.log.Warn("claude account failover start failed", "agent", original.Name, "err", err)
		f.restartClaudeFailoverSource(r, original, confirmed)
	}
}

// The alternate's ready event commits the persisted resume path and queues one notice.
func (f *Fleet) confirmClaudeAccountSwitchLocked(r *agentRec) {
	origin := r.accountOrigin
	if origin == nil || origin.rejected || r.a.Desired != "running" || r.stopping || f.closed {
		return
	}
	r.accountOrigin = nil
	f.saveAgentLocked(r)
	h := f.headLocked()
	if h == nil || f.closed {
		return
	}
	m := model.Message{ID: model.NewID(), From: model.WatchParty, To: h.a.ID, State: model.MsgQueued,
		Body:      fmt.Sprintf("Claude account switched for %s from %s to %s.", r.a.Name, claudeAccountLabel(origin.account), r.a.Account),
		CreatedAt: model.Now()}
	if err := f.st.PutMessage(m); err != nil {
		f.log.Warn("claude account failover notice", "agent", r.a.Name, "err", err)
		return
	}
	f.bus.Publish(model.BusEvent{Type: model.EvMessageCreated, AgentID: h.a.ID, Data: m})
	f.refreshQueuedLocked(h)
	f.goTracked(func() { f.tryDeliver(m.To) })
}

func (f *Fleet) restartClaudeFailoverSource(r *agentRec, original model.Agent, confirmed bool) {
	f.mu.Lock()
	if f.agents[original.ID] != r || r.proc != nil {
		f.mu.Unlock()
		return
	}
	r.a.Account, r.a.SessionID, r.sessionConfirmed = original.Account, original.SessionID, confirmed
	r.accountOrigin = nil
	f.saveAgentLocked(r)
	resume := !f.closed && r.a.Desired == "running"
	f.mu.Unlock()
	if resume {
		if err := f.startAgentOpts(original.ID, "", startOpts{StartOptions: StartOptions{Force: true}, accountSwitch: true}); err != nil {
			f.log.Warn("claude account failover source restart", "agent", original.Name, "err", err)
		}
	}
}

func claudeAccountLabel(name string) string {
	if name == "" {
		return "default"
	}
	return name
}

// A returning account can have an older copy. Only replace it when it is an
// exact prefix of the source conversation, so divergent history is never lost.
func copyClaudeAccountConversation(source, destination string, a model.Agent) (bool, error) {
	if a.SessionID == "" {
		return false, nil
	}
	if !conversationID.MatchString(a.SessionID) {
		return false, errors.New("invalid Claude conversation ID")
	}
	name := a.SessionID + ".jsonl"
	src, err := os.Open(filepath.Join(harness.ClaudeProjectDirIn(source, a.Cwd), name))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer src.Close()
	dir := harness.ClaudeProjectDirIn(destination, a.Cwd)
	dst := filepath.Join(dir, name)
	older, err := os.Open(dst)
	if err == nil {
		n, olderErr := older.Stat()
		olderHash, prefixHash := sha256.New(), sha256.New()
		_, hashErr := io.Copy(olderHash, older)
		older.Close()
		if olderErr != nil || hashErr != nil {
			return false, errors.Join(olderErr, hashErr)
		}
		if _, err := io.CopyN(prefixHash, src, n.Size()); err != nil || !bytes.Equal(olderHash.Sum(nil), prefixHash.Sum(nil)) {
			return false, errors.New("the destination has a different Claude conversation")
		}
		if _, err := src.Seek(0, io.SeekStart); err != nil {
			return false, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(dir, "."+a.SessionID+".*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	_, copyErr := io.Copy(tmp, src)
	if err := errors.Join(copyErr, tmp.Close()); err != nil {
		return false, err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return false, err
	}
	return true, nil
}
