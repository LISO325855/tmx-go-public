package term

import (
	"regexp"
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

var codexFooter = regexp.MustCompile(`(?i)(\d+% (?:context )?left|\? for shortcuts|\s·\s(?:[A-Za-z]:\\|~|/))`)

// boxEmpty is emptyComposer for the watcher's screen, and also true when the input
// box holds nothing but its placeholder: text the harness drew as a hint (screen.LinesTyped).
// OpenCode shows an example on its home composer. Claude Code shows a hint after a turn ("❯  Claude Code", "❯  <the session's
// first words>"); taken as a draft, the agent never counted as back at its input box: no end of
// turn, state working for ever, mail never delivered (hosta-go, 17 Nov). What a person typed is
// drawn as text and still counts as a draft.
func (w *watcher) boxEmpty() bool {
	all, typed := w.scr.LinesTyped()
	if emptyComposer(strings.Join(all, "\n"), w.info.Name) {
		return true
	}
	if w.info.Name == model.HarnessOpenCode {
		for i, line := range all {
			if strings.HasPrefix(strings.TrimSpace(line), "┃  Ask anything…") {
				if text := strings.TrimSpace(typed[i]); text == "" || text == "┃" {
					all[i] = "┃"
				}
			}
		}
		return emptyComposer(strings.Join(all, "\n"), w.info.Name)
	}
	if w.info.Name != model.HarnessClaude {
		return false
	}
	changed := false
	for i, l := range all {
		if t := strings.TrimSpace(l); (strings.HasPrefix(t, "❯") || strings.HasPrefix(t, "│ ❯")) && typed[i] != l {
			all[i], changed = typed[i], true
		}
	}
	return changed && emptyComposer(strings.Join(all, "\n"), w.info.Name)
}

// emptyComposer requires positive evidence of an empty input box. Silence, a
// footer alone, and a prompt quoted in a response are not that evidence. Keep
// wrapped rows: a blank first row does not make a multiline draft empty.
func emptyComposer(text string, h model.Harness) bool {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	trim := func(s string) string { return strings.TrimSpace(s) }
	rule := func(s string) bool {
		s = trim(s)
		return len(s) > 0 && strings.Trim(s, "─━═╭╮╰╯ ") == ""
	}
	switch h {
	case model.HarnessClaude, model.HarnessPi:
		bottom := -1
		for i := len(lines) - 1; i >= 0; i-- {
			if !rule(lines[i]) {
				continue
			}
			if bottom < 0 {
				footer := trim(strings.Join(lines[i+1:], "\n"))
				if len(lines)-i > 4 {
					return false
				}
				if h == model.HarnessClaude && !strings.Contains(footer, "? for shortcuts") && !strings.Contains(footer, "shift+tab") && !strings.Contains(footer, " mode on") && !strings.Contains(footer, "permissions on") && !strings.Contains(footer, "accept edits on") {
					return false
				}
				if h == model.HarnessPi && !strings.Contains(footer, "ctrl+c twice to exit") && !strings.Contains(footer, "ctrl+d to exit") {
					return false
				}
				bottom = i
				continue
			}
			body := trim(strings.Join(lines[i+1:bottom], "\n"))
			if h == model.HarnessPi {
				return body == ""
			}
			return body == "❯" || body == ">" || body == "│ ❯" || body == "│ >"
		}
	case model.HarnessCodex:
		for i := len(lines) - 1; i >= 0; i-- {
			line := trim(lines[i])
			if !strings.HasPrefix(line, "›") {
				continue
			}
			if line != "›" {
				return false
			}
			for j := i + 1; j < len(lines); j++ {
				line = trim(lines[j])
				if line == "" {
					continue
				}
				// A known status footer must immediately follow the empty box.
				return len(lines)-j <= 2 && codexFooter.MatchString(line)
			}
			return false
		}
	case model.HarnessOpenCode:
		for i := len(lines) - 1; i > 0; i-- {
			if strings.HasPrefix(trim(lines[i]), "┃  Build ") || strings.HasPrefix(trim(lines[i]), "┃  Plan ") {
				if !strings.Contains(strings.Join(lines[i+1:min(i+4, len(lines))], "\n"), "tab agents") {
					return false
				}
				seen := false
				for j := i - 1; j >= 0; j-- {
					line := trim(lines[j])
					if !strings.HasPrefix(line, "┃") {
						break
					}
					if line != "┃" {
						return false
					}
					seen = true
				}
				return seen
			}
		}
	case "agy", "antigravity":
		bottom := -1
		for i := len(lines) - 1; i >= 0; i-- {
			if !rule(lines[i]) {
				continue
			}
			if bottom < 0 {
				if len(lines)-i > 4 || !strings.Contains(strings.Join(lines[i+1:], "\n"), "? for shortcuts") {
					return false
				}
				bottom = i
				continue
			}
			var body []string
			for _, line := range lines[i+1 : bottom] {
				body = append(body, strings.Trim(strings.TrimSpace(line), "│ "))
			}
			return strings.TrimSpace(strings.Join(body, "\n")) == ">"
		}
	}
	return false
}
