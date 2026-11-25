package fleet

import (
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// An agent's own owner cards: the questions it put on the owner's list with ask_owner (or as
// project cards), and the messages to people or WhatsApp chats it asked the owner to send. It
// may see which of them still wait, and take one back while the owner has not answered it.
// Taking a card back is never an answer: nothing runs, nothing is sent, nothing is allowed,
// and no mail goes out; the owner's list simply loses the card, with the reason kept on it.
//
// Guard holds are not among them (withdraw_hold), nor a harness's own permission requests.

// ownCard: an approval the agent itself put on the owner's list and may take back.
func ownCard(a model.Approval, agentID string) bool {
	if a.AgentID != agentID {
		return false
	}
	return (a.Kind == model.ApprovalQuestion && a.Card != nil) || a.Kind == model.ApprovalWhatsApp
}

// ownCardCallerLocked: only an agent itself (never the owner, Companion or a peer machine).
func (f *Fleet) ownCardCallerLocked(c Caller) (*agentRec, error) {
	if c.AgentID == "" || c != AgentCaller(c.AgentID) {
		return nil, errf(KindForbidden, "only the agent that asked sees or withdraws its owner cards")
	}
	r, err := f.callerRecLocked(c)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, errf(KindForbidden, "only the agent that asked sees or withdraws its owner cards")
	}
	return r, nil
}

// OwnCards lists the caller's own owner cards that still wait for the owner, parked ones
// included, oldest first.
func (f *Fleet) OwnCards(c Caller) ([]model.Approval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, err := f.ownCardCallerLocked(c)
	if err != nil {
		return nil, err
	}
	pending, err := f.st.ListApprovals(model.ApprovalPending)
	if err != nil {
		return nil, err
	}
	out := []model.Approval{}
	for _, a := range pending {
		if ownCard(a, r.a.ID) {
			out = append(out, a)
		}
	}
	return out, nil
}

// WithdrawOwnCard takes back one of the caller's own owner cards that the owner has not
// answered. A question ends as expired with the reason on it (Card.CloseReason), like a closed
// project card; words the owner still types on it later are delivered as correspondence
// (decideOwnerCardLocked). A message waiting for the owner's Allow ends as expired with the
// reason as its answer, and is not sent. Repeating a withdrawal is not an error.
//
// A message the owner allows in the same instant may still go out: the decision reads the card
// before it sends (decideWhatsApp), and a withdrawal between the two is too late.
func (f *Fleet) WithdrawOwnCard(c Caller, id, reason string) (model.Approval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, err := f.ownCardCallerLocked(c)
	if err != nil {
		return model.Approval{}, err
	}
	a, err := f.st.GetApproval(id)
	if err != nil || !ownCard(a, r.a.ID) {
		// One answer for "no such card" and "not yours": an agent learns nothing about
		// other agents' cards.
		return model.Approval{}, errf(KindNotFound, "no owner card %q of yours", id)
	}
	reason = strings.TrimSpace(reason)
	if len(reason) > MaxBodyBytes {
		return model.Approval{}, errf(KindInvalid, "the reason is longer than %d KB", MaxBodyBytes>>10)
	}
	if reason == "" {
		reason = "Withdrawn by " + r.a.Name
	}
	withdrawn := a.State == model.ApprovalExpired && ((a.Card != nil && a.Card.CloseReason != "") || (a.Card == nil && a.Answer != ""))
	if withdrawn {
		return a, nil
	}
	if a.State != model.ApprovalPending {
		return model.Approval{}, errf(KindConflict, "this card was already resolved (%s)", a.State)
	}
	if a.Card == nil { // a message waiting for the owner's Allow
		a.Version++
		f.finishApprovalLocked(&a, model.ApprovalExpired, "Withdrawn by "+r.a.Name+", not sent: "+reason)
		return a, nil
	}
	card := *a.Card
	card.CloseReason = reason
	a.Card = &card
	a.State, a.Deferred, a.DecidedAt = model.ApprovalExpired, false, model.Now()
	a.Version++
	if err := f.st.PutApproval(a); err != nil {
		return model.Approval{}, err
	}
	project := card.ProjectID
	if project == "" {
		project = r.a.ProjectID
	}
	f.Publish(model.BusEvent{Type: model.EvApprovalResolved, AgentID: a.AgentID, ProjectID: project, Data: a})
	return a, nil
}
