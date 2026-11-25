package fleet

// The watch board (DESIGN.md "Watch"): the latest state of everything tmx watches on this
// machine, not a stream of events (parity with Python tmx's statedash.py). Sources report
// readings (watchBoard.report); the board keeps one row per key and tells the head
//
//   - ONE digest, only when something not-ok is new, at most every DigestMinutes;
//   - urgent rows at once, once per episode (and at most once per DigestMinutes per key, so a
//     reading that flaps at its line cannot become a stream);
//   - one daily digest.
//
// The board is pure bookkeeping: it has its own mutex, never calls into the Fleet, and is safe
// to report to with Fleet.mu held. watch_fleet.go feeds and delivers.

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

const (
	// watchHold: a non-urgent row must hold this long before a digest carries it, so a
	// reading that crosses its line for a moment is never mailed.
	watchHold = 2 * time.Minute
	// watchMaxLines bounds a digest; the rest is counted, and GET /api/watch has all of it.
	watchMaxLines = 12
	// watchPromptLines bounds the state lines under an owner's message to the head.
	watchPromptLines = 6
	watchMaxNotes    = 40
	// Mass death: this many unexpected exits within the window are one fleet-level fault.
	watchMassCount  = 3
	watchMassWindow = 10 * time.Second
	watchMassQuiet  = 10 * time.Minute
	// watchUnconfirmed: rows restored from disk that no source reported again this long after
	// the daemon started are dropped (their source saw nothing wrong).
	watchUnconfirmed = 3 * time.Minute
)

// Event kinds counted for the daily digest.
const (
	watchEvDied      = "died"
	watchEvRestart   = "restart"
	watchEvCrashLoop = "crash-loop"
	watchEvPause     = "pause"
)

type watchNote struct {
	At     int64  `json:"at"`
	Text   string `json:"text"`
	Urgent bool   `json:"urgent,omitempty"`
	// Ride: an "ok again" line. It travels with the next digest but never causes one.
	Ride bool `json:"ride,omitempty"`
}

type watchEvent struct {
	At   int64  `json:"at"`
	Kind string `json:"kind"`
}

// watchFile is watch.json.
type watchFile struct {
	Rows       map[string]*model.WatchRow `json:"rows"`
	Notes      []watchNote                `json:"notes"`
	Events     []watchEvent               `json:"events"`
	UrgentAt   map[string]int64           `json:"urgent_at"`
	LastDigest int64                      `json:"last_digest"`
	LastUrgent int64                      `json:"last_urgent"`
	LastDaily  string                     `json:"last_daily"`
	MassAt     int64                      `json:"mass_at"`
}

type watchBoard struct {
	mu      sync.Mutex
	cfg     config.Watch // guarded by mu
	enabled bool         // cfg.Enabled, fixed at construction: on() reads it without mu
	path    string       // "" = memory only
	machine string
	now     func() time.Time
	d       watchFile
	dirty   bool
	// seen: keys reported since this daemon started (rows from disk start unconfirmed).
	seen    map[string]bool
	started time.Time
	deaths  []watchDeath
	// Talk counters (below): key -> the times of its hits; hitIDs: what was counted already.
	hits   map[string][]int64
	hitIDs map[string]int64
}

type watchDeath struct {
	at   time.Time
	name string
}

func newWatchBoard(cfg config.Watch, path, machine string) *watchBoard {
	b := &watchBoard{cfg: cfg, enabled: cfg.Enabled, path: path, machine: machine, now: time.Now, seen: map[string]bool{}}
	b.d = watchFile{Rows: map[string]*model.WatchRow{}, UrgentAt: map[string]int64{}}
	if path != "" {
		if raw, err := os.ReadFile(path); err == nil {
			var d watchFile
			if json.Unmarshal(raw, &d) == nil {
				if d.Rows == nil {
					d.Rows = map[string]*model.WatchRow{}
				}
				if d.UrgentAt == nil {
					d.UrgentAt = map[string]int64{}
				}
				for k, r := range d.Rows {
					if r == nil || r.Status == "" || r.Status == model.WatchOK {
						delete(d.Rows, k)
					}
				}
				b.d = d
			}
		}
	}
	b.started = b.now()
	if b.d.LastDaily == "" {
		// A new board: the first daily digest is tomorrow's, not a surprise right after install.
		b.d.LastDaily = b.started.Format(time.DateOnly)
		b.dirty = true
	}
	return b
}

func (b *watchBoard) on() bool { return b != nil && b.enabled }

// report takes readings. A row's episode starts when its status changes; an ok reading ends
// it (and, when the head had been told, leaves an "ok again" line for the next digest).
func (b *watchBoard) report(items ...model.WatchItem) {
	if !b.on() || len(items) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now().UnixMilli()
	for _, it := range items {
		text := oneLine(it.Text, 300)
		if it.Note {
			if text != "" {
				b.noteLocked(watchNote{At: now, Text: text, Urgent: it.Urgent})
			}
			continue
		}
		if it.Key == "" {
			continue
		}
		b.seen[it.Key] = true
		row := b.d.Rows[it.Key]
		if it.Status == "" || it.Status == model.WatchOK {
			if row != nil {
				b.clearLocked(it.Key, row, now)
			}
			continue
		}
		if row != nil && row.Status == it.Status {
			if row.Text != text || row.Urgent != it.Urgent {
				row.Text, row.Urgent = text, it.Urgent
				b.dirty = true
			}
			row.Updated = now
			continue
		}
		b.d.Rows[it.Key] = &model.WatchRow{Key: it.Key, Status: it.Status, Text: text, Urgent: it.Urgent, Since: now, Updated: now}
		b.dirty = true
	}
}

func (b *watchBoard) clearLocked(key string, row *model.WatchRow, now int64) {
	if row.Notified > 0 {
		b.noteLocked(watchNote{At: now, Text: "ok again: " + row.Text, Ride: true})
	}
	delete(b.d.Rows, key)
	b.dirty = true
}

func (b *watchBoard) noteLocked(n watchNote) {
	b.d.Notes = append(b.d.Notes, n)
	if over := len(b.d.Notes) - watchMaxNotes; over > 0 {
		b.d.Notes = append(b.d.Notes[:0], b.d.Notes[over:]...)
	}
	b.dirty = true
}

// keep clears the rows under prefix whose key is not in keys: what a source that lists
// everything it watches (the agents of a sample, the fleet's own scan) no longer reports.
func (b *watchBoard) keep(prefix string, keys map[string]bool) {
	if !b.on() {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now().UnixMilli()
	for k, row := range b.d.Rows {
		if strings.HasPrefix(k, prefix) && !keys[k] {
			b.clearLocked(k, row, now)
		}
	}
}

// count records an event for the daily digest.
func (b *watchBoard) count(kind string) {
	if !b.on() {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.d.Events = append(b.d.Events, watchEvent{At: b.now().UnixMilli(), Kind: kind})
	b.dirty = true
}

// died records an unexpected exit and reports whether it completes a mass death (several
// agents within seconds: one fleet-level fault, told once), with the names.
func (b *watchBoard) died(name string) (mass bool, names []string) {
	if !b.on() {
		return false, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	b.d.Events = append(b.d.Events, watchEvent{At: now.UnixMilli(), Kind: watchEvDied})
	b.dirty = true
	kept := b.deaths[:0]
	for _, d := range b.deaths {
		if now.Sub(d.at) <= watchMassWindow {
			kept = append(kept, d)
		}
	}
	b.deaths = append(kept, watchDeath{at: now, name: name})
	if len(b.deaths) < watchMassCount || (b.d.MassAt > 0 && now.UnixMilli()-b.d.MassAt < watchMassQuiet.Milliseconds()) {
		return false, nil
	}
	b.d.MassAt = now.UnixMilli()
	for _, d := range b.deaths {
		names = append(names, d.name)
	}
	return true, names
}

// housekeepLocked drops unconfirmed rows from an earlier daemon, old events and old flap marks.
func (b *watchBoard) housekeepLocked(now time.Time) {
	ms := now.UnixMilli()
	if now.Sub(b.started) >= watchUnconfirmed {
		for k, row := range b.d.Rows {
			if !b.seen[k] {
				b.clearLocked(k, row, ms)
			}
		}
	}
	kept := b.d.Events[:0]
	for _, e := range b.d.Events {
		if ms-e.At < (24 * time.Hour).Milliseconds() {
			kept = append(kept, e)
		}
	}
	if len(kept) != len(b.d.Events) {
		b.d.Events, b.dirty = kept, true
	}
	for k, at := range b.d.UrgentAt {
		if ms-at > (24 * time.Hour).Milliseconds() {
			delete(b.d.UrgentAt, k)
			b.dirty = true
		}
	}
}

// watchMessage is one message for the head and what sending it settles.
type watchMessage struct {
	Body   string
	Urgent bool
	Daily  bool
	rows   []string // keys told
	notes  int      // notes told (the first n)
}

// due decides whether the head is told something now. summary builds the daily digest's
// opening lines (nil = none). The caller sends the message and then calls sent; a message
// that could not be sent settles nothing and is built again on the next tick.
func (b *watchBoard) due(summary func(events map[string]int) []string) *watchMessage {
	if !b.on() {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	ms := now.UnixMilli()
	b.housekeepLocked(now)
	every := time.Duration(b.cfg.DigestMinutes) * time.Minute

	var urgent, held, fresh []*model.WatchRow
	for _, row := range b.sortedRowsLocked() {
		if row.Notified > 0 {
			continue
		}
		switch {
		case row.Urgent && (b.d.UrgentAt[row.Key] == 0 || ms-b.d.UrgentAt[row.Key] >= every.Milliseconds()):
			urgent = append(urgent, row)
		case ms-row.Since >= watchHold.Milliseconds():
			held = append(held, row)
		default:
			fresh = append(fresh, row)
		}
	}
	urgentNote, plainNote := false, false
	for _, n := range b.d.Notes {
		switch {
		case n.Urgent:
			urgentNote = true
		case !n.Ride:
			plainNote = true
		}
	}
	daily := b.cfg.DailyHour >= 0 && now.Hour() >= b.cfg.DailyHour && b.d.LastDaily != now.Format(time.DateOnly)
	digestDue := b.d.LastDigest == 0 || ms-b.d.LastDigest >= every.Milliseconds()
	isUrgent := len(urgent) > 0 || urgentNote
	switch {
	case isUrgent:
	case daily:
	case digestDue && (len(held) > 0 || plainNote):
	default:
		return nil
	}

	m := &watchMessage{Urgent: isUrgent, Daily: daily && !isUrgent, notes: len(b.d.Notes)}
	rows := append(append([]*model.WatchRow{}, urgent...), held...)
	if isUrgent {
		rows = append(rows, fresh...) // the head reads now anyway: everything open, once
	}
	var lines []string
	for _, row := range rows {
		m.rows = append(m.rows, row.Key)
		mark := ""
		if row.Urgent {
			mark = "URGENT: "
		}
		lines = append(lines, fmt.Sprintf("- %s%s (since %s)", mark, row.Text, clock(row.Since, now)))
	}
	var rides []string
	for _, n := range b.d.Notes {
		line := fmt.Sprintf("- %s (%s)", n.Text, clock(n.At, now))
		switch {
		case n.Ride:
			rides = append(rides, line)
		case n.Urgent:
			lines = append([]string{"- URGENT: " + strings.TrimPrefix(line, "- ")}, lines...)
		default:
			lines = append(lines, line)
		}
	}
	lines = append(lines, rides...)
	if m.Daily {
		// The daily digest also lists what the head was told before and is still open.
		for _, row := range b.sortedRowsLocked() {
			if row.Notified > 0 {
				lines = append(lines, fmt.Sprintf("- still open: %s (since %s)", row.Text, clock(row.Since, now)))
			}
		}
	}
	if over := len(lines) - watchMaxLines; over > 0 {
		lines = append(lines[:watchMaxLines-1], fmt.Sprintf("- +%d more: GET /api/watch has every row", over+1))
	}
	var head []string
	switch {
	case m.Urgent:
		head = append(head, fmt.Sprintf("URGENT, tmx watch on %s, %s:", b.machine, now.Format("15:04")))
	case m.Daily:
		head = append(head, fmt.Sprintf("Daily digest, tmx watch on %s, %s:", b.machine, now.Format("Mon 2 Jan")))
		if summary != nil {
			ev := map[string]int{}
			for _, e := range b.d.Events {
				ev[e.Kind]++
			}
			head = append(head, summary(ev)...)
		}
		if len(lines) == 0 {
			head = append(head, "- no open problems")
		}
	default:
		head = append(head, fmt.Sprintf("tmx watch on %s, %s:", b.machine, now.Format("15:04")))
	}
	m.Body = strings.Join(append(head, lines...), "\n")
	return m
}

// sent settles a message that reached the head's mailbox.
func (b *watchBoard) sent(m *watchMessage) {
	if !b.on() || m == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	ms := now.UnixMilli()
	for _, k := range m.rows {
		if row := b.d.Rows[k]; row != nil && row.Notified == 0 {
			row.Notified = ms
			if row.Urgent {
				b.d.UrgentAt[k] = ms
			}
		}
	}
	if m.notes >= len(b.d.Notes) {
		b.d.Notes = nil
	} else {
		b.d.Notes = append([]watchNote{}, b.d.Notes[m.notes:]...)
	}
	b.d.LastDigest = ms
	if m.Urgent {
		b.d.LastUrgent = ms
	}
	if m.Daily {
		b.d.LastDaily = now.Format(time.DateOnly)
	}
	b.dirty = true
}

// promptLines are the open problems for an owner's message to the head (at most
// watchPromptLines; nil = nothing is wrong). The rows shown count as told.
func (b *watchBoard) promptLines() []string {
	if !b.on() {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.cfg.PromptState {
		return nil
	}
	rows := b.sortedRowsLocked()
	if len(rows) == 0 {
		return nil
	}
	now := b.now()
	ms := now.UnixMilli()
	var lines []string
	for i, row := range rows {
		if i == watchPromptLines-1 && len(rows) > watchPromptLines {
			lines = append(lines, fmt.Sprintf("+%d more (GET /api/watch)", len(rows)-i))
			break
		}
		mark := ""
		if row.Urgent {
			mark = "URGENT: "
		}
		lines = append(lines, fmt.Sprintf("%s%s (since %s)", mark, row.Text, clock(row.Since, now)))
		if row.Notified == 0 {
			row.Notified = ms
			if row.Urgent {
				b.d.UrgentAt[row.Key] = ms
			}
			b.dirty = true
		}
	}
	return lines
}

// sortedRowsLocked: urgent first, then the oldest episode first.
func (b *watchBoard) sortedRowsLocked() []*model.WatchRow {
	rows := make([]*model.WatchRow, 0, len(b.d.Rows))
	for _, r := range b.d.Rows {
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Urgent != rows[j].Urgent {
			return rows[i].Urgent
		}
		if rows[i].Since != rows[j].Since {
			return rows[i].Since < rows[j].Since
		}
		return rows[i].Key < rows[j].Key
	})
	return rows
}

func (b *watchBoard) view() model.WatchView {
	v := model.WatchView{Rows: []model.WatchRow{}, Notes: []string{}}
	if b == nil {
		return v
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	v.Enabled, v.DigestEvery = b.cfg.Enabled, b.cfg.DigestMinutes
	for _, r := range b.sortedRowsLocked() {
		v.Rows = append(v.Rows, *r)
	}
	for _, n := range b.d.Notes {
		v.Notes = append(v.Notes, n.Text)
	}
	v.LastDigest, v.LastUrgent, v.LastDaily = b.d.LastDigest, b.d.LastUrgent, b.d.LastDaily
	return v
}

// save writes watch.json when something changed.
func (b *watchBoard) save() error {
	if !b.on() || b.path == "" {
		return nil
	}
	b.mu.Lock()
	if !b.dirty {
		b.mu.Unlock()
		return nil
	}
	raw, err := json.Marshal(b.d)
	b.dirty = false
	b.mu.Unlock()
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(b.path, raw, 0o600)
}

// clock is a time of day for a line; with the date when it is not today.
func clock(ms int64, now time.Time) string {
	t := time.UnixMilli(ms).In(now.Location())
	if t.Format(time.DateOnly) != now.Format(time.DateOnly) {
		return t.Format("2 Jan 15:04")
	}
	return t.Format("15:04")
}

// oneLine keeps a reading on one line of at most limit bytes: no control characters (a
// source's text never starts a new line of the digest).
func oneLine(s string, limit int) string {
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r < ' ' || r == 0x7f || r == ' ' || r == ' ' }), " ")
	s = strings.TrimSpace(s)
	if len(s) > limit {
		cut := limit
		for cut > 0 && s[cut]&0xC0 == 0x80 { // not inside a UTF-8 sequence
			cut--
		}
		s = s[:cut] + "…"
	}
	return s
}

// Talk counters ---------------------------------------------------------------------------
//
// Alert-only counters for how much agents talk (mail, the board, Companion, chats): the
// watch counts and tells the head, it never blocks, pauses or drops anything. In memory only:
// after a restart counting starts again.

const (
	// watchRateKeep is how long a hit is remembered (the longest window any check uses).
	watchRateKeep = 30 * time.Minute
	// watchRateMaxKeys bounds the counters; beyond it new keys are not counted.
	watchRateMaxKeys = 2000
)

// hit counts one event for key now and reports whether it counted. id, when given, makes it
// count once (a bus event that is published again for the same message is not a second message).
func (b *watchBoard) hit(key, id string) bool {
	if !b.on() {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now().UnixMilli()
	if b.hits == nil {
		b.hits, b.hitIDs = map[string][]int64{}, map[string]int64{}
	}
	if id != "" {
		if _, dup := b.hitIDs[key+"\x00"+id]; dup {
			return false
		}
		b.hitIDs[key+"\x00"+id] = now
	}
	if _, ok := b.hits[key]; !ok && len(b.hits) >= watchRateMaxKeys {
		return false
	}
	b.hits[key] = append(b.hits[key], now)
	return true
}

// unhit forgets key's hits (a person wrote in the chat: the agents' run is over).
func (b *watchBoard) unhit(key string) {
	if !b.on() {
		return
	}
	b.mu.Lock()
	delete(b.hits, key)
	b.mu.Unlock()
}

// rates returns, per key under prefix, how many hits fell into the last short and long window.
// It also forgets what is older than watchRateKeep.
func (b *watchBoard) rates(prefix string, short, long time.Duration) map[string][2]int {
	out := map[string][2]int{}
	if !b.on() {
		return out
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now().UnixMilli()
	for key, times := range b.hits {
		kept := times[:0]
		for _, t := range times {
			if now-t < watchRateKeep.Milliseconds() {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			delete(b.hits, key)
			continue
		}
		b.hits[key] = kept
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		var n [2]int
		for _, t := range kept {
			if now-t < short.Milliseconds() {
				n[0]++
			}
			if now-t < long.Milliseconds() {
				n[1]++
			}
		}
		out[key] = n
	}
	for k, t := range b.hitIDs {
		if now-t >= watchRateKeep.Milliseconds() {
			delete(b.hitIDs, k)
		}
	}
	return out
}
