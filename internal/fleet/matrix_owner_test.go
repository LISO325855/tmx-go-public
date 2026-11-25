package fleet

import (
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/store"
)

// The owner acting on a Board row: an event of his on the thread, owner mail to the thread's
// agent, and the status the act implies.

func lastEvent(t *testing.T, f *Fleet, thread string) model.ThreadEntry {
	t.Helper()
	v, err := f.View(OwnerCaller, thread, false, true)
	if err != nil || len(v.Events) == 0 {
		t.Fatalf("events: %v %v", v.Events, err)
	}
	return v.Events[len(v.Events)-1]
}

func ownerMailTo(t *testing.T, f *Fleet, agent string) []string {
	t.Helper()
	msgs, err := f.st.ListMessages(store.MessageFilter{From: model.Owner, To: agent})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range msgs {
		out = append(out, m.Body)
	}
	return out
}

func TestOwnerActsOnABoardRow(t *testing.T) {
	_, f, th, lead := boardThread(t)

	// a note: his words, on the thread and in the lead's mail
	res, err := f.OwnerAct(th.ID, OwnerActRequest{Act: "note", Note: "  Take the 16 GB model.  "})
	if err != nil || res.MailedTo == "" || res.MailError != "" {
		t.Fatalf("note: %+v %v", res, err)
	}
	if e := lastEvent(t, f, th.ID); e.Text != "Take the 16 GB model." || e.By != model.Owner {
		t.Fatalf("the note's event: %+v", e)
	}
	mail := ownerMailTo(t, f, lead.AgentID)
	if len(mail) != 1 || !strings.Contains(mail[0], "Take the 16 GB model.") || !strings.Contains(mail[0], "Buy computers") || !strings.Contains(mail[0], th.ID) {
		t.Fatalf("the lead's mail: %q", mail)
	}

	// thanks and done: an event and mail, the status is the agent's to change
	for act, want := range map[string]string{"thanks": "Thanks.", "done": "Done: I did the next action."} {
		res, err := f.OwnerAct(th.ID, OwnerActRequest{Act: act})
		if err != nil || res.Thread.Status != model.ThreadActive {
			t.Fatalf("%s: %+v %v", act, res, err)
		}
		if e := lastEvent(t, f, th.ID); e.Text != want {
			t.Fatalf("%s: event %q", act, e.Text)
		}
	}
	if res, err = f.OwnerAct(th.ID, OwnerActRequest{Act: "done", Note: "Card limit raised."}); err != nil {
		t.Fatal(err)
	}
	if e := lastEvent(t, f, th.ID); e.Text != "Done: I did the next action. Card limit raised." {
		t.Fatalf("done with a note: %q", e.Text)
	}

	// park: paused, with one event (the pause), and mail
	before := len(ownerMailTo(t, f, lead.AgentID))
	res, err = f.OwnerAct(th.ID, OwnerActRequest{Act: "park"})
	if err != nil || res.Thread.Status != model.ThreadPaused {
		t.Fatalf("park: %+v %v", res, err)
	}
	if e := lastEvent(t, f, th.ID); e.Kind != model.EventPause || !strings.Contains(e.Text, "Park it") {
		t.Fatalf("park's event: %+v", e)
	}
	if n := len(ownerMailTo(t, f, lead.AgentID)); n != before+1 {
		t.Fatalf("park sent %d mails, want 1", n-before)
	}

	// not relevant: archived (off the Board), its event recorded first, mail sent
	res, err = f.OwnerAct(th.ID, OwnerActRequest{Act: "not_relevant", Note: "We lease instead."})
	if err != nil || !res.Thread.Archived {
		t.Fatalf("not relevant: %+v %v", res, err)
	}
	if e := lastEvent(t, f, th.ID); e.Text != "Not relevant any more: drop it. We lease instead." {
		t.Fatalf("not relevant's event: %q", e.Text)
	}
	cards, _ := f.Mine(OwnerCaller)
	for _, c := range cards {
		if c.Thread.ID == th.ID {
			t.Fatal("a thread that is not relevant is still in the owner's list")
		}
	}
	// an archived thread takes no more acts
	if _, err := f.OwnerAct(th.ID, OwnerActRequest{Act: "thanks"}); KindOf(err) != KindInvalid {
		t.Fatalf("an act on an archived thread: %v", err)
	}
}

func TestOwnerActIsChecked(t *testing.T) {
	_, f, th, lead := boardThread(t)
	for name, req := range map[string]OwnerActRequest{
		"an unknown act": {Act: "explode"},
		"no act":         {},
		"an empty note":  {Act: "note", Note: "   "},
		// (a note with a key in it is no longer refused: nothing written into a thread is filtered, 17 Nov)
	} {
		if _, err := f.OwnerAct(th.ID, req); KindOf(err) != KindInvalid {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := f.OwnerAct("no-such-thread", OwnerActRequest{Act: "thanks"}); KindOf(err) != KindNotFound {
		t.Fatalf("an unknown thread: %v", err)
	}
	if n := len(ownerMailTo(t, f, lead.AgentID)); n != 0 {
		t.Fatalf("refused acts sent %d mails", n)
	}
}

// The owner's order is the priority: leads and the head are told to work top-first.
func TestLeadsAndTheHeadAreToldToWorkTopFirst(t *testing.T) {
	_, f, _, lead := boardThread(t)
	w, err := f.LaunchAgent(lead, LaunchRequest{Name: "w"})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if s := f.systemPromptLocked(f.agents[lead.AgentID].a); !strings.Contains(s, boardOrderRule) || !strings.Contains(s, "work top-first") {
		t.Fatalf("the lead is not told:\n%s", s)
	}
	if s := f.systemPromptLocked(f.agents[w.ID].a); strings.Contains(s, boardOrderRule) {
		t.Fatal("a worker is told to check the Board's order")
	}
	for _, r := range f.agents {
		if r.a.Level == model.LevelHead && !strings.Contains(f.systemPromptLocked(r.a), boardOrderRule) {
			t.Fatal("the head is not told")
		}
	}
}

// The Board's Undo: what "Park it" and "Not relevant" did can be taken back, on an intent
// thread and on a project's own thread.
func TestOwnerActsCanBeUndone(t *testing.T) {
	_, f, th, lead := boardThread(t)
	f.mu.Lock()
	projectThread := f.projectThreadLocked(f.agents[lead.AgentID].a.ProjectID)
	f.mu.Unlock()
	no := false
	for _, id := range []string{th.ID, projectThread} {
		if _, err := f.SetState(OwnerCaller, id, StateRequest{Status: "blocked", Blocker: strp("Waits for the card.")}); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		// park, then back to what it was
		if res, err := f.OwnerAct(id, OwnerActRequest{Act: "park"}); err != nil || res.Thread.Status != model.ThreadPaused {
			t.Fatalf("%s park: %+v %v", id, res, err)
		}
		if got, err := f.SetState(OwnerCaller, id, StateRequest{Status: "blocked", Why: "Undo: not parked after all."}); err != nil || got.Status != model.ThreadBlocked {
			t.Fatalf("%s undo park: %+v %v", id, got, err)
		}
		// not relevant, then back on the Board, and it takes acts again
		if res, err := f.OwnerAct(id, OwnerActRequest{Act: "not_relevant"}); err != nil || !res.Thread.Archived {
			t.Fatalf("%s not relevant: %+v %v", id, res, err)
		}
		if got, err := f.UpdateThread(OwnerCaller, id, ThreadPatch{Archived: &no}); err != nil || got.Archived {
			t.Fatalf("%s undo not relevant: %+v %v", id, got, err)
		}
		if res, err := f.OwnerAct(id, OwnerActRequest{Act: "note", Note: "Undo: carry on."}); err != nil || res.Thread.Archived || res.Thread.Status != model.ThreadBlocked {
			t.Fatalf("%s after undo: %+v %v", id, res, err)
		}
		cards, _ := f.Mine(OwnerCaller)
		found := false
		for _, c := range cards {
			found = found || c.Thread.ID == id
		}
		if !found {
			t.Fatalf("%s is not back in the owner's list", id)
		}
	}
}
