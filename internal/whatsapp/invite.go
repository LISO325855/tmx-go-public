package whatsapp

import (
	"net/http"
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// VerifiedDM returns the direct chat with a number or JID when that
// number is verified on this account: a one-to-one chat with exactly that number exists AND the
// person has written in it at least once. A number someone only typed into People, a group, or
// a chat only we wrote into is not verified. The refusal never repeats the number.
func (s *Service) VerifiedDM(number string) (model.WaChat, error) {
	if err := s.ready(false); err != nil {
		return model.WaChat{}, err
	}
	jid := strings.TrimSpace(number)
	if !strings.Contains(jid, "@") {
		number = strings.Map(keepDigits, number)
		if len(number) < 7 {
			return model.WaChat{}, errorf(http.StatusConflict, "the person's WhatsApp number in People is not a full number")
		}
		jid = number + "@s.whatsapp.net"
	}
	c, err := s.o.Store.WaGetChat(jid)
	if err != nil || c.Kind != model.WaDM {
		return model.WaChat{}, errorf(http.StatusConflict, "this person's WhatsApp number is not verified here: "+
			"this account has no one-to-one chat with it yet (the person writes to the owner once, or the owner sends the invite by hand)")
	}
	in, err := s.o.Store.WaHasInbound(jid)
	if err != nil {
		return model.WaChat{}, err
	}
	if !in {
		return model.WaChat{}, errorf(http.StatusConflict, "this person's WhatsApp number is not verified here: "+
			"they have never written from it to this account")
	}
	return s.chatView(c), nil
}

// SendInvite sends the daemon-written invite message to an existing direct chat. origin is the agent
// that asked (the message shows it, like every agent-origin message). It is not AgentSend: no
// allowed-chats rule and no second approval apply, because the invite action already carried
// the owner's decision for exactly this person.
func (s *Service) SendInvite(origin, number, text string) (model.WaMessage, error) {
	c, err := s.ExistingDM(number)
	if err != nil {
		return model.WaMessage{}, err
	}
	msgs, err := s.Send(origin, c.JID, SendRequest{Text: text})
	if err != nil {
		return model.WaMessage{}, err
	}
	return msgs[0], nil
}

// ExistingDM resolves a direct chat already present on this node.
func (s *Service) ExistingDM(destination string) (model.WaChat, error) {
	if err := s.ready(false); err != nil {
		return model.WaChat{}, err
	}
	jid := strings.TrimSpace(destination)
	if !strings.Contains(jid, "@") {
		jid = strings.Map(keepDigits, jid) + "@s.whatsapp.net"
	}
	c, err := s.o.Store.WaGetChat(jid)
	if err != nil || c.Kind != model.WaDM {
		return model.WaChat{}, errorf(http.StatusConflict, "no existing WhatsApp direct chat for this destination")
	}
	return s.chatView(c), nil
}
