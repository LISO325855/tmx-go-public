package fleet

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// Thread logs and the board's order (DESIGN.md "Board"). A log entry is one short line worth
// keeping (a decision, an action, information, a link), filed by the owner (anywhere), the
// head (anywhere) or a project's agents (their project's thread). Entries are never edited:
// a correction is a new entry naming the one it corrects. Project agents start with their
// project's log in their system prompt and read it again with thread_read.

const (
	// maxEntryText: a ceiling far out of reach of real work (256 KB), against a runaway only.
	// Until 17 Nov a log line was 400 characters and three lines; the owner: no caps on what
	// agents write, that is their judgment; long text is folded by the UI.
	maxEntryText = 256 << 10
	maxEntryLink = 500
	// promptEntries is how much of its project's log an agent starts with.
	promptEntries = 12
)

// AddEntryRequest is POST /api/board/log and the thread_add tool.
type AddEntryRequest struct {
	Thread   string `json:"thread,omitempty"` // ID or title; default: the head's current topic, an agent's project
	Kind     string `json:"kind"`
	Text     string `json:"text"`
	Link     string `json:"link,omitempty"`
	Card     int64  `json:"card,omitempty"`
	Corrects string `json:"corrects,omitempty"`
}

// logThreadLocked is the thread a caller files in or reads: the owner names one; the head
// names one or uses its current topic; an agent in a project uses its project's; any agent may name a
// thread it is on (the Matrix), and one outside a project defaults to the newest it joined.
func (f *Fleet) logThreadLocked(c Caller, name string) (model.Thread, *agentRec, error) {
	r, err := f.callerRecLocked(c)
	if err != nil {
		return model.Thread{}, nil, err
	}
	switch {
	case r == nil: // the owner
		if strings.TrimSpace(name) == "" {
			return model.Thread{}, nil, errf(KindInvalid, "say which topic")
		}
		t, err := f.threadByLocked(name)
		return t, nil, err
	case isHead(r):
		if strings.TrimSpace(name) == "" {
			name = threadOr(r.thread)
		}
		t, err := f.threadByLocked(name)
		return t, r, err
	}
	// The Matrix: an agent may log in a thread it is on (launched for it, or put on it).
	if strings.TrimSpace(name) != "" {
		if t, err := f.threadByLocked(name); err == nil && f.onThreadLocked(r, t) {
			return t, r, nil
		}
	} else if r.a.ProjectID == "" {
		if links, _ := f.st.LinksTo(model.AxisAgent, r.a.ID); len(links) > 0 {
			t, err := f.st.GetThread(links[len(links)-1].ThreadID) // the newest
			return t, r, err
		}
	}
	switch {
	case r.a.ProjectID != "":
		id := f.projectThreadLocked(r.a.ProjectID)
		if id == "" {
			return model.Thread{}, nil, errf(KindNotFound, "your project has no thread")
		}
		t, err := f.st.GetThread(id)
		if err != nil {
			return model.Thread{}, nil, err
		}
		if name != "" && name != t.ID && !strings.EqualFold(strings.TrimSpace(name), f.threadTitleLocked(t.ID)) {
			return model.Thread{}, nil, errf(KindForbidden, "you can only use your project's log (%s)", f.threadTitleLocked(t.ID))
		}
		return t, r, nil
	default:
		return model.Thread{}, nil, errf(KindForbidden, "only the head, agents in a project and agents on a thread keep logs")
	}
}

// entryID is a hash of the thread, kind, normalised text and day (UTC).
func entryID(thread, kind, text string, at int64) string {
	norm := strings.ToLower(strings.Join(strings.Fields(text), " "))
	day := time.UnixMilli(at).UTC().Format("2006-01-02")
	sum := sha256.Sum256([]byte(thread + "\x00" + kind + "\x00" + norm + "\x00" + day))
	return hex.EncodeToString(sum[:8])
}

// AddEntry files a line in a thread's log. It reports whether the entry is new (the same
// line filed again the same day returns the existing one).
func (f *Fleet) AddEntry(c Caller, req AddEntryRequest) (model.ThreadEntry, bool, error) {
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if !slices.Contains(model.EventKinds, kind) {
		return model.ThreadEntry{}, false, errf(KindInvalid, "kind must be one of %s", strings.Join(model.EventKinds, ", "))
	}
	text := strings.TrimSpace(req.Text)
	link := strings.TrimSpace(req.Link)
	switch {
	case text == "":
		return model.ThreadEntry{}, false, errf(KindInvalid, "the entry is empty")
	case len(text) > maxEntryText:
		return model.ThreadEntry{}, false, errf(KindInvalid, "the entry is longer than %d KB; put the details in a document and link it", maxEntryText>>10)
	case utf8.RuneCountInString(link) > maxEntryLink:
		return model.ThreadEntry{}, false, errf(KindInvalid, "the link is too long")
	}
	// Nothing is checked or rewritten in what is written here (17 Nov, 22:46: no filters and no
	// silent rewrites on tmx's own records; the check on credentials is where text leaves the
	// machine).

	f.mu.Lock()
	defer f.mu.Unlock()
	t, r, err := f.logThreadLocked(c, req.Thread)
	if err != nil {
		return model.ThreadEntry{}, false, err
	}
	if t.Archived {
		return model.ThreadEntry{}, false, errf(KindInvalid, "the topic %q is archived", f.threadTitleLocked(t.ID))
	}
	if req.Corrects != "" {
		old, err := f.st.GetEntry(req.Corrects)
		if err != nil || old.ThreadID != t.ID {
			return model.ThreadEntry{}, false, errf(KindInvalid, "no entry %q in this log to correct", req.Corrects)
		}
	}
	now := model.Now()
	e := model.ThreadEntry{ThreadID: t.ID, Kind: kind, Text: text, Link: link, Card: req.Card, By: model.Owner, At: now, Corrects: req.Corrects}
	if r != nil {
		e.By, e.ByID = r.a.Name, r.a.ID
	}
	e.ID = entryID(t.ID, kind, text, now)
	added, err := f.st.AddEntry(e)
	if err != nil {
		return model.ThreadEntry{}, false, err
	}
	if !added {
		old, err := f.st.GetEntry(e.ID)
		return old, false, err
	}
	f.bus.Publish(model.BusEvent{Type: model.EvThreadEntry, AgentID: e.ByID, Data: e})
	return e, true, nil
}

// Log returns a thread's log, oldest first (owner and head: any thread; an agent: its project's).
func (f *Fleet) Log(c Caller, thread string) (model.Thread, []model.ThreadEntry, error) {
	f.mu.Lock()
	t, _, err := f.logThreadLocked(c, thread)
	if err == nil {
		t.Title = f.threadTitleLocked(t.ID)
	}
	f.mu.Unlock()
	if err != nil {
		return model.Thread{}, nil, err
	}
	entries, err := f.st.Entries(t.ID)
	return t, entries, err
}

// SetThreadOrder is the owner's urgency order (most urgent first); threads not named keep
// their place, and new ones join at the end.
func (f *Fleet) SetThreadOrder(ids []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return errf(KindInvalid, "topic %q is listed twice", id)
		}
		seen[id] = true
		if _, err := f.st.GetThread(id); err != nil {
			return errf(KindNotFound, "no topic %q", id)
		}
	}
	if err := f.st.SetThreadOrder(ids, model.Now()); err != nil {
		return err
	}
	f.bus.Publish(model.BusEvent{Type: model.EvBoardChanged, Data: map[string]string{"order": "changed"}})
	return nil
}

// orderThreads sorts threads into board order. With an owner's order: placed threads by
// position, then the unplaced ones oldest first (new ones join at the end). Without: General
// first, then the most recently active.
func orderThreads(threads []model.Thread, lastAt map[string]int64) bool {
	ordered := false
	for _, t := range threads {
		ordered = ordered || t.Position > 0
	}
	sort.SliceStable(threads, func(i, j int) bool {
		a, b := threads[i], threads[j]
		if ordered {
			if (a.Position > 0) != (b.Position > 0) {
				return a.Position > 0
			}
			if a.Position > 0 {
				return a.Position < b.Position
			}
			return a.CreatedAt < b.CreatedAt
		}
		if (a.Kind == model.ThreadGeneral) != (b.Kind == model.ThreadGeneral) {
			return a.Kind == model.ThreadGeneral
		}
		return max(a.CreatedAt, lastAt[a.ID]) > max(b.CreatedAt, lastAt[b.ID])
	})
	return ordered
}

// liveEntries drops the entries a later one corrects.
func liveEntries(all []model.ThreadEntry) []model.ThreadEntry {
	gone := map[string]bool{}
	for _, e := range all {
		if e.Corrects != "" {
			gone[e.Corrects] = true
		}
	}
	var out []model.ThreadEntry
	for _, e := range all {
		if !gone[e.ID] {
			out = append(out, e)
		}
	}
	return out
}
