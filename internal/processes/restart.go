package processes

import (
	"errors"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// Restart stops a registered agent tool and starts it again with the command, arguments,
// folder, owner and project it had (a promoted tool stays promoted). The restarted tool is a
// new entry with a new ID; the old entry is removed. System tools restart themselves and are
// refused, as is a tool whose host could not be checked.
func (r *Registry) Restart(id string) (model.ProcessInfo, error) {
	r.mu.Lock()
	e := r.entries[id]
	if e == nil || e.spec.System || e.unknown || r.closed {
		r.mu.Unlock()
		return model.ProcessInfo{}, errors.New("no agent tool to restart")
	}
	spec, old := e.spec, e.info
	spec.Args = append([]string(nil), spec.Args...)
	r.mu.Unlock()

	r.Stop(id)
	// The owner and project it has now (Promote may have changed them since it started).
	spec.OwnerID, spec.ProjectID = old.OwnerID, old.ProjectID
	p, err := r.Start(spec)
	if err != nil {
		return model.ProcessInfo{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, id)
	if n := r.entries[p.ID]; n != nil {
		n.info.CreatorID, n.info.Promoted, n.info.Restarts = old.CreatorID, old.Promoted, old.Restarts+1
		if n.conn != nil {
			r.saveMeta(n) // hosted.go: the next daemon adopts it as it is now
		}
		p = n.info
	}
	return p, nil
}
