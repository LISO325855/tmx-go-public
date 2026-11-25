package fleet

import (
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// The owner acting on a Board row (the owner, 17 Nov 2026 20:58: the Board is to be interactive):
// a quick note, or one tap: park it, not relevant, done / I did the next action, thanks. Each
// writes an event of the owner's on the thread and reaches the thread's agent as owner mail;
// "park" also pauses the thread and "not relevant" archives it (it leaves the Board, the
// record stays).

// Owner acts.
const (
	OwnerActNote        = "note"
	OwnerActPark        = "park"
	OwnerActNotRelevant = "not_relevant"
	OwnerActDone        = "done"
	OwnerActThanks      = "thanks"
)

// ownerActWords is what each act says, on the thread and in the mail.
var ownerActWords = map[string]string{
	OwnerActPark:        "Park it: stop work on this for now.",
	OwnerActNotRelevant: "Not relevant any more: drop it.",
	OwnerActDone:        "Done: I did the next action.",
	OwnerActThanks:      "Thanks.",
}

// ownerActKind is the event kind each act is recorded as.
var ownerActKind = map[string]string{
	OwnerActNote:        model.LogDecision,
	OwnerActPark:        model.EventPause,
	OwnerActNotRelevant: model.LogDecision,
	OwnerActDone:        model.EventResult,
	OwnerActThanks:      model.EventResult,
}

// OwnerActRequest is POST /api/matrix/threads/{id}/owner.
type OwnerActRequest struct {
	Act  string `json:"act"`            // note | park | not_relevant | done | thanks
	Note string `json:"note,omitempty"` // the owner's words; required for "note", optional otherwise
}

// OwnerActResult is the thread after the act and who was told.
type OwnerActResult struct {
	Thread model.Thread `json:"thread"`
	// MailedTo is the agent that got it as owner mail ("" = nobody to tell, or it could not
	// be sent: MailError says why; the event is on the thread either way).
	MailedTo  string `json:"mailed_to,omitempty"`
	MailError string `json:"mail_error,omitempty"`
}

// ownerActText is the line an act leaves: its words, then the owner's note.
func ownerActText(act, note string) string {
	note = strings.TrimSpace(note)
	words := ownerActWords[act]
	switch {
	case words == "":
		return note
	case note == "":
		return words
	default:
		return words + " " + note
	}
}

// OwnerAct records the owner's act on a thread, tells its agent, and sets the status the act
// implies. Only the owner acts here (the route is owner-only).
func (f *Fleet) OwnerAct(idOrTitle string, req OwnerActRequest) (OwnerActResult, error) {
	act := strings.ToLower(strings.TrimSpace(req.Act))
	kind, ok := ownerActKind[act]
	if !ok {
		return OwnerActResult{}, errf(KindInvalid, "act must be note, park, not_relevant, done or thanks")
	}
	text := ownerActText(act, req.Note)
	if text == "" {
		return OwnerActResult{}, errf(KindInvalid, "the note is empty")
	}

	f.mu.Lock()
	t, _, err := f.matrixThreadLocked(OwnerCaller, idOrTitle)
	to := ""
	if err == nil {
		to = f.ownerActRecipientLocked(t)
	}
	f.mu.Unlock()
	if err != nil {
		return OwnerActResult{}, err
	}
	if t.Archived {
		return OwnerActResult{}, errf(KindInvalid, "this thread is archived")
	}

	// The event first: it is the record, and an archived thread takes no more events. "park"
	// records its own (the pause event of the status change, with the same words).
	if act == OwnerActPark && t.Status != model.ThreadPaused {
		if _, err := f.SetState(OwnerCaller, t.ID, StateRequest{Status: model.ThreadPaused, Why: text}); err != nil {
			return OwnerActResult{}, err
		}
	} else if _, _, err := f.AddEntry(OwnerCaller, AddEntryRequest{Thread: t.ID, Kind: kind, Text: text}); err != nil {
		return OwnerActResult{}, err
	}
	if act == OwnerActNotRelevant {
		yes := true
		if _, err := f.UpdateThread(OwnerCaller, t.ID, ThreadPatch{Archived: &yes}); err != nil {
			return OwnerActResult{}, err
		}
	}

	res := OwnerActResult{}
	if to != "" {
		f.mu.Lock()
		// The title is an agent's text inside a message that is labelled as the owner's: one
		// line, cut, its markers made harmless (promptText), so it cannot read as his words.
		title := f.threadTitleLocked(t.ID)
		name := f.nameOfLocked(to)
		f.mu.Unlock()
		if _, err := f.SendOwner(to, "On the thread \""+promptText(title)+"\" ["+t.ID+"], from the owner's Board: "+text, ""); err != nil {
			res.MailError = err.Error()
		} else {
			res.MailedTo = name
		}
	}
	f.mu.Lock()
	res.Thread, _, err = f.matrixThreadLocked(OwnerCaller, t.ID)
	if err == nil {
		res.Thread.Title = f.threadTitleLocked(t.ID)
	}
	f.mu.Unlock()
	return res, err
}

// ownerActRecipientLocked is the agent that answers for a thread: its owner, else its
// project's lead, else the head ("" when none of them exists).
func (f *Fleet) ownerActRecipientLocked(t model.Thread) string {
	if _, ok := f.agents[t.Owner]; ok && t.Owner != "" {
		return t.Owner
	}
	if p := f.projects[t.ProjectID]; p != nil {
		if _, ok := f.agents[p.LeadID]; ok && p.LeadID != "" {
			return p.LeadID
		}
	}
	for id, r := range f.agents {
		if r.a.Level == model.LevelHead {
			return id
		}
	}
	return ""
}
