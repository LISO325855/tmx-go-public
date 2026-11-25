package transcribe

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/blackcode-ch/tmx-go/internal/config"
)

// The dictation cleanup pass, ported from Python tmx (voice.tidy, voice.cleanup "local"): no
// model, nothing leaves the machine. The owner's names are spelled as in voice.vocabulary
// whatever case or spacing whisper heard ("terminal matrix", "terminal-matrix"),
// voice.replacements fix known mishearings ("tia mat" -> "Tiamat"), spaces are tidied and the
// first letter is a capital. French spacing before ? ! : ; is kept. Whole words only, and every
// replacement is literal text, never a pattern. It is applied to every dictation; recordings
// keep what was heard.

const (
	// MaxReplacements and MaxReplacementLen bound voice.replacements (config.json is a file
	// agents can write): at most this many entries, each side at most this many characters.
	MaxReplacements   = 200
	MaxReplacementLen = 200
)

// CoreVocabulary is always part of the vocabulary: the product's own names, which Whisper
// mishears without a hint. An install's own names (people, machines, companies) come from
// voice.vocabulary in config.json, never from the code.
var CoreVocabulary = []string{"Tiamatrix", "tmx"}

var (
	tmxMarkers   = regexp.MustCompile(`(?i)^\s*(?:\[tmx\]|\(tmx:)\s*`)
	spaceRuns    = regexp.MustCompile(`[ \t]+`)
	spaceBefore  = regexp.MustCompile(` +([,.])`)
	wordSplitter = regexp.MustCompile(`[\s\-]+`)
)

// unmark: a transcript never starts like tmx's own delivery or draft markers ("[tmx]", "(tmx:").
func unmark(text string) string {
	for {
		loc := tmxMarkers.FindStringIndex(text)
		if loc == nil {
			return text
		}
		text = text[loc[1]:]
	}
}

// tidyReplacements is voice.replacements as literal text -> text, bounded, and without a value
// that carries a tmx marker.
func tidyReplacements(c config.VoiceConfig) map[string]string {
	out := map[string]string{}
	keys := make([]string, 0, len(c.Replacements))
	for k := range c.Replacements {
		keys = append(keys, k)
	}
	sort.Strings(keys) // a map has no order: the first MaxReplacements by name, the same every time
	for _, k := range keys {
		if len(out) >= MaxReplacements {
			break
		}
		v := c.Replacements[k]
		low := strings.ToLower(v)
		if strings.TrimSpace(k) == "" || utf8.RuneCountInString(k) > MaxReplacementLen || utf8.RuneCountInString(v) > MaxReplacementLen ||
			strings.Contains(low, "[tmx]") || strings.Contains(low, "(tmx:") {
			continue
		}
		out[k] = v
	}
	return out
}

// tidyVocabulary is voice.vocabulary with the core names folded in.
func tidyVocabulary(c config.VoiceConfig) []string {
	have := map[string]bool{}
	var out []string
	for _, w := range c.Vocabulary {
		if w = strings.TrimSpace(w); w != "" {
			out = append(out, w)
			have[strings.ToLower(w)] = true
		}
	}
	for _, w := range CoreVocabulary {
		if !have[strings.ToLower(w)] {
			out = append(out, w)
		}
	}
	return out
}

func wordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// replaceWholeWords replaces every occurrence of heard (its words in any case, separated by any
// spaces or hyphens, or by none) with right, where it is not part of a longer word.
func replaceWholeWords(text, heard, right string) string {
	var parts []string
	for _, p := range wordSplitter.Split(strings.TrimSpace(heard), -1) {
		if p != "" {
			parts = append(parts, regexp.QuoteMeta(p))
		}
	}
	if len(parts) == 0 {
		return text
	}
	rx, err := regexp.Compile(`(?i)` + strings.Join(parts, `[\s\-]*`))
	if err != nil {
		return text
	}
	var b strings.Builder
	pos := 0
	for pos <= len(text) {
		loc := rx.FindStringIndex(text[pos:])
		if loc == nil {
			break
		}
		start, end := pos+loc[0], pos+loc[1]
		if end == start { // cannot happen with a non-empty part; never loop on it
			break
		}
		before, _ := utf8.DecodeLastRuneInString(text[:start])
		after, _ := utf8.DecodeRuneInString(text[end:])
		if (start > 0 && wordRune(before)) || (end < len(text) && wordRune(after)) {
			// Part of a longer word ("clara" in "claramente"): look again one character on.
			_, size := utf8.DecodeRuneInString(text[start:])
			b.WriteString(text[pos : start+size])
			pos = start + size
			continue
		}
		b.WriteString(text[pos:start])
		b.WriteString(right)
		pos = end
	}
	if pos < len(text) {
		b.WriteString(text[pos:])
	}
	return b.String()
}

// Tidy is the cleanup pass for one dictated text.
func Tidy(text string, c config.VoiceConfig) string {
	if c.Cleanup == "off" || text == "" {
		return unmark(strings.TrimSpace(text))
	}
	reps := tidyReplacements(c)
	for _, w := range tidyVocabulary(c) {
		if _, ok := reps[w]; !ok {
			reps[w] = w
		}
	}
	heard := make([]string, 0, len(reps))
	for k := range reps {
		heard = append(heard, k)
	}
	// Longest first, so "Terminal Matrix" is spelled before "tmx" could touch a part of it.
	sort.SliceStable(heard, func(i, j int) bool {
		if li, lj := utf8.RuneCountInString(heard[i]), utf8.RuneCountInString(heard[j]); li != lj {
			return li > lj
		}
		return heard[i] < heard[j]
	})
	out := text
	for _, h := range heard {
		out = replaceWholeWords(out, h, reps[h])
	}
	out = spaceRuns.ReplaceAllString(out, " ")
	out = strings.TrimSpace(spaceBefore.ReplaceAllString(out, "$1"))
	out = unmark(out)
	if r, size := utf8.DecodeRuneInString(out); size > 0 {
		out = string(unicode.ToUpper(r)) + out[size:]
	}
	return out
}
