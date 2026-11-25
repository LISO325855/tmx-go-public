package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/whatsapp"
)

// POST /api/team/{key}/message: an agent proposes a message to a person in People.
//
// The owner decides, always. Where the person has a WhatsApp number in People and that number
// is verified on this machine's WhatsApp (a one-to-one chat they wrote in), the message becomes
// an owner card: Allow sends exactly this text to that chat, deny drops it, and the agent hears
// the outcome by mail. In every other case (no number, not verified, WhatsApp off or not
// linked) nothing can be sent from here: the draft is kept as an open promise "to carry by
// hand" on the Promises page, and the answer says so. The number never leaves the daemon.

// PersonMessageRequest is the body.
type PersonMessageRequest struct {
	Text  string `json:"text"`
	Title string `json:"title,omitempty"` // one line for the Promises page (by hand only)
}

// PersonMessageResult answers it: via "whatsapp" with the owner card, or via "by_hand" with the
// promise that keeps the draft and why it cannot be sent from here.
type PersonMessageResult struct {
	Via      string          `json:"via"`
	Person   string          `json:"person"`
	Approval *model.Approval `json:"approval,omitempty"`
	// Sent: true when it went out at once (the owner lets this agent send without asking).
	Sent    bool           `json:"sent,omitempty"`
	Promise *model.Promise `json:"promise,omitempty"`
	Reason  string         `json:"reason,omitempty"`
}

func (s *Server) personMessage(w http.ResponseWriter, r *http.Request) {
	c, _ := caller(r)
	if c.AgentID == "" || c != fleet.AgentCaller(c.AgentID) {
		writeError(w, http.StatusForbidden, "only agents propose messages to people")
		return
	}
	var in PersonMessageRequest
	if !decode(w, r, &in) {
		return
	}
	text, err := whatsapp.CheckPersonText(in.Text)
	if err != nil {
		writeWaErr(w, err)
		return
	}
	key := strings.ToLower(strings.TrimSpace(r.PathValue("key")))
	target, err := s.f.PersonInviteTarget(key)
	reason := ""
	switch {
	case err == nil && (s.wa == nil || !s.wa.Enabled()):
		reason = "WhatsApp is off on this machine"
	case err == nil:
		ap, sent, err := s.wa.PersonMessage(c.AgentID, target.Number, target.Name, text)
		if err == nil && sent != nil {
			writeJSON(w, http.StatusCreated, PersonMessageResult{Via: "whatsapp", Person: target.Key, Sent: true})
			return
		}
		if err == nil {
			ap = agentApproval(ap) // without Input: the chat's JID is the person's number
			writeJSON(w, http.StatusAccepted, PersonMessageResult{Via: "whatsapp", Person: target.Key, Approval: &ap})
			return
		}
		why, byHand := personByHand(err)
		if !byHand {
			writeWaErr(w, err)
			return
		}
		reason = why
	case fleet.KindOf(err) == fleet.KindConflict: // in People, without a WhatsApp number
		reason = "this person has no WhatsApp number in People"
	default:
		writeErr(w, err) // no such person
		return
	}
	if target.Key != "" {
		key = target.Key
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = "Message to carry by hand"
	}
	body := fmt.Sprintf("To carry by hand: %s. Draft (not sent; the owner decides):\n%s", reason, text)
	p, err := s.f.PutPromise(c, "", fleet.PromiseInput{PersonKey: key, Title: &title, Text: &body})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, PersonMessageResult{Via: "by_hand", Person: key, Promise: &p, Reason: reason})
}

// personByHand: the WhatsApp refusals that mean "this cannot go out from here at all" (the
// number is not verified, WhatsApp is off or not linked), with the reason to
// keep. Anything else (too many waiting, a bad text) is an error for the agent.
func personByHand(err error) (string, bool) {
	switch err {
	case whatsapp.ErrOff:
		return "WhatsApp is off on this machine", true
	case whatsapp.ErrNotLinked:
		return "WhatsApp is not linked on this machine", true
	}
	if we, ok := err.(*whatsapp.Error); ok && we.Status == http.StatusConflict {
		return we.Msg, true
	}
	return "", false
}
