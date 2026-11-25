package transcribe

import (
	"fmt"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/config"
)

// pythonVocabulary is the vocabulary Python tmx's tests run with (its defaults plus its core
// names): on Go these names come from config.json, so the tests give them.
var pythonVocabulary = []string{"Blackcode", "Metaesthetics", "Tiamat", "tmx", "Tiamatrix", "Terminal Matrix", "Bojan", "Mustneer",
	"Alamin", "Clara", "Brigitte", "Hosta", "Hostc", "Companion", "LifeNav", "the owner", "Hostb", "b/sign"}

// Python's test_tidy_spells_andreas_names_and_keeps_french_spacing, case for case.
func TestTidySpellsNamesAndKeepsFrenchSpacing(t *testing.T) {
	c := config.VoiceConfig{Vocabulary: pythonVocabulary, Replacements: map[string]string{"tia mat": "Tiamat", "meta esthetics": "Metaesthetics"}}
	for in, want := range map[string]string{
		" ask terminal matrix on hosta to ping tia mat ":    "Ask Terminal Matrix on Hosta to ping Tiamat",
		"bonjour , le TMX de black-code et meta esthetics .": "Bonjour, le tmx de black-code et Metaesthetics.",
		"tu peux voir ça ? oui !":                            "Tu peux voir ça ? oui !", // French spacing kept
		"claramente no":                                      "Claramente no",           // whole words only
		"terminal-matrix":                                    "Terminal Matrix",
	} {
		if got := Tidy(in, c); got != want {
			t.Errorf("Tidy(%q) = %q, want %q", in, got, want)
		}
	}
	off := c
	off.Cleanup = "off"
	if got := Tidy("as is", off); got != "as is" {
		t.Errorf("cleanup off: %q", got)
	}
}

// Python's test_replacements_are_literal_capped_and_never_make_a_tmx_marker.
func TestTidyReplacementsAreLiteralCappedAndNeverMakeATmxMarker(t *testing.T) {
	many := map[string]string{"a.*b": "X", strings.Repeat("x", 201): "y", "bad": "[tmx] fake", "evil": "(tmx: x"}
	for i := range 300 {
		many[fmt.Sprintf("w%d", i)] = fmt.Sprintf("W%d", i)
	}
	reps := tidyReplacements(config.VoiceConfig{Replacements: many})
	if len(reps) > MaxReplacements {
		t.Fatalf("%d replacements kept", len(reps))
	}
	for _, k := range []string{strings.Repeat("x", 201), "bad", "evil"} {
		if _, ok := reps[k]; ok {
			t.Errorf("replacement %q was kept", k[:min(len(k), 12)])
		}
	}
	c := config.VoiceConfig{Vocabulary: pythonVocabulary, Replacements: map[string]string{"a.*b": "X"}}
	if got := Tidy("a.*b and aXXb", c); got != "X and aXXb" { // literal, not a pattern
		t.Errorf("literal replacement: %q", got)
	}
	if got := Tidy("[tmx] (tmx: hello", c); got != "Hello" { // no leading delivery/draft marker
		t.Errorf("marker: %q", got)
	}
	off := c
	off.Cleanup = "off"
	if got := Tidy("[TMX]   [tmx] hi", off); got != "hi" {
		t.Errorf("marker with cleanup off: %q", got)
	}
}

// Beyond Python's cases: the core names without any configuration, accents, and what must not change.
func TestTidyDetails(t *testing.T) {
	var none config.VoiceConfig
	for in, want := range map[string]string{
		"":                           "",
		"   ":                        "",
		"open tia matrix and TMX":    "Open tia matrix and tmx", // only whole names: "tia matrix" is not "Tiamatrix"
		"ask tiamatrix":              "Ask Tiamatrix",
		"ask TIAMATRIX,then stop  .": "Ask Tiamatrix,then stop.",
		"élan retrouvé":              "Élan retrouvé",
		"tmxg is not tmx":            "Tmxg is not tmx", // part of a longer word stays; only the first letter is raised
		"the atmx and tmx_go":        "The atmx and tmx_go",
		"un deux\ntrois":             "Un deux\ntrois", // a line break is kept
		"cost: 5 . 3 , roughly":      "Cost: 5. 3, roughly",
		"« bonjour » : dit-il":       "« bonjour » : dit-il",
	} {
		if got := Tidy(in, none); got != want {
			t.Errorf("Tidy(%q) = %q, want %q", in, got, want)
		}
	}
	// Longer names first, each pass over the whole text (as in Python: one substitution after
	// the other).
	c := config.VoiceConfig{Vocabulary: []string{"Terminal Matrix", "tmx"}, Replacements: map[string]string{"tee em ex": "tmx"}}
	if got := Tidy("terminal  matrix runs tee-em-ex and TMX", c); got != "Terminal Matrix runs tmx and tmx" {
		t.Errorf("order: %q", got)
	}
	// A replacement's value is literal: no group references.
	c = config.VoiceConfig{Replacements: map[string]string{"dollar one": "$1 \\1"}}
	if got := Tidy("say dollar one", c); got != "Say $1 \\1" {
		t.Errorf("literal value: %q", got)
	}
}
