package term

import (
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

func TestEmptyComposerEvidence(t *testing.T) {
	for _, tc := range []struct {
		h     model.Harness
		text  string
		empty bool
	}{
		{model.HarnessCodex, "›\n  95% context left", true},
		{model.HarnessCodex, "› draft\n  95% context left", false},
		{model.HarnessCodex, "›\n  continued draft\n  95% context left", false},
		{model.HarnessCodex, "›\n  unrecognized footer", false},
		{model.HarnessClaude, "? for shortcuts", false},
		{model.HarnessPi, "────\n\n────\nctrl+c twice to exit", true},
		{model.HarnessPi, "────\ndraft\n────\nctrl+c twice to exit", false},
		{model.HarnessOpenCode, "┃\n┃  Build  model\ntab agents", true},
		{model.HarnessOpenCode, "┃ draft\n┃  Build  model\ntab agents", false},
		{model.HarnessOpenCode, "┃\n┃  Build · local model\n╹▀▀▀▀\ntab agents ctrl+p commands\n\n\nproject 1.19.6", true},
		{model.HarnessOpenCode, "┃\n┃  Ask anything… typed draft\n┃  Build · local model\n╹▀▀▀▀\ntab agents ctrl+p commands\n\n\nproject 1.19.6", false},
	} {
		if got := emptyComposer(tc.text, tc.h); got != tc.empty {
			t.Errorf("%s %q: %v", tc.h, tc.text, got)
		}
	}
}
