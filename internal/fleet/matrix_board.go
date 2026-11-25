package fleet

import "github.com/blackcode-ch/tmx-go/internal/model"

// boardReportRule tells an agent to keep the owner's Board current (the Board view of the
// Matrix, the owner 17 Nov 2026): one row per thread with how far it is and what it waits for.
// Without it the rows say "Not reported": the matrix tool's description alone moves nobody.
const boardReportRule = "The owner reads a Board with one row per thread: how far it is and what it waits for. " +
	"On a thread you own or lead, at each real step call mcp__tmxg__matrix op=state with pct (0-100, your honest estimate) " +
	"and why (one line: where it stands now). When it waits for something, add blocker (one line): what stops it, with status blocked; " +
	"or exactly what the owner has to do, with status waiting_on_owner. Send blocker \"\" when that is over."

// boardOrderRule tells leads and the head that the owner's order on the Board is the priority
// (the owner, 17 Nov 2026 20:58: he drags the rows; agents work top-first).
const boardOrderRule = "Check the Board's order at the start of each job; work top-first: " +
	"mcp__tmxg__matrix op=mine lists your threads in the owner's priority order (priority 1 is the top)."

// reportsBoardLocked says whether an agent is told to report to the Board: a project's lead,
// and any agent that owns a live thread (they are the ones SetState lets report).
func (f *Fleet) reportsBoardLocked(a model.Agent) bool {
	if a.Level == model.LevelLead {
		return true
	}
	threads, err := f.st.ListThreads()
	if err != nil {
		return false
	}
	for _, t := range threads {
		if t.Owner == a.ID && !t.Archived && t.Kind != model.ThreadChat && t.Status != model.ThreadDone {
			return true
		}
	}
	return false
}
