package fleet

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// The Board view of the Matrix (the owner, 17 Nov 2026: one row per thread with how far it is and
// what it waits for). A thread carries progress (0-100, the agents' estimate), the one line that
// came with it, and a blocker; SetState (the agents' matrix op=state) sets them.

func boardThread(t *testing.T) (*env, *Fleet, model.Thread, Caller) {
	t.Helper()
	e := newEnv(t)
	f := e.started()
	p, err := f.CreateProject(OwnerCaller, ProjectRequest{Name: "Shop"})
	if err != nil {
		t.Fatal(err)
	}
	th, err := f.NewThread(OwnerCaller, NewThreadRequest{Title: "Buy computers", Parent: "Shop", Owner: p.LeadID})
	if err != nil {
		t.Fatal(err)
	}
	return e, f, th, Caller{AgentID: p.LeadID}
}

func intp(n int) *int       { return &n }
func strp(s string) *string { return &s }

func TestANewThreadHasReportedNothing(t *testing.T) {
	_, f, th, _ := boardThread(t)
	if th.Progress != nil || th.ProgressWhy != "" || th.Blocker != "" {
		t.Fatalf("a new thread: %+v", th)
	}
	b, _ := json.Marshal(th)
	for _, k := range []string{`"progress"`, `"progress_why"`, `"blocker"`} {
		if strings.Contains(string(b), k) {
			t.Fatalf("a thread that reported nothing carries %s: %s", k, b)
		}
	}
	cards, err := f.Mine(OwnerCaller)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cards {
		if c.Thread.Progress != nil {
			t.Fatalf("thread %q reports progress it never set: %d", c.Thread.Title, *c.Thread.Progress)
		}
	}
}

func TestProgressAndBlockerAreSetWithTheStateAndSurviveARestart(t *testing.T) {
	e, f, th, lead := boardThread(t)

	// progress alone, with where it stands; 0 is a report too
	got, err := f.SetState(lead, th.ID, StateRequest{Pct: intp(0), Why: "Order form found, nothing bought yet."})
	if err != nil || got.Progress == nil || *got.Progress != 0 || got.ProgressWhy != "Order form found, nothing bought yet." || got.Status != model.ThreadActive {
		t.Fatalf("pct alone: %+v %v", got, err)
	}
	got, err = f.SetState(lead, th.ID, StateRequest{Pct: intp(60), Why: "Two laptops in the basket."})
	if err != nil || *got.Progress != 60 || got.ProgressWhy != "Two laptops in the basket." {
		t.Fatalf("pct: %+v %v", got, err)
	}
	// a new number without a new line keeps the last line
	if got, err = f.SetState(lead, th.ID, StateRequest{Pct: intp(65)}); err != nil || *got.Progress != 65 || got.ProgressWhy != "Two laptops in the basket." {
		t.Fatalf("pct without why: %+v %v", got, err)
	}
	// blocked, with what stops it; a blocker of several lines is one line
	got, err = f.SetState(lead, th.ID, StateRequest{Status: "blocked", Blocker: strp("Waits for the card limit\n to be raised.")})
	if err != nil || got.Status != model.ThreadBlocked || got.Blocker != "Waits for the card limit to be raised." || *got.Progress != 65 {
		t.Fatalf("blocked: %+v %v", got, err)
	}
	// waiting on the owner: the blocker says exactly what he has to do
	got, err = f.SetState(lead, th.ID, StateRequest{Status: "waiting_on_owner", Blocker: strp("Raise the card limit to CHF 4,000.")})
	if err != nil || got.Status != model.ThreadWaiting || got.Blocker != "Raise the card limit to CHF 4,000." {
		t.Fatalf("waiting: %+v %v", got, err)
	}

	// the owner's list carries it, and a daemon restart keeps it
	for _, fl := range []*Fleet{f, e.started()} {
		cards, err := fl.Mine(OwnerCaller)
		if err != nil {
			t.Fatal(err)
		}
		var found bool
		for _, c := range cards {
			if c.Thread.ID != th.ID {
				continue
			}
			found = true
			if c.Thread.Progress == nil || *c.Thread.Progress != 65 || c.Thread.ProgressWhy != "Two laptops in the basket." || c.Thread.Blocker != "Raise the card limit to CHF 4,000." {
				t.Fatalf("the list's card: %+v", c.Thread)
			}
		}
		if !found {
			t.Fatal("the thread is not in the owner's list")
		}
	}

	// active again: it waits for nothing any more, unless it says so
	if got, err = f.SetState(lead, th.ID, StateRequest{Status: "active"}); err != nil || got.Blocker != "" || *got.Progress != 65 {
		t.Fatalf("active again: %+v %v", got, err)
	}
	if got, err = f.SetState(lead, th.ID, StateRequest{Blocker: strp("Delivery on Friday.")}); err != nil || got.Blocker != "Delivery on Friday." || got.Status != model.ThreadActive {
		t.Fatalf("blocker alone: %+v %v", got, err)
	}
	if got, err = f.SetState(lead, th.ID, StateRequest{Blocker: strp("  ")}); err != nil || got.Blocker != "" {
		t.Fatalf("clearing the blocker: %+v %v", got, err)
	}
	// done is 100, whatever was reported, and waits for nothing
	if _, err = f.SetState(lead, th.ID, StateRequest{Status: "blocked", Blocker: strp("x")}); err != nil {
		t.Fatal(err)
	}
	if got, err = f.SetState(lead, th.ID, StateRequest{Status: "done"}); err != nil || got.Progress == nil || *got.Progress != 100 || got.Blocker != "" {
		t.Fatalf("done: %+v %v", got, err)
	}
}

func TestProgressAndBlockerAreChecked(t *testing.T) {
	_, f, th, lead := boardThread(t)
	for name, req := range map[string]StateRequest{
		"below 0":        {Pct: intp(-1)},
		"above 100":      {Pct: intp(101)},
		"nothing at all": {},
		"only a why":     {Why: "just saying"},
	} {
		if _, err := f.SetState(lead, th.ID, req); KindOf(err) != KindInvalid {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A blocker is never refused for its length or its words (17 Nov): a long one is kept whole,
	// one with a value shaped like a key is filed as written (no filter, no rewrite).
	long := strings.Repeat("waiting for the certificate password and the 2FA device; ", 20)
	got, err := f.SetState(lead, th.ID, StateRequest{Blocker: strp(long)})
	if err != nil || got.Blocker != strings.TrimSpace(long) {
		t.Fatalf("a long blocker: %d characters kept, %v", len(got.Blocker), err)
	}
	got, err = f.SetState(lead, th.ID, StateRequest{Blocker: strp("use the key sk-ant-api03-" + strings.Repeat("A", 40) + " from the vault")})
	if err != nil || !strings.Contains(got.Blocker, "sk-ant-api03-AAAA") || strings.Contains(got.Blocker, "removed by tmx") || !strings.Contains(got.Blocker, "from the vault") {
		t.Fatalf("a key in a blocker: %q %v", got.Blocker, err)
	}
	// the same people as for the status: a worker that does not own the thread may not
	w, err := f.LaunchAgent(lead, LaunchRequest{Name: "w"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.SetState(Caller{AgentID: w.ID}, th.ID, StateRequest{Pct: intp(99)}); KindOf(err) != KindForbidden {
		t.Fatalf("a worker set the progress of a thread it does not own: %v", err)
	}
	if got, err := f.View(OwnerCaller, th.ID, false, false); err != nil || got.Thread.Progress != nil {
		t.Fatalf("a refused report left progress behind: %+v %v", got.Thread, err)
	}
}

// The agents that may report are told to: a project's lead and a thread's owner, nobody else.
func TestLeadsAndThreadOwnersAreToldToReportToTheBoard(t *testing.T) {
	_, f, th, lead := boardThread(t)
	w, err := f.LaunchAgent(lead, LaunchRequest{Name: "w"})
	if err != nil {
		t.Fatal(err)
	}
	prompt := func(id string) string {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.systemPromptLocked(f.agents[id].a)
	}
	for _, want := range []string{"op=state", "pct (0-100", "blocker", "waiting_on_owner"} {
		if !strings.Contains(prompt(lead.AgentID), want) {
			t.Fatalf("the lead is not told %q:\n%s", want, prompt(lead.AgentID))
		}
	}
	if strings.Contains(prompt(w.ID), boardReportRule) {
		t.Fatal("a worker that owns no thread is told to report to the Board")
	}
	// a worker that owns a thread is told; once the thread is done, no more
	mine, err := f.NewThread(OwnerCaller, NewThreadRequest{Title: "Compare prices", Parent: "Shop", Owner: w.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt(w.ID), boardReportRule) {
		t.Fatal("a thread's owner is not told to report to the Board")
	}
	if _, err := f.SetState(OwnerCaller, mine.ID, StateRequest{Status: "done"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt(w.ID), boardReportRule) {
		t.Fatal("the owner of a finished thread is still told to report")
	}
	_ = th
}

func TestAProgressReportIsAnEventOnlyWhenItSaysSomethingNew(t *testing.T) {
	_, f, th, lead := boardThread(t)
	events := func() int {
		v, err := f.View(OwnerCaller, th.ID, false, true)
		if err != nil {
			t.Fatal(err)
		}
		return len(v.Events)
	}
	before := events()
	if _, err := f.SetState(lead, th.ID, StateRequest{Pct: intp(40), Why: "Half the pages moved."}); err != nil {
		t.Fatal(err)
	}
	if events() != before+1 {
		t.Fatalf("a progress report made %d events, want 1", events()-before)
	}
	v, _ := f.View(OwnerCaller, th.ID, false, true)
	if last := v.Events[len(v.Events)-1].Text; !strings.Contains(last, "40%") || !strings.Contains(last, "Half the pages moved.") {
		t.Fatalf("the event does not say it: %q", last)
	}
	// the same again says nothing new
	if _, err := f.SetState(lead, th.ID, StateRequest{Pct: intp(40), Why: "Half the pages moved."}); err != nil {
		t.Fatal(err)
	}
	if events() != before+1 {
		t.Fatalf("repeating a report made another event")
	}
}
