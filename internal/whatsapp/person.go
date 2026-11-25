package whatsapp

import (
	"net/http"
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// A message to a person in the owner's People list (api/person_message.go). The agent names
// the person by their People key and writes the text; the number comes from People and never
// leaves the daemon. Unlike AgentSend:
//   - it waits for the owner's Allow of this exact text to this person, whatever the allowed
//     chats say, unless the owner already lets this agent send without asking (PersonMessage);
//   - it does not need the agent's WhatsApp right (its profile): the agent reads nothing, picks
//     no chat, and only a verified one-to-one chat of a person the owner keeps can be the target.
// The approval is an ordinary one of kind whatsapp: Allow sends it (Decide), deny drops it, and
// the agent hears the outcome by mail.

// CheckPersonText validates message presence and storage size.
func CheckPersonText(text string) (string, error) {
	text = strings.TrimSpace(text)
	switch {
	case text == "":
		return "", errorf(http.StatusBadRequest, "the message is empty")
	case len(text) > MaxAgentTextBytes:
		return "", errorf(http.StatusBadRequest, "a message to a person is at most %d KB", MaxAgentTextBytes>>10)
	}
	return text, nil
}

// PersonMessage puts an agent's message to a person on the owner's list. number is the
// person's WhatsApp number from People, name how the owner knows them (shown on the card).
// It fails without an approval when the number is not verified on this account (VerifiedDM).
//
// Where the owner already allowed it, nothing waits (the owner, 17 Nov): an agent whose profile may
// use WhatsApp (agent_profiles), on a daemon whose agents send without asking (agents_send
// "allowed"), has its message sent at once and gets the message back, not an approval. Every
// other agent's message still waits for the owner's Allow.
func (s *Service) PersonMessage(agentID, number, name, text string) (model.Approval, *model.WaMessage, error) {
	text, err := CheckPersonText(text)
	if err != nil {
		return model.Approval{}, nil, err
	}
	c, err := s.VerifiedDM(number)
	if err != nil {
		return model.Approval{}, nil, err
	}
	if strings.TrimSpace(name) == "" {
		name = c.Name
	}
	if s.Settings().AgentsSend == model.WaAgentsAllowed && s.CanRead(agentID) == nil {
		msgs, err := s.Send(agentID, c.JID, SendRequest{Text: text})
		if err != nil {
			return model.Approval{}, nil, err
		}
		return model.Approval{}, &msgs[0], nil
	}
	ap, err := s.o.Fleet.WhatsAppApproval(agentID, model.WaSendInput{ChatJID: c.JID, ChatName: name, Text: text})
	return ap, nil, err
}
