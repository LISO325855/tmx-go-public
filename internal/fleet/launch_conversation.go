package fleet

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/harness"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// Launching into an existing conversation (LaunchRequest.ConversationID): the new agent takes
// over a conversation the harness already has on this machine for the agent's folder, e.g. one
// that an agent of another tmx or a plain `claude` / `codex` in a terminal held before. The
// conversation is resumed (Claude: --resume ID; Codex: resume ID); nothing is sent to it until
// the launcher gives a prompt. Whoever ran it before must have stopped: tmx does not look for
// or stop a process here (that is adopt's job).

// conversationID is the shape of a Claude or Codex conversation id (a UUID).
var conversationID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// resumeConversationLocked checks that c may launch a into conversation id and that the
// harness has it for a's folder, then makes it a's conversation. Only cheap checks happen
// here, under f.mu. carryRoot is set when Claude's transcript is so far only in the owner's
// ~/.claude: the caller then copies it into that agents' Claude folder with
// copyClaudeTranscript, outside the lock (a transcript can be tens of megabytes).
func (f *Fleet) resumeConversationLocked(c Caller, a *model.Agent, id string) (carryRoot string, err error) {
	// The owner, a paired fleet machine acting for the owner, or the head. Other agents cannot:
	// a conversation carries whatever its earlier owner read.
	allowed := (c.Owner || c.Peer != "") && !c.Companion && c.AgentID == ""
	if !allowed && c.AgentID != "" {
		if me := f.agents[c.AgentID]; me != nil && me.a.Level == model.LevelHead {
			allowed = true
		}
	}
	if !allowed {
		return "", errf(KindForbidden, "only the owner or the head can launch an agent into an existing conversation")
	}
	if !conversationID.MatchString(id) {
		return "", errf(KindInvalid, "%q is not a conversation id", id)
	}
	for _, r := range f.agents {
		if strings.EqualFold(r.a.SessionID, id) {
			return "", errf(KindConflict, "conversation %s is already %s's", id, r.a.Name)
		}
	}
	home, _ := os.UserHomeDir()
	switch a.Harness {
	case model.HarnessClaude:
		// The agents' own Claude folder, or the owner's ~/.claude (specLocked copies it over).
		root, err := f.claudeConfigDirLocked(*a)
		if err != nil {
			return "", errf(KindInternal, "%v", err)
		}
		if !harness.ClaudeTranscriptExistsIn(root, a.Cwd, id) {
			if home == "" || !harness.ClaudeTranscriptExists(home, a.Cwd, id) {
				return "", errf(KindNotFound, "Claude has no conversation %s for the folder %s", id, a.Cwd)
			}
			carryRoot = root
		}
	case model.HarnessCodex:
		codexHome := harness.CodexHome(home)
		for _, kv := range f.accountEnvLocked(*a) { // the agent's account, where it will look
			if v, ok := strings.CutPrefix(kv, "CODEX_HOME="); ok && v != "" {
				codexHome = v
			}
		}
		// Like Claude's: the conversation must have been recorded in this agent's folder.
		if !harness.CodexRolloutIn(codexHome, a.Cwd, id) {
			return "", errf(KindNotFound, "Codex has no conversation %s for the folder %s", id, a.Cwd)
		}
	default:
		return "", errf(KindInvalid, "an existing conversation can be resumed by a claude or codex agent, not %s", a.Harness)
	}
	a.SessionID = id
	return carryRoot, nil
}

// copyClaudeTranscript copies a's conversation from the owner's ~/.claude/projects into root
// (the agents' Claude folder), streamed to a temporary file and renamed into place. The owner's
// file is left as it was. Called without f.mu.
func copyClaudeTranscript(root string, a model.Agent) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	src, err := os.Open(filepath.Join(harness.ClaudeProjectDir(home, a.Cwd), a.SessionID+".jsonl"))
	if err != nil {
		return err
	}
	defer src.Close()
	dir := harness.ClaudeProjectDirIn(root, a.Cwd)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+a.SessionID+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, a.SessionID+".jsonl"))
}
