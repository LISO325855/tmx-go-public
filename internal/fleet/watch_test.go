package fleet

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/store"
)

// testBoard is a board with a clock the test moves. It starts at 10:00 local time.
func testBoard(t *testing.T, path string) (*watchBoard, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.Local)
	b := newWatchBoard((*config.WatchConfig)(nil).Effective(), path, "testbox")
	b.now = func() time.Time { return now }
	b.started = now
	b.d.LastDaily = now.Format(time.DateOnly)
	return b, &now
}

func row(key, status, text string) model.WatchItem {
	return model.WatchItem{Key: key, Status: status, Text: text}
}

func mustDue(t *testing.T, b *watchBoard) *watchMessage {
	t.Helper()
	m := b.due(nil)
	if m == nil {
		t.Fatal("no message due")
	}
	return m
}

func TestWatchDigestIsOneMessageAndRateLimited(t *testing.T) {
	b, now := testBoard(t, "")
	b.report(row("mail:a", "stuck", "a has mail waiting"), row("peer:hosta", "offline", "machine hosta is unreachable"))
	if m := b.due(nil); m != nil {
		t.Fatalf("a reading younger than the hold was mailed: %q", m.Body)
	}
	*now = now.Add(3 * time.Minute)
	m := mustDue(t, b)
	if m.Urgent || m.Daily || !strings.Contains(m.Body, "a has mail waiting") || !strings.Contains(m.Body, "machine hosta is unreachable") ||
		!strings.HasPrefix(m.Body, "tmx watch on testbox, 10:03:") || strings.Count(m.Body, "\n") != 2 {
		t.Fatalf("digest: %q", m.Body)
	}
	// Not sent (no head, or one still waits): nothing is settled, it is due again.
	if again := b.due(nil); again == nil || again.Body != m.Body {
		t.Fatalf("an unsent digest was dropped: %+v", again)
	}
	b.sent(m)
	// The same readings again, for hours: never a second message.
	for i := 0; i < 200; i++ {
		*now = now.Add(time.Minute)
		b.report(row("mail:a", "stuck", "a has mail waiting"), row("peer:hosta", "offline", "machine hosta is unreachable"))
		if m := b.due(nil); m != nil {
			t.Fatalf("minute %d: told again: %q", i, m.Body)
		}
	}
	// Something new right after a digest waits for the next one.
	b.sent(&watchMessage{}) // "a digest just went out"
	b.report(row("mail:b", "stuck", "b has mail waiting"))
	*now = now.Add(10 * time.Minute)
	if m := b.due(nil); m != nil {
		t.Fatalf("a second digest within the interval: %q", m.Body)
	}
	*now = now.Add(21 * time.Minute)
	m = mustDue(t, b)
	if !strings.Contains(m.Body, "b has mail waiting") || strings.Contains(m.Body, "a has mail waiting") {
		t.Fatalf("second digest carries only what is new: %q", m.Body)
	}
}

func TestWatchFlapIsNeverMailed(t *testing.T) {
	b, now := testBoard(t, "")
	for i := 0; i < 50; i++ {
		b.report(row("memory", "warn", "memory level warn"))
		*now = now.Add(time.Minute)
		b.report(row("memory", model.WatchOK, ""))
		*now = now.Add(time.Minute)
		if m := b.due(nil); m != nil {
			t.Fatalf("flap %d was mailed: %q", i, m.Body)
		}
	}
	if v := b.view(); len(v.Rows) != 0 || len(v.Notes) != 0 {
		t.Fatalf("a flap left something behind: %+v", v)
	}
}

func TestWatchUrgentAtOnceOncePerEpisode(t *testing.T) {
	b, now := testBoard(t, "")
	b.report(row("mail:a", "stuck", "a has mail waiting")) // younger than the hold
	low := model.WatchItem{Key: "disk:C:", Status: "low", Text: "disk is below its free-space reserve: C: has 3.0 GB free", Urgent: true}
	b.report(low)
	m := mustDue(t, b)
	if !m.Urgent || !strings.HasPrefix(m.Body, "URGENT, tmx watch on testbox") || !strings.Contains(m.Body, "- URGENT: disk is below") ||
		!strings.Contains(m.Body, "a has mail waiting") {
		t.Fatalf("urgent message (with everything open): %q", m.Body)
	}
	b.sent(m)
	for i := 0; i < 10; i++ { // still low: silence
		*now = now.Add(time.Minute)
		b.report(low)
		if m := b.due(nil); m != nil {
			t.Fatalf("urgent told twice in one episode: %q", m.Body)
		}
	}
	// It recovers and falls again five minutes later: not a second urgent message at once
	// (one per key per interval); it is told once the interval is over, with the recovery line.
	b.report(row("disk:C:", model.WatchOK, ""))
	b.sent(&watchMessage{}) // "a digest just went out"
	*now = now.Add(5 * time.Minute)
	b.report(low)
	if m := b.due(nil); m != nil {
		t.Fatalf("a flapping urgent row became a stream: %q", m.Body)
	}
	*now = now.Add(31 * time.Minute)
	m = mustDue(t, b)
	if !strings.Contains(m.Body, "- URGENT: disk is below") || !strings.Contains(m.Body, "ok again: disk is below") {
		t.Fatalf("digest after a flap: %q", m.Body)
	}
	b.sent(m)
	// A new episode after the interval is urgent again.
	b.report(row("disk:C:", model.WatchOK, ""))
	*now = now.Add(40 * time.Minute)
	b.report(low)
	if m := mustDue(t, b); !m.Urgent {
		t.Fatalf("a new episode is not urgent: %q", m.Body)
	}
}

func TestWatchRecoveryRidesAlongButNeverCausesAMessage(t *testing.T) {
	b, now := testBoard(t, "")
	b.report(row("peer:hosta", "offline", "machine hosta is unreachable"))
	*now = now.Add(3 * time.Minute)
	b.sent(mustDue(t, b))
	b.report(row("peer:hosta", model.WatchOK, ""))
	*now = now.Add(2 * time.Hour)
	if m := b.due(nil); m != nil {
		t.Fatalf("a recovery alone caused a message: %q", m.Body)
	}
	b.report(model.WatchItem{Note: true, Text: "w1 died (exited with code 1); automatic restart 1 in 30s"})
	m := mustDue(t, b)
	if !strings.Contains(m.Body, "w1 died") || !strings.Contains(m.Body, "ok again: machine hosta is unreachable") {
		t.Fatalf("digest: %q", m.Body)
	}
	b.sent(m)
	if v := b.view(); len(v.Notes) != 0 {
		t.Fatalf("notes kept after they were told: %v", v.Notes)
	}
}

func TestWatchDailyDigest(t *testing.T) {
	b, now := testBoard(t, "")
	b.count(watchEvRestart)
	b.count(watchEvRestart)
	b.count(watchEvPause)
	summary := func(ev map[string]int) []string {
		return []string{"- agents: 3 running", "- restarts: " + strings.Repeat("x", ev[watchEvRestart])}
	}
	*now = time.Date(2026, 11, 18, 7, 59, 0, 0, time.Local)
	if m := b.due(summary); m != nil {
		t.Fatalf("daily digest before its hour: %q", m.Body)
	}
	*now = now.Add(2 * time.Minute)
	m := b.due(summary)
	if m == nil || !m.Daily || m.Urgent || !strings.HasPrefix(m.Body, "Daily digest, tmx watch on testbox, Wed 18 Nov:") ||
		!strings.Contains(m.Body, "- agents: 3 running") || !strings.Contains(m.Body, "- restarts: xx") || !strings.Contains(m.Body, "- no open problems") {
		t.Fatalf("daily digest: %+v", m)
	}
	b.sent(m)
	*now = now.Add(10 * time.Hour)
	if m := b.due(summary); m != nil {
		t.Fatalf("a second daily digest on the same day: %q", m.Body)
	}
	// The next day's lists what is still open, even though the head was told before.
	b.report(row("peer:hosta", "offline", "machine hosta is unreachable"))
	*now = now.Add(3 * time.Minute)
	b.sent(mustDue(t, b))
	*now = time.Date(2026, 11, 19, 8, 0, 0, 0, time.Local)
	b.report(row("peer:hosta", "offline", "machine hosta is unreachable"))
	m = b.due(summary)
	if m == nil || !m.Daily || !strings.Contains(m.Body, "- still open: machine hosta is unreachable") || strings.Contains(m.Body, "no open problems") {
		t.Fatalf("next daily digest: %+v", m)
	}
	// daily_hour -1 turns it off.
	off := -1
	b2 := newWatchBoard((&config.WatchConfig{DailyHour: &off}).Effective(), "", "testbox")
	b2.d.LastDaily = "2000-01-01"
	if m := b2.due(summary); m != nil {
		t.Fatalf("daily digest with daily_hour -1: %q", m.Body)
	}
}

func TestWatchSurvivesARestartWithoutRepeating(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.json")
	b, now := testBoard(t, path)
	low := model.WatchItem{Key: "disk:C:", Status: "low", Text: "disk is below its free-space reserve", Urgent: true}
	b.report(low, row("memory", "warn", "memory level warn"))
	*now = now.Add(3 * time.Minute)
	b.sent(mustDue(t, b))
	if err := b.save(); err != nil {
		t.Fatal(err)
	}
	// A new daemon: the disk guard says "low" again (the same episode), the memory guard
	// says nothing (it is fine now).
	b2 := newWatchBoard((*config.WatchConfig)(nil).Effective(), path, "testbox")
	at := *now
	b2.now = func() time.Time { return at }
	b2.started = at
	if v := b2.view(); len(v.Rows) != 2 || v.Rows[0].Notified == 0 {
		t.Fatalf("rows after a restart: %+v", v.Rows)
	}
	b2.report(low)
	if m := b2.due(nil); m != nil {
		t.Fatalf("the same episode was announced again after a restart: %q", m.Body)
	}
	at = at.Add(4 * time.Minute)
	if m := b2.due(nil); m != nil {
		t.Fatalf("dropping an unconfirmed row caused a message: %q", m.Body)
	}
	v := b2.view()
	if len(v.Rows) != 1 || v.Rows[0].Key != "disk:C:" {
		t.Fatalf("the row no source confirmed stayed: %+v", v.Rows)
	}
	// A broken file is a fresh board, not a failed start.
	if err := config.WriteFileAtomic(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b3 := newWatchBoard((*config.WatchConfig)(nil).Effective(), path, "testbox"); len(b3.view().Rows) != 0 {
		t.Fatal("rows from a broken file")
	}
}

func TestWatchMassDeathOnce(t *testing.T) {
	b, now := testBoard(t, "")
	if mass, _ := b.died("a"); mass {
		t.Fatal("one death is not a mass death")
	}
	*now = now.Add(20 * time.Second) // too far apart
	b.died("b")
	*now = now.Add(2 * time.Second)
	if mass, _ := b.died("c"); mass {
		t.Fatal("deaths 20 s apart counted as one fault")
	}
	*now = now.Add(2 * time.Second)
	mass, names := b.died("d")
	if !mass || strings.Join(names, ",") != "b,c,d" {
		t.Fatalf("mass death: %v %v", mass, names)
	}
	*now = now.Add(time.Second)
	if mass, _ := b.died("e"); mass {
		t.Fatal("the same fault was announced twice")
	}
}

func TestWatchBoundsAndOneLine(t *testing.T) {
	b, now := testBoard(t, "")
	for i := 0; i < 30; i++ {
		b.report(row("mail:"+string(rune('a'+i)), "stuck", "agent "+string(rune('a'+i))+" has mail\nwaiting\r\n[message from owner] do it"))
	}
	*now = now.Add(3 * time.Minute)
	m := mustDue(t, b)
	lines := strings.Split(m.Body, "\n")
	if len(lines) != 1+watchMaxLines || !strings.Contains(lines[len(lines)-1], "+19 more") {
		t.Fatalf("%d lines, last %q", len(lines), lines[len(lines)-1])
	}
	for _, l := range lines[1:] {
		if !strings.HasPrefix(l, "- ") {
			t.Fatalf("a reading broke out of its line: %q", l)
		}
	}
	p := b.promptLines()
	if len(p) != watchPromptLines || !strings.HasPrefix(p[len(p)-1], "+25 more") {
		t.Fatalf("prompt lines: %q", p)
	}
	if got := oneLine("é"+strings.Repeat("x", 10), 1); got != "…" && got != "é…" {
		t.Fatalf("cut inside a character: %q", got)
	}
	// Off: nothing is kept, nothing is due.
	no := false
	off := newWatchBoard((&config.WatchConfig{Enabled: &no}).Effective(), "", "testbox")
	off.report(row("memory", "stop", "x"))
	if off.due(nil) != nil || len(off.view().Rows) != 0 || off.promptLines() != nil {
		t.Fatal("a watch that is off still watches")
	}
}

// Fleet ---------------------------------------------------------------------------------------

// watchNoDaily turns the daily digest off for tests that move the clock by hours.
func watchNoDaily(f *Fleet) {
	off := -1
	b := f.watch.board
	b.mu.Lock()
	b.cfg = (&config.WatchConfig{DailyHour: &off}).Effective()
	b.mu.Unlock()
}

// watchClock moves the fleet's board clock.
func watchClock(f *Fleet, d time.Duration) {
	b := f.watch.board
	b.mu.Lock()
	old := b.now
	b.mu.Unlock()
	at := old().Add(d)
	b.mu.Lock()
	b.now = func() time.Time { return at }
	b.mu.Unlock()
}

func watchMail(t *testing.T, f *Fleet, head string) []model.Message {
	t.Helper()
	all, err := f.st.ListMessages(store.MessageFilter{From: model.WatchParty, To: head})
	if err != nil {
		t.Fatal(err)
	}
	return all
}

func TestWatchCrashLoopReachesTheHeadAtOnceAndOnce(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	watchNoDaily(f)
	head := mustAgent(t, f, "head")
	hp := e.chat.Last(head.ID)
	hp.State(model.StateIdle)
	waitState(t, f, "head", model.StateIdle)
	w := launch(t, f, "w1", "")
	f.mu.Lock()
	f.agents[w.ID].recovery.attempts = []time.Time{time.Now(), time.Now(), time.Now()}
	f.mu.Unlock()
	before := len(hp.Sent())
	e.chat.Last(w.ID).Exit(1, nil)
	eventually(t, "w1 stays stopped", func() bool { return mustAgent(t, f, "w1").Desired == "stopped" })
	if n := len(hp.Sent()); n != before {
		t.Fatalf("the watch mailed outside its tick: %d sends", n-before)
	}
	f.WatchTick()
	eventually(t, "urgent mail to the head", func() bool { return len(hp.Sent()) == before+1 })
	got := hp.Sent()[before]
	for _, want := range []string{watchLabel, "│ URGENT, tmx watch on testbox", "w1 keeps dying (exited with code 1)", "crash loop: 3 automatic restarts within 15 minutes", "stays stopped"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	hp.Turn("noted", 0, 1)
	waitState(t, f, "head", model.StateIdle)
	for i := 0; i < 5; i++ {
		watchClock(f, time.Hour)
		f.WatchTick()
	}
	if n := len(watchMail(t, f, head.ID)); n != 1 {
		t.Fatalf("%d watch messages for one crash loop", n)
	}
	// A deliberate start ends the episode: the row goes.
	if _, err := f.StartAgent(OwnerCaller, w.ID); err != nil {
		t.Fatal(err)
	}
	f.WatchTick()
	for _, r := range f.Watch().Rows {
		if strings.HasPrefix(r.Key, "agent:") {
			t.Fatalf("crash row kept after a start: %+v", r)
		}
	}
}

func TestWatchRestartIsADigestLineNotAStream(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	head := mustAgent(t, f, "head")
	hp := e.chat.Last(head.ID)
	hp.State(model.StateIdle)
	waitState(t, f, "head", model.StateIdle)
	a, b, c := launch(t, f, "a1", ""), launch(t, f, "b1", ""), launch(t, f, "c1", "")
	before := len(hp.Sent())
	for _, w := range []model.Agent{a, b, c} {
		e.chat.Last(w.ID).Exit(1, nil)
		waitState(t, f, w.Name, model.StateError)
	}
	f.WatchTick()
	eventually(t, "one message", func() bool { return len(hp.Sent()) == before+1 })
	got := hp.Sent()[before]
	if !strings.Contains(got, "FLEET-LEVEL FAULT: 3 agents died within 10 s (a1, b1, c1)") || strings.Count(got, "automatic restart 1 in 30s") != 3 {
		t.Fatalf("mass death message: %q", got)
	}
	if n := len(watchMail(t, f, head.ID)); n != 1 {
		t.Fatalf("three deaths made %d messages", n)
	}
}

func TestWatchStuckMailIsReportedNeverDelivered(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	head := mustAgent(t, f, "head")
	hp := e.chat.Last(head.ID)
	hp.State(model.StateIdle)
	waitState(t, f, "head", model.StateIdle)
	w := launch(t, f, "w1", "")
	wp := e.chat.Last(w.ID)
	wp.State(model.StateIdle)
	waitState(t, f, "w1", model.StateIdle)
	// Mail that sits in the queue of an idle agent (whatever lost its delivery).
	m := model.Message{ID: model.NewID(), From: head.ID, To: w.ID, Body: "are you there", State: model.MsgQueued,
		CreatedAt: model.Now() - (6 * time.Minute).Milliseconds()}
	if err := f.st.PutMessage(m); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.refreshQueuedLocked(f.agents[w.ID])
	f.mu.Unlock()
	sent := len(wp.Sent())
	f.WatchTick()
	rows := f.Watch().Rows
	if len(rows) != 1 || rows[0].Key != "mail:"+w.ID || rows[0].Status != "stuck" || !strings.Contains(rows[0].Text, "w1 is idle but 1 message(s)") {
		t.Fatalf("rows: %+v", rows)
	}
	watchClock(f, 3*time.Minute)
	before := len(hp.Sent())
	f.WatchTick()
	eventually(t, "digest to the head", func() bool { return len(hp.Sent()) == before+1 })
	if got := hp.Sent()[before]; !strings.Contains(got, "│ tmx watch on testbox") || !strings.Contains(got, "w1 is idle but 1 message(s)") || strings.Contains(got, "URGENT") {
		t.Fatalf("digest: %q", got)
	}
	// Report only: the watch did not deliver, type or change the message.
	if n := len(wp.Sent()); n != sent {
		t.Fatalf("the watch delivered mail: %d sends", n-sent)
	}
	if cur, err := f.st.GetMessage(m.ID); err != nil || cur.State != model.MsgQueued {
		t.Fatalf("the watch changed the message: %+v %v", cur, err)
	}
	// Working is not stuck (mail waits for the turn by design); the row clears.
	wp.State(model.StateWorking)
	waitState(t, f, "w1", model.StateWorking)
	f.WatchTick()
	if rows := f.Watch().Rows; len(rows) != 0 {
		t.Fatalf("a working agent's mail is reported: %+v", rows)
	}
	// A stopped agent's mail is reported later (15 min), and a shelved one's never.
	if _, err := f.StopAgent(OwnerCaller, w.ID); err != nil {
		t.Fatal(err)
	}
	f.WatchTick()
	if rows := f.Watch().Rows; len(rows) != 0 {
		t.Fatalf("mail for a stopped agent reported after 9 min: %+v", rows)
	}
	watchClock(f, 10*time.Minute)
	f.WatchTick()
	if rows := f.Watch().Rows; len(rows) != 1 || rows[0].Status != "stopped" || !strings.Contains(rows[0].Text, "w1 is stopped and 1 message(s)") {
		t.Fatalf("stopped agent's mail: %+v", rows)
	}
	f.mu.Lock()
	f.agents[w.ID].a.ShelvedAt = model.Now()
	f.mu.Unlock()
	f.WatchTick()
	if rows := f.Watch().Rows; len(rows) != 0 {
		t.Fatalf("a shelved agent's mail is reported: %+v", rows)
	}
}

func TestWatchOneMessageWaitsForABusyHead(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	head := mustAgent(t, f, "head")
	hp := e.chat.Last(head.ID)
	hp.State(model.StateWorking)
	waitState(t, f, "head", model.StateWorking)
	watchNoDaily(f)
	before := len(hp.Sent())
	b := f.watch.board
	b.report(row("test:hosta", "offline", "machine hosta is unreachable"))
	watchClock(f, 3*time.Minute)
	f.WatchTick()
	// Not urgent: it waits for the end of the head's turn.
	if n := len(hp.Sent()); n != before {
		t.Fatalf("a digest interrupted the head: %d sends", n-before)
	}
	for i := 0; i < 20; i++ { // more and more goes wrong while the head is busy
		b.report(row("test:x"+string(rune('a'+i)), "stuck", "more mail waiting"))
		watchClock(f, 45*time.Minute)
		f.WatchTick()
	}
	queued := 0
	for _, m := range watchMail(t, f, head.ID) {
		if m.State == model.MsgQueued {
			queued++
		}
	}
	if queued != 1 || mustAgent(t, f, "head").Queued != 1 {
		t.Fatalf("%d watch messages wait for the head (queued %d)", queued, mustAgent(t, f, "head").Queued)
	}
	// An urgent one may not jump the queue either while one waits; once the head read it,
	// the urgent one goes at once, into the running turn.
	hp.Turn("ok", 0, 1)
	eventually(t, "the waiting digest is delivered", func() bool { return len(hp.Sent()) == before+1 })
	waitState(t, f, "head", model.StateWorking)
	b.report(model.WatchItem{Key: "memory", Status: "stop", Text: "memory is critical", Urgent: true})
	f.WatchTick()
	eventually(t, "urgent goes into the running turn", func() bool { return len(hp.Sent()) == before+2 })
	if got := hp.Sent()[before+1]; !strings.Contains(got, "URGENT: memory is critical") || !strings.Contains(got, "more mail waiting") {
		t.Fatalf("urgent message: %q", got)
	}
}

func TestWatchPauseAndResume(t *testing.T) {
	e := newEnv(t)
	f := e.fleetWith(&fakeRes{max: 8}, &fakeSuspend{})
	w := launch(t, f, "w1", "")
	if _, err := f.BrakeAgent(w.ID, BrakeWriteBudget, "w1 wrote 21.0 GB today; its daily write budget is 20.0 GB.", true); err != nil {
		t.Fatal(err)
	}
	rows := f.Watch().Rows
	if len(rows) != 1 || rows[0].Key != "paused:"+w.ID || rows[0].Urgent || !strings.Contains(rows[0].Text, "w1 is paused by tmx: w1 wrote 21.0 GB") {
		t.Fatalf("rows: %+v", rows)
	}
	f.WatchTick() // the scan keeps a row whose agent is still paused
	if len(f.Watch().Rows) != 1 {
		t.Fatal("the scan dropped the pause row")
	}
	if !f.UnbrakeAgent(w.ID, BrakeWriteBudget, "resumed: a new day") {
		t.Fatal("not resumed")
	}
	if rows := f.Watch().Rows; len(rows) != 0 {
		t.Fatalf("rows after resume: %+v", rows)
	}
	// The short turn off a saturated disk is not reported.
	if _, err := f.BrakeAgent(w.ID, BrakeDisk, "The disk is saturated", false); err != nil {
		t.Fatal(err)
	}
	if rows := f.Watch().Rows; len(rows) != 0 {
		t.Fatalf("a disk turn was reported: %+v", rows)
	}
}

type fakeSample model.WatchSample

func (s fakeSample) WatchSample() model.WatchSample { return model.WatchSample(s) }

type fakeGuardEvent []model.WatchItem

func (g fakeGuardEvent) WatchItems() []model.WatchItem { return g }

func TestWatchReadsResourceEvents(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	w := launch(t, f, "w1", "")
	const gb = 1 << 30
	f.Publish(model.BusEvent{Type: model.EvResourcesSampled, Data: fakeSample{WrittenToday: 70 * gb,
		Agents: []model.WatchAgentUse{{AgentID: w.ID, Written: 15 * gb, Budget: 20 * gb}, {AgentID: "gone", Written: 30 * gb, Budget: 20 * gb}}}})
	if rows := f.Watch().Rows; len(rows) != 0 {
		t.Fatalf("75 %% of a budget and 70 %% of the machine's is fine: %+v", rows)
	}
	f.Publish(model.BusEvent{Type: model.EvResourcesSampled, Data: fakeSample{WrittenToday: 85 * gb,
		Agents: []model.WatchAgentUse{{AgentID: w.ID, Written: 17 * gb, Budget: 20 * gb}}}})
	got := map[string]string{}
	for _, r := range f.Watch().Rows {
		got[r.Key] = r.Status + "|" + r.Text
	}
	if got["budget:"+w.ID] != "high|w1 wrote 17.0 GB today, 85 % of its daily write budget (20.0 GB)" ||
		got["writes:machine"] != "high|all agents together wrote 85.0 GB today, 85 % of the machine's 100.0 GB a day" || len(got) != 2 {
		t.Fatalf("rows: %v", got)
	}
	f.Publish(model.BusEvent{Type: model.EvResourcesSampled, Data: fakeSample{WrittenToday: 120 * gb}})
	rows := f.Watch().Rows
	if len(rows) != 1 || rows[0].Status != "over" || !strings.Contains(rows[0].Text, "over the machine's 100.0 GB a day") {
		t.Fatalf("a new day's sample without the agent, machine over: %+v", rows)
	}
	f.Publish(model.BusEvent{Type: model.EvResourcesSampled, Data: fakeSample{}})
	// Guard events: whatever the guard maps them to lands on the board.
	f.Publish(model.BusEvent{Type: model.EvResourcesGuard, Data: fakeGuardEvent{{Key: "memory", Status: "stop", Text: "memory is critical", Urgent: true}}})
	rows = f.Watch().Rows
	if len(rows) != 1 || rows[0].Key != "memory" || !rows[0].Urgent {
		t.Fatalf("guard event: %+v", rows)
	}
	// Any other event with such data is not read (it may be published with the fleet locked).
	f.Publish(model.BusEvent{Type: model.EvSlotsUpdated, Data: fakeGuardEvent{{Key: "x", Status: "bad", Text: "x"}}})
	if len(f.Watch().Rows) != 1 {
		t.Fatal("a foreign event fed the board")
	}
}

func TestWatchPeerOffline(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	now := f.watch.board.now()
	peers := []PeerHealth{{Name: "hosta", OK: true, Seen: now.UnixMilli()}, {Name: "hostb", OK: false}}
	f.SetPeerHealth(func() []PeerHealth { return peers })
	f.WatchTick()
	if rows := f.Watch().Rows; len(rows) != 0 {
		t.Fatalf("a peer that just did not answer is reported: %+v", rows)
	}
	peers[0] = PeerHealth{Name: "hosta", OK: false, Seen: now.Add(-2 * time.Minute).UnixMilli()}
	watchClock(f, 2*time.Minute) // hosta 4 min down, hostb 2 min
	f.WatchTick()
	if rows := f.Watch().Rows; len(rows) != 0 {
		t.Fatalf("under five minutes is a blip: %+v", rows)
	}
	watchClock(f, 4*time.Minute)
	f.WatchTick()
	got := map[string]string{}
	for _, r := range f.Watch().Rows {
		got[r.Key] = r.Text
	}
	if !strings.Contains(got["peer:hosta"], "machine hosta has been unreachable since") || !strings.Contains(got["peer:hostb"], "machine hostb has not answered since") || len(got) != 2 {
		t.Fatalf("rows: %v", got)
	}
	peers[0].OK, peers[1].OK = true, true
	f.WatchTick()
	if rows := f.Watch().Rows; len(rows) != 0 {
		t.Fatalf("rows after the peers came back: %+v", rows)
	}
}

func TestWatchStateSeparateFromOwnersMessage(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	head := mustAgent(t, f, "head")
	hp := e.chat.Last(head.ID)
	hp.State(model.StateIdle)
	waitState(t, f, "head", model.StateIdle)
	if _, err := f.SendOwner("head", "all quiet?", ""); err != nil {
		t.Fatal(err)
	}
	if s := hp.Sent(); len(s) != 1 || s[0] != "all quiet?" {
		t.Fatalf("with nothing wrong the owner's words go as written: %q", s)
	}
	f.watch.board.report(model.WatchItem{Key: "disk:C:", Status: "low", Text: "disk is below its free-space reserve: C: has 3.0 GB free", Urgent: true})
	hp.State(model.StateIdle)
	waitState(t, f, "head", model.StateIdle)
	if _, err := f.SendOwner("head", "and now?", ""); err != nil {
		t.Fatal(err)
	}
	s := hp.Sent()
	if len(s) != 2 || s[1] != "and now?" {
		t.Fatalf("owner turn contaminated: %q", s)
	}
	// An owner turn does not consume the pending urgent digest.
	f.WatchTick()
	if n := len(watchMail(t, f, head.ID)); n != 1 {
		t.Fatalf("want separate watch message, got %d", n)
	}
	f.WatchTick()
	if n := len(watchMail(t, f, head.ID)); n != 1 {
		t.Fatalf("duplicate watch message: %d", n)
	}
	// A worker's owner message never carries it.
	w := launch(t, f, "w1", "")
	wp := e.chat.Last(w.ID)
	wp.State(model.StateIdle)
	waitState(t, f, "w1", model.StateIdle)
	if _, err := f.SendOwner("w1", "hello", ""); err != nil {
		t.Fatal(err)
	}
	if s := wp.Sent(); s[len(s)-1] != "hello" {
		t.Fatalf("worker turn: %q", s)
	}
	// prompt_state off: never.
	no := false
	f.watch.board.mu.Lock()
	f.watch.board.cfg = (&config.WatchConfig{PromptState: &no}).Effective()
	f.watch.board.mu.Unlock()
	hp.State(model.StateIdle)
	waitState(t, f, "head", model.StateIdle)
	if _, err := f.SendOwner("head", "third", ""); err != nil {
		t.Fatal(err)
	}
	if s := hp.Sent(); s[len(s)-1] != "third" {
		t.Fatalf("prompt_state off: %q", s[len(s)-1])
	}
}

func TestWatchDailyDigestFromTheFleet(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	head := mustAgent(t, f, "head")
	hp := e.chat.Last(head.ID)
	hp.State(model.StateIdle)
	waitState(t, f, "head", model.StateIdle)
	const gb = 1 << 30
	f.Publish(model.BusEvent{Type: model.EvResourcesSampled, Data: fakeSample{WrittenToday: 12 * gb, MemFree: 20 * gb, MemTotal: 64 * gb, DiskFree: 200 * gb, DiskTotal: 900 * gb}})
	b := f.watch.board
	b.mu.Lock()
	b.d.LastDaily = "2000-01-01"
	at := time.Date(2026, 11, 18, 8, 0, 0, 0, time.Local)
	b.now = func() time.Time { return at }
	b.mu.Unlock()
	before := len(hp.Sent())
	f.WatchTick()
	eventually(t, "daily digest", func() bool { return len(hp.Sent()) == before+1 })
	got := hp.Sent()[before]
	for _, want := range []string{"│ Daily digest, tmx watch on testbox, Wed 18 Nov:", "│ - agents: ", "│ - last 24 h: 0 unexpected exit(s)",
		"│ - today: agents wrote 12.0 GB; memory 20.0 GB of 64.0 GB free; disk 200.0 GB free of 900.0 GB", "│ - no open problems"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}

func TestWatchWithoutAHeadKeepsItsNews(t *testing.T) {
	e := newEnv(t)
	f := e.fleet() // not started: no head yet
	f.watch.board.report(model.WatchItem{Key: "memory", Status: "stop", Text: "memory is critical", Urgent: true})
	f.WatchTick()
	if rows := f.Watch().Rows; len(rows) != 1 || rows[0].Notified != 0 {
		t.Fatalf("an undeliverable message was counted as told: %+v", rows)
	}
}
