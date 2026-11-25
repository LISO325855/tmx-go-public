package term

import (
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
	"github.com/blackcode-ch/tmx-go/internal/runner/term/screen"
)

// These are harness status rows, not task output. Their spinner, elapsed time,
// token counters and cycling thinking labels can change throughout a stuck turn.
var progressStatus = regexp.MustCompile(`(?i)esc to interrupt|waiting for api response|\btab to queue message\b|\bcontext left\b|\d+% left|^\? for shortcuts$|^working[.\s…]*(\(.*\))?$|^thinking[.\s…]*(\(.*\))?$`)

var progressDigits = regexp.MustCompile(`\p{N}+`)

func meaningfulScreen(lines []string) []string {
	var text []string
	for _, line := range lines {
		// Read rendered cells, never ANSI bytes: cursor movement, colours and
		// repainting identical text cannot manufacture progress.
		line = strings.TrimSpace(strings.Map(func(r rune) rune {
			if r >= 0x2800 && r <= 0x28ff { // animated braille indicators
				return -1
			}
			return r
		}, line))
		line = strings.TrimLeft(line, "✢✳✶✻✽·●•◐◓◑◒◴◷◶◵◰◳◲◱ -/\\|")
		line = strings.Join(strings.Fields(line), " ")
		if progressStatus.MatchString(line) || !strings.ContainsFunc(line, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) {
			continue
		}
		text = append(text, line)
	}
	return text
}

// Settle the normalized body, not the PTY stream: animated status rows may
// redraw indefinitely while completed task output is stable. A partial repaint
// changes this candidate and restarts its timer without replacing the baseline.
// Continuous meaningful changes may advance the baseline after a bounded wait.
func (w *watcher) settleProgressLocked(state screen.State, text []string, now time.Time) {
	normalized := make([]string, len(text))
	for i, line := range text {
		normalized[i] = progressDigits.ReplaceAllString(line, "#")
	}
	if w.progressCandidateAt.IsZero() || !slices.Equal(w.progressCandidate, normalized) {
		w.progressCandidate, w.progressCandidateAt = normalized, now
	}
	if !addedProgress(w.progressText, text) {
		w.progressPendingAt = time.Time{}
	} else if w.progressPendingAt.IsZero() {
		w.progressPendingAt = now
	}
	if now.Sub(w.progressCandidateAt) >= progressSettle ||
		(!w.progressPendingAt.IsZero() && now.Sub(w.progressPendingAt) >= progressMaxWait) {
		w.observeProgressLocked(state, text)
		w.progressPendingAt = time.Time{}
	}
}

// Only added/changed meaningful text counts. Removing text (including a blank
// intermediate repaint) retains the baseline, so clearing and restoring a screen
// cannot keep a stuck agent busy. The baseline is bounded by one screen's rows.
func (w *watcher) observeProgressLocked(state screen.State, text []string) {
	if state == screen.Idle {
		w.progressText = text
		return
	}
	if state != screen.Working && !(state == screen.Starting && w.p.state == model.StateWorking) {
		return
	}
	if addedProgress(w.progressText, text) {
		w.progressText = text
		w.p.emitLocked(runner.Event{Kind: runner.EvProgress})
	}
}

func addedProgress(before, after []string) bool {
	counts := make(map[string]int)
	for _, line := range before {
		counts[progressDigits.ReplaceAllString(line, "#")]++
	}
	for _, line := range after {
		key := progressDigits.ReplaceAllString(line, "#")
		counts[key]--
		if counts[key] < 0 {
			return true // new text, or another appended instance of a numeric row
		}
	}
	// Once the viewport fills, appended batch rows replace scrolled-off rows.
	// Require an exact suffix/prefix overlap: rewriting a countdown in place
	// must not look like appending output. Keep raw digits only for this proof.
	// Fixed headers/footers can surround the scrolling portion of a TUI.
	for len(before) > 0 && len(after) > 0 && before[0] == after[0] {
		before, after = before[1:], after[1:]
	}
	for len(before) > 0 && len(after) > 0 && before[len(before)-1] == after[len(after)-1] {
		before, after = before[:len(before)-1], after[:len(after)-1]
	}
	for drop := 1; drop < len(before); drop++ {
		overlap := len(before) - drop
		if overlap >= len(after) {
			continue
		}
		match := true
		for i := 0; i < overlap; i++ {
			if before[drop+i] != after[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
