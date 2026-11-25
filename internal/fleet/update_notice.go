package fleet

import (
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/rollback"
	"github.com/blackcode-ch/tmx-go/internal/store"
)

// UpdateNotice queues one durable notice per confirmed install, including after
// a restart between health confirmation and delivery. It is system mail, not owner mail.
func (f *Fleet) UpdateNotice(r rollback.ProbationResult) error {
	if r.Outcome != "confirmed" {
		return nil
	}
	id := fmt.Sprintf("update-%x", sha256.Sum256([]byte(r.From+"\n"+r.To+"\n"+r.At.String())))
	f.mu.Lock()
	if _, err := f.st.GetMessage(id); err == nil {
		f.mu.Unlock()
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		f.mu.Unlock()
		return err
	}
	h := f.headLocked()
	if h == nil || f.closed {
		f.mu.Unlock()
		return fmt.Errorf("update notice: head unavailable")
	}
	m := model.Message{ID: id, From: model.WatchParty, To: h.a.ID, Body: fmt.Sprintf("updated from %s to %s", r.From, r.To), State: model.MsgQueued, CreatedAt: model.Now()}
	if err := f.st.PutMessage(m); err != nil {
		f.mu.Unlock()
		return err
	}
	f.bus.Publish(model.BusEvent{Type: model.EvMessageCreated, AgentID: h.a.ID, Data: m})
	f.refreshQueuedLocked(h)
	f.mu.Unlock()
	f.tryDeliver(m.To)
	return nil
}
