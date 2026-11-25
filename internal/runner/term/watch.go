package term

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/harness"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
	"github.com/blackcode-ch/tmx-go/internal/runner/claude"
	"github.com/blackcode-ch/tmx-go/internal/runner/term/screen"
)

// Screen reading for a harness TUI. Every evalEvery the screen is classified (screen.Classify
// with the harness's patterns) and turned into runner events:
//   - working / idle: EvState (idle only after it held for idleHold, so a redraw between
//     two outputs is not "idle", and never within the submit window of a typed message);
//   - a menu (permission prompt, question, dialog): an EvApproval of Tool
//     model.ToolTerminalMenu once it looked the same for menuSettle readings, withdrawn
//     (EvApprovalCancelled) after offMenuSettle readings without it; known first-run gates
//     are answered instead (harness.Gate);
//   - usage limit: working (mail must not be typed into it) and one timeline note.
const (
	evalEvery       = 250 * time.Millisecond
	idleHold        = 600 * time.Millisecond
	menuSettle      = 2
	offMenuSettle   = 3
	keyGap          = 40 * time.Millisecond
	gateRetry       = 3 * time.Second
	progressSettle  = 300 * time.Millisecond  // settle meaningful content independently of cosmetic output
	progressMaxWait = 2 * time.Second         // streaming output must not wait forever for a settled frame
	typedSettle     = 1500 * time.Millisecond // after a message's Enter, before idle counts again
	pickSettle      = 800 * time.Millisecond  // longest wait for a menu's cursor to move after one arrow
	pickPoll        = 40 * time.Millisecond
	pickRetries     = 3 // arrows that did not move the cursor before giving up
)

type watcher struct {
	inputMu sync.Mutex // serialize human keys with a mail paste and its delayed Enter
	p       *process
	info    harness.Info
	spec    runner.Spec
	cmd     harness.Command
	scr     *screen.Screen
	start   time.Time

	// guarded by p.mu
	lastOutput          time.Time
	outputWriting       bool        // screen write in flight; a frame is not yet settled
	humanInputAt        time.Time   // protects human input before the TUI has echoed it
	ready               bool        // the input box has been seen: sends are typed, not queued
	mailMenuHeld        bool        // only a menu that did not dismiss can hold live mail
	mailTyping          bool        // selected mail owns the paste/Enter transaction, guarded by p.mu
	queued              []mailInput // sends waiting for the input box
	pending             *pendingMenu
	candidate           string // signature of a menu seen but not yet settled
	candN               int
	offMenu             int
	idleReported        time.Time // periodic positive idle evidence repairs a missed fleet transition
	progressText        []string  // last meaningful screen content; cosmetic changes do not replace it
	progressCandidate   []string  // normalized content being settled; never includes spinner/status churn
	progressCandidateAt time.Time
	progressPendingAt   time.Time // first meaningful difference from the settled baseline
	idleSince           time.Time
	busyUntil           time.Time
	usageNoted          string
	gateAt              map[string]time.Time
	seq                 int
	tr                  *transcript // Claude: its conversation file (transcript.go); nil otherwise
	turnInput           bool        // something went in since it was last idle (a typed message, keys, transcript lines)
	inFlight            int         // non-owner keys admitted and not written yet: no menu becomes an approval (guardedWrite)
}

type pendingMenu struct {
	id  string
	sig string
}

type mailInput struct {
	queuedAt     time.Time
	holdLoggedAt time.Time
	menuWait     bool
	launchPrompt bool
	text         string
	ids          []string
	// now identifies owner mail for diagnostics; all sources share delivery rules.
	now bool
}

func newWatcher(p *process, info harness.Info, spec runner.Spec, cmd harness.Command, cols, rows int) *watcher {
	w := &watcher{p: p, info: info, spec: spec, cmd: cmd, start: time.Now(), gateAt: map[string]time.Time{}}
	if info.Name == model.HarnessClaude {
		w.tr = newTranscript(spec)
	}
	// The screen answers the program's terminal queries (cursor position, attributes): a TUI
	// asking at start-up gets an answer even with no viewer attached. The answers are written
	// on their own goroutine: the screen waits for each to be taken while it holds its lock
	// (inside output), and a pty write waits for as long as the program is not reading.
	replies := make(chan []byte, 64)
	go func() {
		for {
			select {
			case b := <-replies:
				_ = p.write(b)
			case <-p.done:
				return
			}
		}
	}()
	w.scr = screen.New(cols, rows, func(b []byte) {
		select {
		case replies <- b:
		default: // 64 answers unread: the program is not reading; drop rather than stall
		}
	})
	return w
}

func (w *watcher) output(b []byte) {
	w.p.mu.Lock()
	w.outputWriting = true
	w.lastOutput = time.Now()
	w.p.mu.Unlock()
	w.scr.Write(b)
	w.p.mu.Lock()
	w.lastOutput = time.Now()
	w.outputWriting = false
	w.p.mu.Unlock()
}

func (w *watcher) run() {
	t := time.NewTicker(evalEvery)
	defer t.Stop()
	for {
		select {
		case <-w.p.done:
			return
		case <-t.C:
			w.evaluate()
		}
	}
}

func (w *watcher) quietLocked() time.Duration {
	if w.lastOutput.IsZero() {
		return time.Since(w.start)
	}
	return time.Since(w.lastOutput)
}

func (w *watcher) classify() screen.Detection {
	w.p.mu.Lock()
	q := w.quietLocked()
	w.p.mu.Unlock()
	return w.scr.Classify(w.info.Patterns, q)
}

// evaluate reads the screen once and emits what changed.
func (w *watcher) evaluate() {
	w.p.mu.Lock()
	progressAt := w.lastOutput
	w.p.mu.Unlock()
	d := w.classify()
	progress := meaningfulScreen(w.scr.Lines())
	empty := w.boxEmpty()
	now := time.Now()
	var gate string
	p := w.p
	p.mu.Lock()
	w.followLocked()
	w.logHeldLocked(now, d, empty)
	switch {
	case d.State.Menu():
		w.offMenu, w.idleSince = 0, time.Time{}
		if w.inFlight > 0 {
			break // a non-owner key admitted before this menu is still going in: read it once it landed
		}
		if label, ok := w.gateFor(d); ok {
			// Answered once the dialog held still (a menu drawn a moment ago may not take
			// keys yet), and not again while it is still on its way out.
			sig := d.Signature()
			if w.candidate != sig {
				w.candidate, w.candN = sig, 1
			} else if w.candN++; w.candN >= menuSettle && now.Sub(w.gateAt[sig]) > gateRetry {
				w.gateAt[sig] = now
				gate = label
				p.emitLocked(runner.Event{Kind: runner.EvTimeline, Entry: &model.TimelineEntry{
					Kind: model.EntrySystem, Text: fmt.Sprintf("tmx answered %s's start-up screen: %s", w.info.Label, gateText(label))}})
			}
			break
		}
		w.menuLocked(d)
	default:
		w.candidate, w.candN = "", 0
		if w.pending != nil {
			if w.offMenu++; w.offMenu < offMenuSettle {
				break
			}
			p.emitLocked(runner.Event{Kind: runner.EvApprovalCancelled, Approval: &runner.ApprovalRequest{ID: w.pending.id}})
			w.pending = nil
		}
		switch d.State {
		case screen.Working:
			w.idleSince = time.Time{}
			p.setStateLocked(model.StateWorking)

		case screen.UsageLimit:
			p.setStateLocked(model.StateWorking)
			if w.usageNoted != d.Prompt {
				w.usageNoted = d.Prompt
				p.emitLocked(runner.Event{Kind: runner.EvTimeline, Entry: &model.TimelineEntry{Kind: model.EntrySystem,
					IsError: true, Text: fmt.Sprintf("%s hit its usage limit; try again at %s", w.info.Label, d.Prompt)}})
			}
		case screen.Idle:
			if w.mailTyping {
				break
			}
			if !empty {
				w.idleSince = time.Time{}
				break
			}
			if !w.submitSettledLocked(now) {
				break
			}
			if w.idleSince.IsZero() {
				w.idleSince = now
			}
			if now.Sub(w.idleSince) < idleHold {
				break
			}
			w.ready = true

			if p.state != model.StateIdle { // back at its input box
				if p.state == model.StateWorking || p.state == model.StateWaiting {
					w.turnEndedLocked(now)
				}
				w.turnInput = false
			}
			wasIdle := p.state == model.StateIdle
			p.setStateLocked(model.StateIdle)
			if !wasIdle || now.Sub(w.idleReported) >= 2*time.Second {
				if wasIdle {
					p.emitLocked(runner.Event{Kind: runner.EvState, State: model.StateIdle})
				}
				w.idleReported = now
			}
		}
	}
	// A new PTY chunk before/during this reading invalidates its snapshot. Do
	// not replace the settled baseline with a partial clear-and-restore frame.
	if !w.outputWriting && w.lastOutput == progressAt {
		w.settleProgressLocked(d.State, progress, time.Now())
	}
	p.mu.Unlock()
	if gate != "" {
		_ = w.pick(gate, nil)
	}
	// State observation never gates delivery. A formerly open menu is the
	// sole exception: resume the FIFO once a normal composer is observed.
	if d.State == screen.Idle || d.State == screen.Working {
		p.mu.Lock()
		w.mailMenuHeld = false
		p.mu.Unlock()
		w.pumpMail()
	}
}

// menuLocked turns a settled menu into a pending approval.
func (w *watcher) menuLocked(d screen.Detection) {
	p := w.p
	sig := d.Signature()
	if w.pending != nil && w.pending.sig == sig {
		p.setStateLocked(model.StateWaiting)
		return
	}
	if w.candidate != sig {
		w.candidate, w.candN = sig, 1
	} else {
		w.candN++
	}
	if w.candN < menuSettle {
		return
	}
	if w.pending != nil {
		p.emitLocked(runner.Event{Kind: runner.EvApprovalCancelled, Approval: &runner.ApprovalRequest{ID: w.pending.id}})
	}
	w.seq++
	w.pending = &pendingMenu{id: fmt.Sprintf("menu-%d", w.seq), sig: sig}
	kind := model.ApprovalQuestion
	if d.State == screen.Approval || d.State == screen.Trust {
		kind = model.ApprovalPermission
	}
	input, _ := json.Marshal(map[string]any{"harness": w.info.Name, "screen": string(d.State), "selected": d.Selected, "choices": d.Choices})
	prompt := d.Prompt
	if prompt == "" {
		prompt = w.info.Label + " is asking something on its screen."
	}
	p.emitLocked(runner.Event{Kind: runner.EvApproval, Approval: &runner.ApprovalRequest{
		ID: w.pending.id, Kind: kind, Tool: model.ToolTerminalMenu, Input: input,
		Description: menuDescription(w.info.Label, d.State), Question: prompt, Options: d.Labels(),
	}})
	p.setStateLocked(model.StateWaiting)
}

func menuDescription(label string, s screen.State) string {
	switch s {
	case screen.Approval:
		return label + " asks for permission"
	case screen.Trust:
		return label + " asks whether to trust this folder"
	case screen.Locked:
		return label + " says the conversation is open elsewhere"
	case screen.Question:
		return label + " asks a question"
	}
	return label + " shows a menu"
}

// keepDefault is the gate answer "press Enter on the highlighted choice".
const keepDefault = "\x00enter"

func gateText(label string) string {
	if label == keepDefault {
		return "kept the default"
	}
	return label
}

// gateFor: the answer to a known start-up screen, if d is one tmx may answer.
func (w *watcher) gateFor(d screen.Detection) (label string, ok bool) {
	text := strings.Join(w.scr.Lines(), "\n")
	for _, g := range w.info.Gates {
		if !g.Rx.MatchString(text) {
			continue
		}
		if g.Trust && !harness.TrustableFolder(w.spec.Cwd) {
			return "", false
		}
		if g.Label == "" {
			return keepDefault, true
		}
		if _, err := screen.PlanAnswer(d, g.Label, nil); err != nil {
			return "", false
		}
		return g.Label, true
	}
	return "", false
}

// pick selects choice in the menu on the screen one step at a time: it presses one arrow,
// reads the screen until the highlight has moved, and presses Enter only once the
// highlighted option is the right one (keys sent while a dialog is still starting are lost;
// an Enter on the wrong option is not).
func (w *watcher) pick(choice string, syn screen.Synonyms) error {
	if choice == keepDefault {
		return w.press("enter")
	}
	d := w.classify()
	for stuck, steps := 0, 0; stuck < pickRetries && steps < 4*len(d.Choices)+4; steps++ {
		if !d.State.Menu() {
			return errors.New("the menu left the screen before it was answered")
		}
		keys, err := screen.PlanAnswer(d, choice, syn)
		if err != nil {
			return err
		}
		step, _, _ := strings.Cut(keys, " ")
		if step != "up" && step != "down" && step != "left" && step != "right" {
			return w.press(keys) // "enter" on the right option, or a digit
		}
		before := d.Selected
		if err := w.press(step); err != nil {
			return err
		}
		for deadline := time.Now().Add(pickSettle); ; {
			time.Sleep(pickPoll)
			if d = w.classify(); d.Selected != before || !d.State.Menu() || time.Now().After(deadline) {
				break
			}
		}
		if d.Selected == before {
			stuck++
		} else {
			stuck = 0
		}
	}
	return errors.New("the menu's cursor did not reach the option; answer it in the terminal")
}

// answer resolves the pending menu: d.Answer picks an option (label or number); otherwise
// Allow picks "approve" and deny "deny" (the harness's synonyms). A deny message is typed as
// the next message once the input box is back.
func (w *watcher) answer(id string, d runner.Decision) error {
	cur := w.classify()
	p := w.p
	p.mu.Lock()
	if w.pending == nil || w.pending.id != id {
		p.mu.Unlock()
		return errors.New("that menu is no longer on the screen")
	}
	if !cur.State.Menu() || cur.Signature() != w.pending.sig {
		p.mu.Unlock()
		return errors.New("the menu on the screen changed; answer it in the terminal")
	}
	choice := d.Answer
	if choice == "" {
		choice = "approve"
		if !d.Allow {
			choice = "deny"
		}
	}
	_, err := screen.PlanAnswer(cur, choice, w.info.Synonyms)
	var text, keys string
	switch {
	case err == nil:
	case choice == "deny":
		keys, err = "esc", nil // menus without a "No" are dismissed
	case len(cur.Choices) == 0 && d.Answer != "":
		text, err = d.Answer, nil // a question without options: type the answer
	}
	if err != nil {
		p.mu.Unlock()
		return err
	}
	w.pending, w.candidate, w.candN = nil, "", 0
	w.markBusyLocked()
	p.setStateLocked(model.StateWorking)
	if !d.Allow && d.Answer == "" && strings.TrimSpace(d.Message) != "" {
		w.queued = append(w.queued, mailInput{text: d.Message})
	}
	p.mu.Unlock()
	switch {
	case text != "":
		w.typeText(text)
		return nil
	case keys != "":
		return w.press(keys)
	}
	return w.pick(choice, w.info.Synonyms)
}

// guardedWrite is Process.Write's gate for a harness: while w.pending is set (a menu is on
// screen, p.state == StateWaiting), only the owner may write. Called once per Write
// (fleet/terminal.go's SendKeys writes one key per call), so a multi-key sequence is refused
// key-by-key from the moment it is set pending; a key already written before that stands.
//
// A non-owner may write only an interrupt key (runner.NonOwnerKey: Esc, Ctrl-C), and not while
// a menu is anywhere along the way to becoming an approval: pending, a candidate the detector
// saw once (w.candN > 0, which a gate screen also sets), or on the screen right now but not yet
// read by the detector (its 250 ms poll). The screen is read before p.mu is taken (classify
// takes p.mu itself); a menu drawn after that read is the detector's to catch.
//
// p.mu is never held across the pty write: the write blocks while the program is not reading
// its input (a paste while it is busy printing), and draining its output needs p.mu. Instead
// an admitted key is counted in w.inFlight in the same p.mu section as its check, and the
// detector makes no menu an approval while a key is in flight (evaluate). So a transition
// either lands before the check (the key is refused) or after the key was written, never
// between the two.
func (w *watcher) guardedWrite(b []byte, owner bool) error {
	w.inputMu.Lock()
	defer w.inputMu.Unlock()
	p := w.p
	menuNow := false
	if !owner {
		if !runner.NonOwnerKey(b) {
			return runner.ErrOwnerOnly
		}
		menuNow = w.classify().State.Menu()
	}
	p.mu.Lock()
	if owner {
		w.humanInputAt = time.Now()
	}
	if !owner && (menuNow || w.pending != nil || w.candN > 0) {
		p.mu.Unlock()
		return runner.ErrApprovalPending
	}
	w.turnInput = true // a person typed: what follows is a turn (transcript.go)
	if !owner {
		w.inFlight++
	}
	p.mu.Unlock()

	err := p.write(b)

	if !owner {
		p.mu.Lock()
		w.inFlight--
		p.mu.Unlock()
	}
	return err
}

// Every source uses the same immediate, ordered paste/Enter stream.
func (w *watcher) send(text string) error {
	return w.sendMail(mailInput{text: text})
}

func (w *watcher) sendMail(input mailInput) error {
	if input.queuedAt.IsZero() {
		input.queuedAt = time.Now()
	}
	w.p.mu.Lock()
	w.queued = append(w.queued, input)
	w.mailStage(input, "input_queue")
	w.p.mu.Unlock()
	w.pumpMail()
	return nil
}

// One FIFO for every source. Selection happens under p.mu before inputMu is
// acquired, so a newer arrival cannot overtake a selected paste waiting on keys.
func (w *watcher) pumpMail() {
	select {
	case <-w.p.done:
		return
	default:
	}
	w.p.mu.Lock()
	if w.mailTyping || w.mailMenuHeld || len(w.queued) == 0 {
		w.p.mu.Unlock()
		return
	}
	input := w.queued[0]
	// OpenCode needs a mounted composer. Claude's first launch prompt needs
	// positive evidence that startup has finished before the paste/Enter pair.
	if !w.ready && (w.info.Name == model.HarnessOpenCode || input.launchPrompt) {
		w.p.mu.Unlock()
		return
	}
	w.queued = w.queued[1:]
	w.mailTyping = true
	w.markBusyLocked() // state observation only; never a delivery gate
	w.p.setStateLocked(model.StateWorking)
	w.p.mu.Unlock()
	w.typeMail(input)
}

// This timer protects idle state observation only; mail delivery ignores it.
func (w *watcher) submitSettledLocked(now time.Time) bool {
	return !now.Before(w.busyUntil)
}

func (w *watcher) markBusyLocked() {
	w.idleSince = time.Time{}
	w.busyUntil = time.Now().Add(w.info.SubmitDelay + typedSettle)
}

// typeText pastes text (bracketed when the program asked for it) and presses Enter after
// the harness's submit delay.
func (w *watcher) typeText(text string) {
	w.typeMail(mailInput{text: text})
}

func (w *watcher) typeMail(input mailInput) {
	text := input.text
	w.mailStage(input, "input_lock_wait")
	w.inputMu.Lock()
	w.mailStage(input, "composer_recheck")
	// Esc may decline/cancel a tool approval. Never concatenate arbitrary text
	// behind it until the menu has actually left the screen.
	if !w.dismissMailMenu(input) {
		input.menuWait = true
		w.p.mu.Lock()
		w.queued = append([]mailInput{input}, w.queued...)
		w.mailTyping, w.mailMenuHeld = false, true
		w.p.emitLocked(runner.Event{Kind: runner.EvMailDelivery, MessageIDs: input.ids, Delivery: "menu_wait"})
		w.p.mu.Unlock()
		w.inputMu.Unlock()
		return
	}
	empty := w.boxEmpty()
	w.p.mu.Lock()
	if input.menuWait {
		// Once pasting resumes, exit recovery must no longer treat this as unpasted.
		w.p.emitLocked(runner.Event{Kind: runner.EvMailDelivery, MessageIDs: input.ids, Delivery: "dispatching"})
	}
	if input.now && !input.holdLoggedAt.IsZero() {
		log.Printf("terminal owner queue released agent=%s messages=%v after=%.1fs", w.spec.AgentID, input.ids, time.Since(input.queuedAt).Seconds())
	}
	if w.tr != nil {
		w.tr.note(text)
	}
	w.turnInput = true
	w.p.mu.Unlock()
	text = strings.ReplaceAll(runner.PlainText(text), "\n", "\r")
	w.mailStage(input, "paste_write")
	var err error
	if !empty {
		// End navigation preserves an existing draft, including wrapped lines.
		err = w.p.write([]byte(strings.Repeat("\x1b[F", len(w.scr.Lines())+1)))
		if err == nil && w.menuNow() {
			err = runner.ErrApprovalPending
		}
		text = "  " + text
	}
	if err == nil {
		err = w.p.write(w.scr.Paste(text))
	}
	if err == nil {
		w.mailStage(input, "submit_delay")
	} else {
		w.mailStage(input, "paste_failed")
	}
	time.AfterFunc(w.info.SubmitDelay, func() {
		defer func() {
			// The next paste starts as soon as this Enter transaction ends.
			// No idle, draft, human-typing, readiness or post-submit timer gate.
			w.inputMu.Unlock()
			w.p.mu.Lock()
			w.mailTyping = false
			w.p.mu.Unlock()
			w.pumpMail()
		}()
		stage := "unconfirmed"
		select {
		case <-w.p.done:
		default:
			// A second look before Enter: a menu or an approval that came up since the text was
			// typed (possible when it went into a working harness) would take the Enter as its
			// answer. Then no Enter: the text stays in the box, the delivery is unconfirmed.
			if err == nil {
				w.mailStage(input, "submit_menu_check")
			}
			if err == nil && !w.menuNow() {
				w.mailStage(input, "enter_write")
				if err = w.p.write([]byte("\r")); err == nil {
					stage = "submitted"
				}
			}
		}
		w.mailStage(input, stage)
		if len(input.ids) > 0 {
			w.p.mu.Lock()
			w.p.emitLocked(runner.Event{Kind: runner.EvMailDelivery, MessageIDs: input.ids, Delivery: stage})
			w.p.mu.Unlock()
		}
	})
}

// Called only while inputMu owns the entire mail transaction. A menu that
// ignores Esc is reported explicitly; text is never fed into its option picker.
func (w *watcher) dismissMailMenu(input mailInput) bool {
	if !w.menuNow() {
		return true
	}
	// A pending approval can outlive its screen. Clear that stale bookkeeping
	// without sending Esc into an already-working agent.
	if d := w.classify(); d.State == screen.Idle || d.State == screen.Working {
		w.clearMailMenu()
		return true
	}
	w.mailStage(input, "menu_escape")
	if err := w.p.write([]byte{0x1b}); err != nil {
		return false
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		d := w.classify()
		if d.State == screen.Idle || d.State == screen.Working {
			w.clearMailMenu()
			return true
		}
		select {
		case <-w.p.done:
			return false
		case <-time.After(pickPoll):
		}
	}
	w.mailStage(input, "menu_wait")
	return false
}

func (w *watcher) clearMailMenu() {
	w.p.mu.Lock()
	defer w.p.mu.Unlock()
	if w.pending != nil {
		w.p.emitLocked(runner.Event{Kind: runner.EvApprovalCancelled, Approval: &runner.ApprovalRequest{ID: w.pending.id}})
	}
	w.pending, w.candidate, w.candN = nil, "", 0
}

type mailProgress struct {
	stage string
	since int64
}

// mailStage logs identifiers and fixed stage names only; never owner words or
// screen contents. Reads stay available while a PTY/input lock is blocked.
func (w *watcher) mailStage(input mailInput, stage string) {
	w.p.recordMailStage(w.spec.AgentID, input.ids, stage)
}

func (p *process) recordMailStage(agentID string, ids []string, stage string) {
nextMessage:
	for _, id := range ids {
		next := mailProgress{stage: stage, since: time.Now().UnixMilli()}
		for {
			previous, loaded := p.deliveryStages.LoadOrStore(id, next)
			if !loaded {
				break
			}
			if previous.(mailProgress).stage == stage {
				continue nextMessage
			}
			if p.deliveryStages.CompareAndSwap(id, previous, next) {
				break
			}
		}
		log.Printf("terminal mail stage agent=%s message=%s stage=%s", agentID, id, stage)
	}
}

func (p *process) MailStage(id string) string {
	stage, _ := p.MailProgress(id)
	return stage
}

func (p *process) MailProgress(id string) (string, int64) {
	if value, ok := p.deliveryStages.Load(id); ok {
		progress := value.(mailProgress)
		return progress.stage, progress.since
	}
	return "", 0
}

// ForgetMailStage is called only after the fleet consumes the submission receipt.
func (p *process) ForgetMailStage(id string) { p.deliveryStages.Delete(id) }

// menuNow: a menu is on the screen, or on its way to becoming an approval.
func (w *watcher) menuNow() bool {
	menu := w.classify().State.Menu()
	w.p.mu.Lock()
	defer w.p.mu.Unlock()
	return menu || w.pending != nil || w.candN > 0
}

// press sends a key spec, one key at a time.
func (w *watcher) press(spec string) error {
	keys, err := screen.Keys(spec)
	if err != nil {
		return err
	}
	for i, k := range keys {
		if i > 0 {
			time.Sleep(keyGap)
		}
		if err := w.p.write(k); err != nil {
			return err
		}
	}
	return nil
}

// watchSession reports the conversation id (EvSession) once it is known to exist: at once
// for a resumed one; for a new one when the harness has written it (Claude's transcript
// for the pre-assigned --session-id; Codex's rollout carrying this launch's marker).
func (w *watcher) watchSession() {
	s := w.spec
	home, _ := os.UserHomeDir()
	var probe func() string
	switch w.info.Name {
	case model.HarnessClaude:
		if id := argAfter(s.ExtraArgs, "--session-id"); id != "" {
			probe = func() string {
				if harness.ClaudeTranscriptExistsIn(claude.ConfigRoot(s.Env), s.Cwd, id) {
					return id
				}
				return ""
			}
		}
	case model.HarnessCodex:
		if s.SessionID == "" && w.cmd.LaunchID != "" {
			codexHome := harness.CodexHome(home)
			probe = func() string { return harness.CodexFindByMarker(codexHome, s.Cwd, w.cmd.LaunchID, w.start) }
		}
	case model.HarnessOpenCode:
		// A new conversation starts without -s (opencode refuses an id it did not create);
		// its id is read back from opencode's database once it exists.
		if s.SessionID == "" {
			db := harness.OpenCodeDBPath(home)
			if s.OpenCodeDataDir != "" {
				db = filepath.Join(s.OpenCodeDataDir, "opencode", "opencode.db")
			}
			probe = func() string { return harness.OpenCodeFindNew(db, s.Cwd, w.start.Add(-2*time.Second)) }
		}
	}
	if probe == nil {
		if s.SessionID != "" {
			w.emitSession(s.SessionID)
		}
		return
	}
	for delay := 2 * time.Second; ; {
		select {
		case <-w.p.done:
			return
		case <-time.After(delay):
		}
		if id := probe(); id != "" {
			w.emitSession(id)
			return
		}
		if time.Since(w.start) > 2*time.Minute {
			delay = 10 * time.Second
		}
	}
}

func (w *watcher) emitSession(id string) {
	w.p.mu.Lock()
	defer w.p.mu.Unlock()
	w.p.emitLocked(runner.Event{Kind: runner.EvSession, SessionID: id})
}

func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, flag+"="); ok {
			return v
		}
	}
	return ""
}

// stripQueryReplies also protects direct runner callers; the fleet filters before
// recording human input, and current viewers filter before sending.
func stripQueryReplies(b []byte) []byte { return runner.StripTerminalQueryReplies(b) }

// lastWords is the screen's last few non-empty lines, joined (for an exit error).
func (w *watcher) lastWords() string {
	var out []string
	lines := w.scr.Lines()
	for i := len(lines) - 1; i >= 0 && len(out) < 6; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			out = append([]string{t}, out...)
		}
	}
	return strings.Join(out, " / ")
}

// logHeldLocked records predicates only, never screen contents or owner words.
// Called under p.mu; each claimed owner message is sampled at most every 30s.
func (w *watcher) logHeldLocked(now time.Time, d screen.Detection, empty bool) {
	for i := range w.queued {
		in := &w.queued[i]
		if !in.now || in.queuedAt.IsZero() || now.Sub(in.queuedAt) < 10*time.Second || (!in.holdLoggedAt.IsZero() && now.Sub(in.holdLoggedAt) < 30*time.Second) {
			continue
		}
		humanAge := float64(-1)
		if !w.humanInputAt.IsZero() {
			humanAge = now.Sub(w.humanInputAt).Seconds()
		}
		log.Printf("terminal owner queue held agent=%s messages=%v after=%.1fs screen=%v process=%s menu=%t candidate=%t not_ready=%t draft_or_placeholder=%t recent_human=%t human_source=owner_terminal_write human_age_s=%.1f submit_settle=%t idle_settle=%t", w.spec.AgentID, in.ids, now.Sub(in.queuedAt).Seconds(), d.State, w.p.state, w.pending != nil || d.State.Menu(), w.candN > 0, !w.ready, !empty, humanAge >= 0 && humanAge < 20, humanAge, now.Before(w.busyUntil), w.idleSince.IsZero() || now.Sub(w.idleSince) < idleHold)
		in.holdLoggedAt = now
	}
}
