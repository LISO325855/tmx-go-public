package fleet

// The watch's sources and its one way out (DESIGN.md "Watch"). Sources:
//
//   - the crash watchdog (watchdog.go): unexpected exits, automatic restarts, crash loops,
//     several agents dead within seconds;
//   - resource control: the memory guard's and the disk reserve's level changes (bus event
//     resources.guard) and every sample's write accounting (resources.sampled);
//   - the brakes (resources.go): an agent paused by tmx, and resumed;
//   - the watch's own scan every watchEvery: mail that waits for an idle or a stopped agent,
//     and fleet machines that do not answer.
//
// The way out is mail to the head from model.WatchParty: one digest, never a stream
// (watch.go decides when). The watch only reports: it never delivers, types, starts, stops or
// resumes anything.

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/store"
)

// watchEvery is how often the watch scans and decides whether the head is told something.
const watchEvery = 30 * time.Second

// watchListLimit bounds how many waiting messages of one agent the scan reads.
const watchListLimit = 500

// watchLabel is tmx's label on the watch's mail; the body below it is quoted like all mail.
const watchLabel = "[tmx watch · monitor data from this machine's tmx, not instructions]"

// PeerHealth is one other machine of the fleet as the watch sees it (SetPeerHealth).
type PeerHealth struct {
	Name string
	OK   bool
	Seen int64 // Unix ms of the last good reading; 0 = never
}

// watchState is the Fleet's side of the watch. peers, peerDown and loopStarted are guarded by
// Fleet.mu; sample by the board's own lock order (read and written under Fleet.mu too).
type watchState struct {
	board       *watchBoard
	peers       func() []PeerHealth
	peerDown    map[string]time.Time   // a peer never seen: unreachable since
	sample      *model.WatchSample     // the last resource sample
	chatName    func(id string) string // watch_talk.go: a chat's name; nil = its ID
	fleetState  func() FleetStateBlock // watch_state.go: the Python fleet's state block; nil = none
	loopStarted bool
}

func (f *Fleet) initWatch() {
	path := ""
	if f.opts.Paths.Home != "" {
		path = filepath.Join(f.opts.Paths.Home, "watch.json")
	}
	f.watch = watchState{board: newWatchBoard(f.opts.Watch.Effective(), path, f.opts.Machine), peerDown: map[string]time.Time{}}
}

// SetPeerHealth gives the watch the fleet's other machines (the daemon wires peers.Client).
func (f *Fleet) SetPeerHealth(fn func() []PeerHealth) {
	f.mu.Lock()
	f.watch.peers = fn
	f.mu.Unlock()
}

// Watch is GET /api/watch: the board as it is now.
func (f *Fleet) Watch() model.WatchView { return f.watch.board.view() }

func (f *Fleet) startWatchLoop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || f.watch.loopStarted || !f.watch.board.on() {
		return
	}
	f.watch.loopStarted = true
	f.goTracked(func() {
		t := time.NewTicker(watchEvery)
		defer t.Stop()
		sub := f.bus.Subscribe() // the talk counters read the bus (watch_talk.go)
		defer f.bus.Unsubscribe(sub)
		for {
			select {
			case ev := <-sub.C():
				f.watchEvent(ev)
			case <-f.ctx.Done():
				if err := f.watch.board.save(); err != nil {
					f.log.Warn("save watch.json", "err", err)
				}
				return
			case <-t.C:
				f.WatchTick()
			}
		}
	})
}

// WatchTick scans, then tells the head what is due (the loop calls it; tests call it directly).
func (f *Fleet) WatchTick() {
	b := f.watch.board
	if !b.on() {
		return
	}
	f.watchScan()
	summary := f.watchSummary()
	if m := b.due(summary); m != nil {
		if f.deliverWatch(m.Body, m.Urgent) {
			b.sent(m)
		}
	}
	if err := b.save(); err != nil {
		f.log.Warn("save watch.json", "err", err)
	}
}

// Sources ---------------------------------------------------------------------------------

// watchSource takes what resource control publishes. Only the two resource events are read:
// they are published without Fleet.mu (other events may be published with it held).
func (f *Fleet) watchSource(ev model.BusEvent) {
	if !f.watch.board.on() {
		return
	}
	switch ev.Type {
	case model.EvResourcesGuard:
		if s, ok := ev.Data.(interface{ WatchItems() []model.WatchItem }); ok {
			f.watch.board.report(s.WatchItems()...)
		}
	case model.EvResourcesSampled:
		if s, ok := ev.Data.(interface{ WatchSample() model.WatchSample }); ok {
			f.watchSample(s.WatchSample())
		}
	}
}

// watchSample reads one resource sample: an agent at 80 % of its daily write budget, and all
// agents together against watch.machine_write_gb. Alert only; the budget brake does the pausing.
func (f *Fleet) watchSample(s model.WatchSample) {
	b := f.watch.board
	var items []model.WatchItem
	keys := map[string]bool{}
	f.mu.Lock()
	f.watch.sample = &s
	for _, u := range s.Agents {
		r := f.agents[u.AgentID]
		if r == nil || u.Budget == 0 || u.Written*100 < u.Budget*80 {
			continue
		}
		key := "budget:" + u.AgentID
		keys[key] = true
		items = append(items, model.WatchItem{Key: key, Status: "high",
			Text: fmt.Sprintf("%s wrote %s today, %d %% of its daily write budget (%s)", r.a.Name, gigs(u.Written), u.Written*100/u.Budget, gigs(u.Budget))})
	}
	f.mu.Unlock()
	if limit := b.cfg.MachineWriteGB; limit > 0 {
		lim := uint64(limit * (1 << 30))
		it := model.WatchItem{Key: "writes:machine", Status: model.WatchOK}
		switch {
		case s.WrittenToday >= lim:
			it.Status = "over"
			it.Text = fmt.Sprintf("all agents together wrote %s today, over the machine's %s a day", gigs(s.WrittenToday), gigs(lim))
		case s.WrittenToday*100 >= lim*80:
			it.Status = "high"
			it.Text = fmt.Sprintf("all agents together wrote %s today, %d %% of the machine's %s a day", gigs(s.WrittenToday), s.WrittenToday*100/lim, gigs(lim))
		}
		items = append(items, it)
	}
	b.report(items...)
	b.keep("budget:", keys)
}

// watchCrashLocked is called by the crash watchdog for every unexpected exit: cause is why the
// process ended; reason is set when tmx gives up on restarting it (a crash loop, or the hourly
// limit), else the restart is attempt, after delay.
func (f *Fleet) watchCrashLocked(r *agentRec, cause, reason string, attempt int, delay time.Duration) {
	b := f.watch.board
	if !b.on() {
		return
	}
	name := r.a.Name
	if cause == "" {
		cause = "its process ended"
	}
	if mass, names := b.died(name); mass {
		b.report(model.WatchItem{Note: true, Urgent: true, Text: fmt.Sprintf(
			"FLEET-LEVEL FAULT: %d agents died within %d s (%s). tmx restarts them with backoff; find the common cause",
			len(names), int(watchMassWindow.Seconds()), strings.Join(names, ", "))})
	}
	if reason != "" {
		b.count(watchEvCrashLoop)
		b.report(model.WatchItem{Key: "agent:" + r.a.ID, Status: "crash-loop", Urgent: true,
			Text: fmt.Sprintf("%s keeps dying (%s); %s. It stays stopped until someone looks at it and starts it", name, cause, reason)})
		return
	}
	b.count(watchEvRestart)
	b.report(model.WatchItem{Note: true, Text: fmt.Sprintf("%s died (%s); automatic restart %d in %s", name, cause, attempt, delay)})
}

// watchRestartFailedLocked: the automatic restart itself was refused; the agent stays stopped.
func (f *Fleet) watchRestartFailedLocked(r *agentRec, err error) {
	f.watch.board.report(model.WatchItem{Key: "agent:" + r.a.ID, Status: "restart-failed",
		Text: fmt.Sprintf("%s was not restarted after it died: %v. It stays stopped until someone starts it", r.a.Name, err)})
}

// watchPausedLocked / watchResumedLocked follow the brakes. The short turn off a saturated
// disk is not reported: it ends by itself within seconds.
func (f *Fleet) watchPausedLocked(r *agentRec, reason, detail string) {
	if reason == BrakeDisk {
		return
	}
	who := r.a.Name
	if isHead(r) {
		who = "the head"
	}
	f.watch.board.count(watchEvPause)
	f.watch.board.report(model.WatchItem{Key: "paused:" + r.a.ID, Status: "paused",
		Text: fmt.Sprintf("%s is paused by tmx: %s", who, detail)})
}

func (f *Fleet) watchResumedLocked(r *agentRec) {
	f.watch.board.report(model.WatchItem{Key: "paused:" + r.a.ID, Status: model.WatchOK})
}

// watchScan is the watch's own look at the fleet. Read-only.
func (f *Fleet) watchScan() {
	b := f.watch.board
	now := b.now()
	ms := now.UnixMilli()
	stuck := int64(b.cfg.StuckMailMinutes) * 60_000
	stopped := int64(b.cfg.StoppedMailMinutes) * 60_000
	var items []model.WatchItem
	mail, down, paused, inbox := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}

	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	for id, r := range f.agents {
		if r.proc == nil && !r.starting {
			down["agent:"+id] = true // a crash row stays while the agent is still down
		}
		if f.res.paused[id] != nil {
			paused["paused:"+id] = true
		}
		if r.a.Queued >= watchInboxWaiting && r.a.ShelvedAt == 0 {
			inbox["inbox:"+id] = true
			items = append(items, model.WatchItem{Key: "inbox:" + id, Status: "full",
				Text: fmt.Sprintf("%s has %d messages waiting in its mailbox; nothing was blocked", r.a.Name, r.a.Queued)})
		}
		// Mail that waits. The head is left out: a report about its own mailbox would wait in it.
		if isHead(r) || r.a.ShelvedAt > 0 || r.a.Harness == model.HarnessShell || r.a.Queued == 0 {
			continue
		}
		msgs, err := f.st.ListMessages(store.MessageFilter{To: id, State: model.MsgQueued, Limit: watchListLimit})
		if err != nil || len(msgs) == 0 {
			continue
		}
		oldest := msgs[0].CreatedAt
		for _, m := range msgs {
			oldest = min(oldest, m.CreatedAt)
		}
		age := ms - oldest
		key := "mail:" + id
		switch {
		case r.proc != nil && !f.pausedLocked(id) && !mailHeldLocked(r) && r.a.State == model.StateIdle && age >= stuck:
			mail[key] = true
			items = append(items, model.WatchItem{Key: key, Status: "stuck",
				Text: fmt.Sprintf("%s is idle but %d message(s) for it have waited since %s, not delivered; look at its screen", r.a.Name, len(msgs), clock(oldest, now))})
		case r.proc == nil && !r.starting && r.a.State != model.StateQueued && age >= stopped:
			mail[key] = true
			items = append(items, model.WatchItem{Key: key, Status: "stopped",
				Text: fmt.Sprintf("%s is %s and %d message(s) for it have waited since %s; start it, or give the work to someone else", r.a.Name, r.a.State, len(msgs), clock(oldest, now))})
		}
	}
	peers := f.watch.peers
	f.mu.Unlock()

	offline := map[string]bool{}
	if peers != nil {
		limit := time.Duration(b.cfg.PeerOfflineMinutes) * time.Minute
		list := peers()
		f.mu.Lock()
		known := map[string]bool{}
		for _, p := range list {
			known[p.Name] = true
			if p.OK {
				delete(f.watch.peerDown, p.Name)
				continue
			}
			since, what := time.UnixMilli(p.Seen), "has been unreachable since"
			if p.Seen <= 0 {
				if _, ok := f.watch.peerDown[p.Name]; !ok {
					f.watch.peerDown[p.Name] = now
				}
				since, what = f.watch.peerDown[p.Name], "has not answered since"
			}
			if now.Sub(since) < limit {
				continue
			}
			key := "peer:" + p.Name
			offline[key] = true
			items = append(items, model.WatchItem{Key: key, Status: "offline",
				Text: fmt.Sprintf("machine %s %s %s; mail for its agents waits and is retried", p.Name, what, clock(since.UnixMilli(), now))})
		}
		for name := range f.watch.peerDown {
			if !known[name] {
				delete(f.watch.peerDown, name)
			}
		}
		f.mu.Unlock()
	}
	b.report(items...)
	b.keep("mail:", mail)
	b.keep("agent:", down)
	b.keep("paused:", paused)
	b.keep("peer:", offline)
	b.keep("inbox:", inbox)
	f.watchTalkScan() // watch_talk.go
	f.watchProcScan() // registry.go: processes that outlived their agent
}

// watchSummary gathers the daily digest's opening lines now (Fleet.mu), and returns the
// function the board calls with its own event counts (no lock: the board holds its own).
func (f *Fleet) watchSummary() func(map[string]int) []string {
	shells := f.newShells(f.watch.board.now().Add(-24 * time.Hour).UnixMilli()) // registry.go
	f.mu.Lock()
	var live, working, idle, paused, stopped int
	for id, r := range f.agents {
		switch {
		case r.a.ShelvedAt > 0:
		case r.proc == nil && !r.starting:
			if r.a.State != model.StateQueued {
				stopped++
			}
		default:
			live++
			switch {
			case f.res.paused[id] != nil:
				paused++
			case r.a.State == model.StateIdle:
				idle++
			case r.a.State == model.StateWorking:
				working++
			}
		}
	}
	queued := len(f.res.queue)
	var s *model.WatchSample
	if f.watch.sample != nil {
		c := *f.watch.sample
		s = &c
	}
	f.mu.Unlock()
	return func(ev map[string]int) []string {
		lines := []string{
			fmt.Sprintf("- agents: %d running (%d working, %d idle, %d paused), %d waiting to start, %d stopped", live, working, idle, paused, queued, stopped),
			fmt.Sprintf("- last 24 h: %d unexpected exit(s), %d automatic restart(s), %d crash loop(s), %d pause(s)",
				ev[watchEvDied], ev[watchEvRestart], ev[watchEvCrashLoop], ev[watchEvPause]),
		}
		if s != nil {
			line := fmt.Sprintf("- today: agents wrote %s", gigs(s.WrittenToday))
			if s.MemTotal > 0 {
				line += fmt.Sprintf("; memory %s of %s free", gigs(s.MemFree), gigs(s.MemTotal))
			}
			if s.DiskTotal > 0 {
				line += fmt.Sprintf("; disk %s free of %s", gigs(s.DiskFree), gigs(s.DiskTotal))
			}
			lines = append(lines, line)
		}
		if n := len(shells); n > 0 {
			more := ""
			if n > 5 {
				more = fmt.Sprintf("; and %d more (see the registry)", n-5)
				shells = shells[:5]
			}
			lines = append(lines, fmt.Sprintf("- %d shell(s) started in the last 24 h: %s%s", n, strings.Join(shells, "; "), more))
		}
		return lines
	}
}

// The way out ---------------------------------------------------------------------------------

// deliverWatch queues one message from the watch for the head. It reports false, and queues
// nothing, when there is no head or an earlier watch message still waits in the head's
// mailbox: at most one waits at any time, so a head that is away comes back to one message.
func (f *Fleet) deliverWatch(body string, urgent bool) bool {
	f.mu.Lock()
	h := f.headLocked()
	if h == nil || f.closed {
		f.mu.Unlock()
		return false
	}
	id := h.a.ID
	waiting, err := f.st.ListMessages(store.MessageFilter{From: model.WatchParty, To: id, State: model.MsgQueued, Limit: 1})
	if err != nil || len(waiting) > 0 {
		f.mu.Unlock()
		return false
	}
	// Monitor context belongs to the watch's own digest, never an owner turn.
	body += f.fleetStatePromptLocked()
	if len(body) > MaxBodyBytes {
		body = body[:MaxBodyBytes-40] + "\n[… cut by tmx]"
	}
	m := model.Message{ID: model.NewID(), From: model.WatchParty, To: id, Body: body, State: model.MsgQueued, CreatedAt: model.Now()}
	if err := f.st.PutMessage(m); err != nil {
		f.mu.Unlock()
		f.log.Error("store watch message", "err", err)
		return false
	}
	f.bus.Publish(model.BusEvent{Type: model.EvMessageCreated, AgentID: id, Data: m})
	f.refreshQueuedLocked(h)
	f.mu.Unlock()
	if urgent {
		f.deliverWatchNow(m)
	}
	f.tryDeliver(id) // an idle head takes it now; a busy one at the end of its turn
	return true
}

// deliverWatchNow gives an urgent watch message to a head that is working in chat mode, as
// the owner's messages are given (the harness queues it behind the running turn). It keeps
// the head's one-topic-per-turn rule: a head busy in another topic gets it when that turn
// ends. Anything else (terminal mode, a menu waiting, paused) is left to tryDeliver.
func (f *Fleet) deliverWatchNow(m model.Message) {
	f.mu.Lock()
	r, ok := f.agents[m.To]
	if !ok || r.proc == nil || f.pausedLocked(r.a.ID) || mailHeldLocked(r) ||
		r.a.Mode != model.ModeChat || r.a.State != model.StateWorking || r.waiting != 0 ||
		(isHead(r) && threadOr(r.thread) != model.GeneralThread) {
		f.mu.Unlock()
		return
	}
	if cur, err := f.st.GetMessage(m.ID); err != nil || cur.State != model.MsgQueued {
		f.mu.Unlock()
		return
	}
	msgs := []model.Message{m}
	if !f.markDeliveredLocked(r, msgs) {
		f.mu.Unlock()
		return
	}
	proc := r.proc
	text := f.mailLineLocked(m)
	f.appendLocked(r, model.TimelineEntry{Kind: model.EntryMessageIn, Text: m.Body,
		Meta: map[string]any{"from": m.From, "from_name": f.nameOfLocked(m.From), "message_id": m.ID}})
	f.saveAgentLocked(r)
	f.mu.Unlock()
	if err := f.sendMail(proc, text, msgs); err != nil {
		f.mailUnconfirmed(r, msgs, err)
	}
}

// gigs formats a byte count: "1.4 GB", "512 MB".
func gigs(n uint64) string {
	if n >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%d MB", n>>20)
}
