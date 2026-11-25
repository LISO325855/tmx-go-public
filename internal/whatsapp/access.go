package whatsapp

import (
	"net/http"
	"slices"
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// Per-chat access for agents (C7). A profile in agent_profiles may use WhatsApp at all; WHICH
// chats one of its agents may read, and which it may ask to write to, is the owner's choice per
// chat.
//
// The head reads every chat, as before, EXCEPT the chats the owner marked private (settings
// private_chats): a private chat does not exist for the head, whatever other list names it; it
// may only ask to write there if the chat is on a send list of its own.
//
// Every other agent reads nothing and may write nowhere without an entry (settings
// chat_access), except the chats the owner already opened to agents another way:
//   - allowed_chats: agents write there without asking, so they see the answers.
// (notify_head_chats concerns the head only, which reads those chats anyway unless private.)
//
// A chat an agent may not read does not exist for it: it is not in its list, a search does not
// find it, and reading or writing it by name, number or JID answers "no such chat". Writing
// still waits for the owner's card per message unless the chat is in allowed_chats (or
// agents_send is "allowed"); a message to a person in People (person.go) is not governed here.
//
// Limit: this governs tmx's own WhatsApp tools and routes. The messages are also in the
// daemon's database in tmx's state folder, which the guard keeps guarded agents out of; a
// harness without the guard, or a shell, is not held back by this.

// AccessKeyAgent prefixes a chat_access key that names one agent ("agent:alpha-lead"); any
// other key is a profile slug ("head").
const AccessKeyAgent = "agent:"

const (
	maxAccessKeys  = 100
	maxAccessChats = 500
)

// accessKeys are the chat_access keys that apply to an agent: its profile and its own name.
func (s *Service) accessKeys(agentID string) []string {
	var keys []string
	if p, err := s.o.Fleet.AgentProfile(agentID); err == nil && p.Slug != "" {
		keys = append(keys, p.Slug)
	}
	if a, err := s.o.Fleet.Agent(agentID); err == nil && a.Name != "" {
		keys = append(keys, AccessKeyAgent+a.Name)
	}
	return keys
}

// isHead: the machine's head agent (the one the owner's notices go to).
func (s *Service) isHead(agentID string) bool {
	return agentID != "" && s.o.Fleet != nil && agentID == s.o.Fleet.HeadID()
}

// CanReadChat reports whether the agent may read the chat jid (CanRead must hold as well).
func (s *Service) CanReadChat(agentID, jid string) bool {
	if s.o.Fleet == nil || s.CanRead(agentID) != nil {
		return false
	}
	set := s.Settings()
	if s.isHead(agentID) {
		return !slices.Contains(set.PrivateChats, jid) // everything but what the owner keeps private
	}
	if slices.Contains(set.AllowedChats, jid) {
		return true
	}
	for _, k := range s.accessKeys(agentID) {
		if slices.Contains(set.ChatAccess[k].Read, jid) {
			return true
		}
	}
	return false
}

// CanSendChat reports whether the agent may ask to write to the chat jid: a chat it may read,
// or one in its send list (write-only: it may write there without seeing the chat).
func (s *Service) CanSendChat(agentID, jid string) bool {
	if s.CanReadChat(agentID, jid) {
		return true
	}
	if s.o.Fleet == nil || s.CanRead(agentID) != nil {
		return false
	}
	set := s.Settings()
	for _, k := range s.accessKeys(agentID) {
		if slices.Contains(set.ChatAccess[k].Send, jid) {
			return true
		}
	}
	return false
}

// AgentChats is Chats for an agent: only the chats it may read.
func (s *Service) AgentChats(agentID, q string, limit int) ([]model.WaChat, error) {
	if err := s.CanRead(agentID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q = strings.TrimSpace(q)
	all, err := s.listChats(q, 5000, q == "")
	if err != nil {
		return nil, err
	}
	out := []model.WaChat{}
	for _, c := range all {
		if s.CanReadChat(agentID, c.JID) {
			out = append(out, c)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

// AgentFindChat is FindChat for an agent: it looks only among the chats the agent may read
// (toSend: may write to). Anything else is "no such chat", whatever was named.
func (s *Service) AgentFindChat(agentID, ref string, toSend bool) (model.WaChat, error) {
	if err := s.CanRead(agentID); err != nil {
		return model.WaChat{}, err
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return model.WaChat{}, errorf(http.StatusBadRequest, "name the chat")
	}
	may := func(jid string) bool {
		if toSend {
			return s.CanSendChat(agentID, jid)
		}
		return s.CanReadChat(agentID, jid)
	}
	none := errorf(http.StatusNotFound, "no WhatsApp chat called %q among the chats the owner opened to you (whatsapp_chats lists them)", ref)
	if c, err := s.Chat(ref); err == nil {
		if may(c.JID) {
			return c, nil
		}
		return model.WaChat{}, none
	} else if !isNotFound(err) {
		return model.WaChat{}, err
	}
	all, err := s.listChats("", 5000, false)
	if err != nil {
		return model.WaChat{}, err
	}
	digits := strings.Map(keepDigits, ref)
	var exact, part []model.WaChat
	for _, c := range all {
		if !may(c.JID) {
			continue
		}
		switch {
		case strings.EqualFold(c.Name, ref) || (len(digits) >= 7 && strings.Map(keepDigits, phoneOf(c.JID)) == digits):
			exact = append(exact, c)
		case strings.Contains(strings.ToLower(c.Name), strings.ToLower(ref)):
			part = append(part, c)
		}
	}
	switch {
	case len(exact) == 1:
		return exact[0], nil
	case len(exact) == 0 && len(part) == 1:
		return part[0], nil
	case len(exact)+len(part) == 0:
		return model.WaChat{}, none
	}
	names := []string{}
	for _, c := range append(exact, part...) {
		names = append(names, c.Name+" ("+c.JID+")")
	}
	slices.Sort(names)
	if len(names) > 8 {
		names = append(names[:8], "…")
	}
	return model.WaChat{}, errorf(http.StatusConflict, "%q matches several chats: %s; use the JID", ref, strings.Join(names, ", "))
}

// AgentMessages is Messages for an agent: the chat by name, number or JID, if it may read it.
func (s *Service) AgentMessages(agentID, chat, before string, limit int) ([]model.WaMessage, error) {
	c, err := s.AgentFindChat(agentID, chat, false)
	if err != nil {
		return nil, err
	}
	return s.Messages(c.JID, before, limit)
}

// cleanChatAccess validates the owner's chat_access: keys are profile slugs or agent:<name>,
// values lists of chat JIDs; empty entries are dropped.
func cleanChatAccess(in map[string]model.WaChatAccess) (map[string]model.WaChatAccess, error) {
	out := map[string]model.WaChatAccess{}
	if len(in) > maxAccessKeys {
		return nil, errorf(http.StatusBadRequest, "chat_access: at most %d profiles or agents", maxAccessKeys)
	}
	for key, acc := range in {
		key = strings.TrimSpace(key)
		name := strings.TrimPrefix(key, AccessKeyAgent)
		if !model.ValidName(name) {
			return nil, errorf(http.StatusBadRequest, "chat_access: %q is not a profile slug or agent:<name>", key)
		}
		clean := model.WaChatAccess{Read: []string{}, Send: []string{}}
		for _, l := range []struct {
			in  []string
			out *[]string
		}{{acc.Read, &clean.Read}, {acc.Send, &clean.Send}} {
			for _, jid := range l.in {
				jid = strings.TrimSpace(jid)
				if jid == "" || slices.Contains(*l.out, jid) {
					continue
				}
				if !strings.Contains(jid, "@") {
					return nil, errorf(http.StatusBadRequest, "chat_access: %q is not a chat (a WhatsApp JID like 41791234567@s.whatsapp.net)", jid)
				}
				*l.out = append(*l.out, jid)
			}
			if len(*l.out) > maxAccessChats {
				return nil, errorf(http.StatusBadRequest, "chat_access: at most %d chats per list", maxAccessChats)
			}
		}
		if len(clean.Read)+len(clean.Send) > 0 {
			out[key] = clean
		}
	}
	return out, nil
}
