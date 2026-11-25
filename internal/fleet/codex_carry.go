package fleet

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/harness"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// A Codex agent's stored conversation and its own Codex home.
//
// Under the guard every Codex agent has a Codex home of its own (<agent>/codex-guard/home), so
// that its config and its shims are tmx's. A conversation that was recorded somewhere else (an
// agent moved over from the owner's own ~/.codex, an imported one) is not in that home: Codex
// then starts a new conversation, and the daemon used to store the new id over the old one
// without a word. The owner lost the conversation and never learned it.
//
// So, before a start (like the copy of a Claude transcript, harness.go carryClaudeTranscript):
//   - the rollout of the stored conversation is looked for in the agent's Codex home; if it is
//     not there, in the Codex home of the agent's account and in the owner's own, and copied
//     over (the original is left as it is);
//   - if it is found nowhere, the timeline says so, and the stored id is KEPT: whatever new
//     conversation Codex opens for this start is reported in the timeline but not stored in
//     its place (events.go), so the pointer to the owner's conversation is not lost.

// codexAgentHome is the Codex home of an agent whose guard folder is guardHome.
func codexAgentHome(guardHome string) string { return filepath.Join(guardHome, "home") }

// carryCodexRolloutLocked runs before a start of r (a Codex agent with a stored conversation
// and a Codex home of its own). It reports whether the conversation is in agentHome afterwards.
func (f *Fleet) carryCodexRolloutLocked(r *agentRec, agentHome string) bool {
	a := r.a
	if a.SessionID == "" || agentHome == "" {
		return true
	}
	if harness.CodexRolloutExists(agentHome, a.SessionID) {
		r.codexLost = false
		return true
	}
	var homes []string
	for _, kv := range f.accountEnvLocked(a) { // the agent's account, where its conversations were recorded
		if v, ok := strings.CutPrefix(kv, "CODEX_HOME="); ok && v != "" {
			homes = append(homes, v)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		homes = append(homes, harness.CodexHome(home))
	}
	for _, h := range homes {
		src := harness.CodexRolloutPath(h, a.SessionID)
		if src == "" {
			continue
		}
		rel, err := filepath.Rel(h, src)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		if err := copyFilePrivate(src, filepath.Join(agentHome, rel)); err != nil {
			f.log.Warn("copy a Codex conversation into the agent's Codex home", "agent", a.Name, "err", err)
			continue
		}
		r.codexLost = false
		f.appendLocked(r, model.TimelineEntry{Kind: model.EntrySystem,
			Text: "Conversation " + a.SessionID + " was copied from your own Codex folder into this agent's, so that it can be resumed."})
		return true
	}
	if !r.codexLost { // said once, not at every start
		f.appendLocked(r, model.TimelineEntry{Kind: model.EntryError, IsError: true,
			Text: "Codex has no conversation " + a.SessionID + " in this agent's Codex folder, and it is not in yours either: this start opens a new conversation. " +
				a.SessionID + " stays stored as this agent's conversation; put its rollout file back to resume it."})
	}
	r.codexLost = true
	return false
}

// copyFilePrivate copies src to dst (its folders made, private), through a temporary file.
func copyFilePrivate(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".carry-*")
	if err != nil {
		return err
	}
	_, err = io.Copy(tmp, in)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}
