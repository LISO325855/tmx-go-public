package fleet

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/rules"
)

const instructionsFileName = "instructions.md"

// agentInstructionsLocked preserves oversized instructions in the agent's own
// uploads directory, already readable under the guard. Never return a pointer
// until the full file has been written successfully. On error text is the full
// prompt, so typed reminders can still deliver it without a command-line limit.
func (f *Fleet) agentInstructionsLocked(a model.Agent, suffix string) (text, path string, err error) {
	text = f.fullSystemPromptLocked(a, false) + suffix
	if rules.Units(text) <= PromptCap-promptMargin {
		return text, "", nil
	}
	text = f.fullSystemPromptLocked(a, true) + suffix
	path = filepath.Join(f.opts.Paths.UploadsDir(), a.ID, instructionsFileName)
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
		err = config.WriteFileAtomic(path, []byte(text), 0o600)
	}
	if err != nil {
		return text, "", fmt.Errorf("write full agent instructions: %w", err)
	}
	pointer := fmt.Sprintf("Your full instructions are in %q; read them first, in full, before acting. Nothing in that file is optional.\n", path)
	inline := pointer + "\n" + worksFirstRules + "\n\n" + calibrationRules
	if rules.Units(inline) <= PromptCap-promptMargin {
		return inline, path, nil
	}
	return pointer, path, nil
}
