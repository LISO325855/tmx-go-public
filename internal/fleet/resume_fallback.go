package fleet

import (
	"errors"
	"strconv"
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/store"
)

// A resume the harness refuses because it no longer has the conversation (the transcript was
// deleted or moved, the Claude folder changed) fails the same way on every attempt: restarting
// it is a crash loop that ends in "start it deliberately", which fails again. Such an exit gets
// one fresh start instead: the agent keeps its name, brief and place, loses the old
// conversation's context, and its timeline says so.

// lostConversationMarks are the harnesses' own words for "I do not have that conversation", as
// they appear in the process's last error output (lower case). Only messages seen for real go
// here: a wrong match would throw a conversation away.
var lostConversationMarks = []string{
	"no conversation found with session id", // claude --resume <id>
}

// conversationLost reports whether a process's exit error says the harness does not have the
// conversation it was asked to resume.
func conversationLost(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, m := range lostConversationMarks {
		if strings.Contains(text, m) {
			return true
		}
	}
	return false
}

// freshAfterLostConversationLocked decides, for an unexpected exit, whether r starts once more
// with a new conversation instead of the usual crash recovery. It reports true when it took
// the exit over: the session id is cleared, the timeline has a line, and the caller starts the
// agent again. At most once per agent in the daemon's lifetime: a harness that says the same
// about a brand-new conversation is broken in another way, and the usual limits apply.
func (f *Fleet) freshAfterLostConversationLocked(r *agentRec, err error) bool {
	if r.freshFallback || r.a.SessionID == "" || !conversationLost(err) {
		return false
	}
	r.freshFallback = true
	old := r.a.SessionID
	r.a.SessionID = ""
	r.sessionConfirmed = false
	f.appendLocked(r, model.TimelineEntry{Kind: model.EntrySystem,
		Text: "Previous conversation " + old + " not found by " + string(r.a.Harness) + "; started fresh. Its earlier context is not in this conversation."})
	f.requeueUnconsumedMailLocked(r)
	return true
}

// requeueUnconsumedMailLocked puts the mail that was given to the process that just failed back
// into the queue, for the fresh start to get. Claimed mail is never replayed (store.ClaimMail),
// with this one exception, because here nothing can have been consumed: the harness refused
// the conversation the mail was written to, so no turn ran. Only plain mail claimed since this
// daemon started that process; a message that is a task receipt keeps its own outcome (an exit
// ends it as interrupted and its sender is told), and a reattached process is left alone.
func (f *Fleet) requeueUnconsumedMailLocked(r *agentRec) {
	if r.procStarted == 0 {
		return
	}
	isTask := func(id string) bool {
		_, err := f.st.Task(id)
		return !errors.Is(err, store.ErrNotFound) // a task, or unknown: leave it
	}
	ids, err := f.st.RequeueUnconsumedMail(r.a.ID, r.procStarted, model.Now(), isTask)
	if err != nil {
		f.log.Error("requeue mail after a lost conversation", "agent", r.a.Name, "err", err)
		return
	}
	if len(ids) == 0 {
		return
	}
	for _, id := range ids {
		if m, err := f.st.GetMessage(id); err == nil {
			f.bus.Publish(model.BusEvent{Type: model.EvMessageUpdated, AgentID: r.a.ID, Data: m})
		}
	}
	if n, err := f.st.CountQueued(r.a.ID); err == nil {
		r.a.Queued = n
	}
	what := "1 message"
	if len(ids) > 1 {
		what = strconv.Itoa(len(ids)) + " messages"
	}
	f.appendLocked(r, model.TimelineEntry{Kind: model.EntrySystem,
		Text: what + " given to the start that failed went back to the queue and will be delivered again."})
}

// dropPhantomSessionLocked: before a real start (never a reattach), a Claude conversation that
// was reported by its process but never written is not resumed. Claude names a conversation in
// its first output, before any turn; if that process ended without one (it was not signed in
// yet, it crashed at once), the id is "confirmed" here but Claude has no transcript for it, and
// --resume can only fail with "No conversation found". Such a conversation is started as new,
// under the same id.
//
// It must never drop a conversation that holds something. So both have to be true: no
// transcript for it in the agents' Claude folder (after the usual copy from the owner's
// ~/.claude), AND the agent has never produced an answer in its whole timeline. An agent that
// ever answered keeps resuming; if that fails, the exit handler's one fresh start applies.
func (f *Fleet) dropPhantomSessionLocked(r *agentRec) {
	a := r.a
	if !r.sessionConfirmed || a.SessionID == "" || (a.Harness != model.HarnessClaude && a.Harness != "") {
		return
	}
	root, err := f.claudeConfigDirLocked(a)
	if err != nil {
		return
	}
	carryClaudeTranscript(root, a)
	if claudeConversationExists(root, a) {
		return
	}
	answered, err := f.st.HasTimelineKind(a.ID, string(model.EntryAssistant))
	if err != nil || answered {
		return
	}
	r.sessionConfirmed = false
	f.appendLocked(r, model.TimelineEntry{Kind: model.EntrySystem,
		Text: "Conversation " + a.SessionID + " was never written by " + string(model.HarnessClaude) + " and never answered: starting it as a new conversation."})
}
