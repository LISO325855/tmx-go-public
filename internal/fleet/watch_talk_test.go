package fleet

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

func watchRow(f *Fleet, key string) *model.WatchRow {
	for _, r := range f.Watch().Rows {
		if r.Key == key {
			return &r
		}
	}
	return nil
}

func TestWatchTalkCountersAlertOnly(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	watchNoDaily(f)
	a, b := launch(t, f, "a1", ""), launch(t, f, "b1", "")
	mail := func(i int, from, to string) {
		f.watchEvent(model.BusEvent{Type: model.EvMessageCreated, Data: model.Message{ID: fmt.Sprintf("m%s%s%d", from, to, i), From: from, To: to}})
	}
	// One sender to one recipient: 59 in 10 min is busy, not a row.
	for i := 0; i < watchMailPer10-1; i++ {
		mail(i, a.ID, b.ID)
	}
	mail(0, a.ID, b.ID) // the same message published again is not another message
	mail(0, b.ID, a.ID) // the other direction is another pair
	// The owner's words, tmx's own mail and outside mail are not agent talk.
	for i := 0; i < 200; i++ {
		mail(i, model.Owner, a.ID)
		mail(i, a.ID, model.Owner)
		mail(i, model.WatchParty, a.ID)
		mail(i, model.ReminderParty, a.ID)
		mail(i, ExternalPrefix+"c1", a.ID)
	}
	f.watchScan()
	if rows := f.Watch().Rows; len(rows) != 0 {
		t.Fatalf("under the thresholds: %+v", rows)
	}
	mail(watchMailPer10, a.ID, b.ID)
	f.watchScan()
	key := "talk:mail:" + a.ID + ":" + b.ID
	if r := watchRow(f, key); r == nil || r.Urgent || r.Text != "a1 sent b1 60 messages in the last 10 min; nothing was blocked" || len(f.Watch().Rows) != 1 {
		t.Fatalf("mail row: %+v", f.Watch().Rows)
	}
	// Companion and the board.
	for i := 0; i < watchCompanionPer10; i++ {
		from, to := model.CompanionParty, a.ID
		if i%2 == 0 {
			from, to = a.ID, model.CompanionParty
		}
		mail(i, from, to)
	}
	for i := 0; i < watchBoardPer10; i++ {
		f.watchEvent(model.BusEvent{Type: []string{model.EvThreadUpdated, model.EvThreadRemoved, model.EvBoardChanged}[i%3]})
	}
	f.watchEvent(model.BusEvent{Type: model.EvThreadEntry}) // a log line is not a board change
	f.watchScan()
	if r := watchRow(f, "talk:companion"); r == nil || !strings.HasPrefix(r.Text, "30 messages went to and from Companion") {
		t.Fatalf("companion row: %+v", f.Watch().Rows)
	}
	// (the watch loop also counts this fleet's own real board events: 60 or a few more)
	if r := watchRow(f, "talk:board"); r == nil || !strings.HasPrefix(r.Text, "the board changed 6") {
		t.Fatalf("board row: %+v", f.Watch().Rows)
	}
	// A full mailbox.
	f.mu.Lock()
	f.agents[b.ID].a.Queued = watchInboxWaiting
	f.mu.Unlock()
	f.watchScan()
	if r := watchRow(f, "inbox:"+b.ID); r == nil || r.Text != "b1 has 40 messages waiting in its mailbox; nothing was blocked" {
		t.Fatalf("inbox row: %+v", f.Watch().Rows)
	}
	// It is one digest line each, not urgent; and when the talk stops the rows go.
	watchClock(f, 3*time.Minute)
	if m := f.watch.board.due(nil); m == nil || m.Urgent || strings.Count(m.Body, "\n") != 4 {
		t.Fatalf("digest: %+v", m)
	}
	f.mu.Lock()
	f.agents[b.ID].a.Queued = 0
	f.mu.Unlock()
	watchClock(f, 8*time.Minute)
	f.watchScan()
	if rows := f.Watch().Rows; len(rows) != 0 {
		t.Fatalf("rows after the window passed: %+v", rows)
	}
}

func TestWatchChatRunawayNeverPausesAChat(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	watchNoDaily(f)
	f.SetChatNamer(func(id string) string {
		if id == "c1" {
			return "Build crew"
		}
		return ""
	})
	n := 0
	agent := func(chat string) {
		n++
		f.watchEvent(model.BusEvent{Type: model.EvChatMessage, Data: model.ChatMessage{ID: fmt.Sprintf("x%d", n), ChatID: chat, Sender: "me", SenderAgent: "head"}})
	}
	person := func(chat, id string) {
		f.watchEvent(model.BusEvent{Type: model.EvChatMessage, Data: &model.ChatMessage{ID: id, ChatID: chat, Sender: "peer1", SenderName: "Anna"}})
	}
	for i := 0; i < watchChatPer10-1; i++ {
		agent("c1")
	}
	// The same message published again (a delivery receipt) is not another message.
	f.watchEvent(model.BusEvent{Type: model.EvChatMessage, Data: model.ChatMessage{ID: "x1", ChatID: "c1", Sender: "me", SenderAgent: "head"}})
	f.watchEvent(model.BusEvent{Type: model.EvChatMessage, Data: model.ChatMessage{ID: "s1", ChatID: "c1", Sender: "system", SenderAgent: "head"}})
	f.watchScan()
	if rows := f.Watch().Rows; len(rows) != 0 {
		t.Fatalf("49 agent messages: %+v", rows)
	}
	agent("c1")
	f.watchScan()
	r := watchRow(f, "chatrun:c1")
	if r == nil || r.Urgent || r.Status != "busy" ||
		r.Text != `agents wrote 50 messages in the chat "Build crew" in the last 10 min with no message from a person; the chat runs on, nothing was paused` {
		t.Fatalf("chat row: %+v", f.Watch().Rows)
	}
	// A person writes: the run is over. The same message arriving again later does not
	// reset the next run.
	person("c1", "p1")
	f.watchScan()
	if rows := f.Watch().Rows; len(rows) != 0 {
		t.Fatalf("a person wrote, the row stays: %+v", rows)
	}
	for i := 0; i < watchChatPer10; i++ {
		agent("c1")
	}
	person("c1", "p1")
	f.watchScan()
	if watchRow(f, "chatrun:c1") == nil {
		t.Fatal("a republished person message reset the count")
	}
	// It keeps going: urgent, once. Another chat (no name known) at 40 per 10 min for
	// 50 minutes is under the short threshold but over the long one.
	for slice := 0; slice < 5; slice++ {
		for i := 0; i < 40; i++ {
			agent("c2")
		}
		watchClock(f, 9*time.Minute)
	}
	for i := 0; i < 80; i++ {
		agent("c2")
	}
	f.watchScan()
	r = watchRow(f, "chatrun:c2")
	if r == nil || !r.Urgent || r.Status != "runaway" || !strings.HasPrefix(r.Text, `agents keep talking in the chat "c2": `) || !strings.HasSuffix(r.Text, "nothing was paused") {
		t.Fatalf("runaway row: %+v", f.Watch().Rows)
	}
	m := f.watch.board.due(nil)
	if m == nil || !m.Urgent || !strings.Contains(m.Body, "- URGENT: agents keep talking") {
		t.Fatalf("urgent message: %+v", m)
	}
	f.watch.board.sent(m)
	for i := 0; i < 300; i++ {
		agent("c2")
	}
	f.watchScan()
	if m := f.watch.board.due(nil); m != nil {
		t.Fatalf("a runaway chat was announced twice: %q", m.Body)
	}
	// Quiet again: the row goes by itself.
	watchClock(f, 31*time.Minute)
	f.watchScan()
	if rows := f.Watch().Rows; len(rows) != 0 {
		t.Fatalf("rows after the chat went quiet: %+v", rows)
	}
}

// The loop reads the bus: real mail is counted without any hook in the mail path.
func TestWatchTalkReadsTheBus(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	f.CreateProject(OwnerCaller, ProjectRequest{Name: "alpha"})
	lead := mustAgent(t, f, "alpha-lead")
	e.chat.Last(lead.ID).State(model.StateWorking)
	lc := callerFor(t, f, e.chat, lead.ID)
	w, err := f.LaunchAgent(lc, LaunchRequest{Name: "w1"})
	if err != nil {
		t.Fatal(err)
	}
	wc := callerFor(t, f, e.chat, w.ID)
	if _, err := f.SendMessage(wc, SendRequest{To: "alpha-lead", Body: "done"}); err != nil {
		t.Fatal(err)
	}
	key := "talk:mail:" + w.ID + watchMailSeparator + lead.ID
	eventually(t, "the mail is counted", func() bool {
		return f.watch.board.rates("talk:mail:", watchTalkWindow, watchTalkWindow)[key][0] == 1
	})
}
