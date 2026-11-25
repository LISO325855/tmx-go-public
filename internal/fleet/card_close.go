package fleet

import (
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// Closing owner cards without an answer: the owner (or the head) retires questions that no
// longer need one, e.g. the stale cards an import brought along. A closed card leaves the
// owner's list, ends as expired with the reason on it, records no decision and sends no mail:
// the asker is not told anything, because nothing was decided. Words the owner still types on
// such a card later are delivered as correspondence (decideOwnerCardLocked).

// CloseCardsRequest names the cards: IDs, or Imported for every waiting card an import brought
// (Card.Origin set), parked ones included. Both may be given.
type CloseCardsRequest struct {
	IDs      []string `json:"ids,omitempty"`
	Imported bool     `json:"imported,omitempty"`
	Reason   string   `json:"reason"`
}

// CloseCardsResult lists what was closed and, per ID that was not, why.
type CloseCardsResult struct {
	Closed  []model.Approval  `json:"closed"`
	Skipped map[string]string `json:"skipped,omitempty"`
}

// CloseOwnerCards closes the named owner cards (owner or head). Only questions with a card are
// touched: never a permission, a guard hold, a brake or a message waiting to be sent. The head
// closes other agents' questions, not its own.
func (f *Fleet) CloseOwnerCards(c Caller, req CloseCardsRequest) (CloseCardsResult, error) {
	out := CloseCardsResult{Closed: []model.Approval{}}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" || len(reason) > MaxBodyBytes {
		return out, errf(KindInvalid, "give a reason (it stays on the closed cards)")
	}
	if len(req.IDs) == 0 && !req.Imported {
		return out, errf(KindInvalid, "name the cards: ids, or imported")
	}
	var live []string
	f.mu.Lock()
	out, err := f.closeOwnerCardsLocked(c, req, reason, &live)
	f.mu.Unlock()
	if err != nil {
		return out, err
	}
	for _, id := range live {
		a, err := f.Decide(c, id, DecideRequest{Decision: "deny", Message: reason})
		if err != nil {
			out.skip(id, err.Error())
			continue
		}
		out.Closed = append(out.Closed, a)
	}
	return out, nil
}

func (f *Fleet) closeOwnerCardsLocked(c Caller, req CloseCardsRequest, reason string, live *[]string) (CloseCardsResult, error) {
	out := CloseCardsResult{Closed: []model.Approval{}}
	headID := "" // set when the head closes: its own questions stay the owner's to close
	if !c.Owner {
		me, err := f.callerRecLocked(c)
		if err != nil || c.AgentID == "" || c != AgentCaller(c.AgentID) || !isHead(me) {
			return out, errf(KindForbidden, "only the owner or the head closes owner cards")
		}
		headID = me.a.ID
	}
	targets := map[string]bool{}
	for _, id := range req.IDs {
		if id = strings.TrimSpace(id); id != "" {
			targets[id] = true
		}
	}
	pending, err := f.st.ListApprovals(model.ApprovalPending)
	if err != nil {
		return out, err
	}
	seen := map[string]bool{}
	for _, a := range pending {
		named := targets[a.ID]
		imported := req.Imported && a.Card != nil && a.Card.Origin != nil
		if !named && !imported {
			continue
		}
		seen[a.ID] = true
		if a.Kind != model.ApprovalQuestion {
			if named {
				out.skip(a.ID, "not an owner card (a permission, hold or message is answered, not closed)")
			}
			continue
		}
		if headID != "" && a.AgentID == headID {
			// The head does not retire what it asked the owner itself (it has withdraw_card for
			// that, which records that it was the asker who took it back).
			out.skip(a.ID, "the head's own question: the owner closes it, or the head withdraws it")
			continue
		}
		if a.Card == nil {
			if _, active := f.perms[a.ID]; active {
				if c.Owner {
					*live = append(*live, a.ID)
				} else {
					out.skip(a.ID, "only the owner can decline an active harness question")
				}
				continue
			}
			f.finishApprovalLocked(&a, model.ApprovalExpired, reason)
			out.Closed = append(out.Closed, a)
			continue
		}
		card := *a.Card
		card.CloseReason = reason
		a.Card = &card
		a.State, a.Deferred, a.DecidedAt = model.ApprovalExpired, false, model.Now()
		a.Version++
		if err := f.st.PutApproval(a); err != nil {
			return out, err
		}
		f.Publish(model.BusEvent{Type: model.EvApprovalResolved, AgentID: a.AgentID, ProjectID: card.ProjectID, Data: a})
		out.Closed = append(out.Closed, a)
	}
	for id := range targets {
		if !seen[id] {
			out.skip(id, "not waiting (unknown, answered or closed already)")
		}
	}
	if len(out.Closed) > 0 {
		f.log.Info("owner cards closed without an answer", "count", len(out.Closed), "imported", req.Imported, "by", f.nameOfLocked(c.AgentID))
	}
	return out, nil
}

func (r *CloseCardsResult) skip(id, why string) {
	if r.Skipped == nil {
		r.Skipped = map[string]string{}
	}
	r.Skipped[id] = why
}
