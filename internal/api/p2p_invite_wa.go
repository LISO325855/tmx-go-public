package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/chat"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/p2p"
	"github.com/blackcode-ch/tmx-go/internal/peertext"
)

// Join invites can be sent to a person in People or an existing WhatsApp direct chat.

// InviteWATTL is how long an invite sent by WhatsApp stays valid. Not a parameter.
const InviteWATTL = 48 * time.Hour

// AgentP2PInviteWARequest is POST /api/p2p/agent/invite-whatsapp.
type AgentP2PInviteWARequest struct {
	Person     string `json:"person,omitempty" jsonschema:"the person's key in People or an existing WhatsApp direct-chat JID"`
	ApprovalID string `json:"approval_id,omitempty" jsonschema:"fetch the returned approval ID after the owner answers; omit person"`
}

// InviteSent is the receipt of an invite sent by WhatsApp: everything but the link.
type InviteSent struct {
	Person    string `json:"person"`     // the People key
	InviteID  string `json:"invite_id"`  // revoke with it
	ExpiresAt int64  `json:"expires_at"` // unix ms
	Hash      string `json:"hash"`       // first 8 hex digits of the link's SHA-256
	MessageID string `json:"message_id"` // the WhatsApp message (pending until WhatsApp confirms)
}

// InviteHash is the short hash logs and receipts carry instead of an invite link.
func InviteHash(link string) string {
	sum := sha256.Sum256([]byte(link))
	return hex.EncodeToString(sum[:4])
}

var joinLinkRx = regexp.MustCompile(`tmx://join/[A-Za-z0-9_\-=]+`)

// hideJoinLinks masks invite links in WhatsApp text an agent reads back (an invite the daemon
// sent stays in the owner's chat history; the agent's transcript must not get it from there).
func hideJoinLinks(s string) string {
	if !strings.Contains(s, p2p.LinkPrefix) {
		return s
	}
	return joinLinkRx.ReplaceAllString(s, p2p.LinkPrefix+"[hidden]")
}

func hideJoinLinksIn(m *model.WaMessage) {
	if m == nil {
		return
	}
	m.Text, m.Caption = hideJoinLinks(m.Text), hideJoinLinks(m.Caption)
	if m.Quoted != nil {
		q := *m.Quoted
		q.Text = hideJoinLinks(q.Text)
		m.Quoted = &q
	}
}

// inviteWAText is the whole message; nothing in it comes from an agent. lang is the person's
// language in People (it, de, fr; anything else reads English).
func inviteWAText(lang, person, from, link string, expires time.Time) string {
	first, _, _ := strings.Cut(strings.TrimSpace(person), " ")
	day := expires.Format("02.01.2006 15:04")
	var body string
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "it":
		body = "Ciao %s, questo è l'invito personale per collegarti a Tiamatrix di %s. Aprilo in tmx sul tuo computer (Join). Vale una volta sola, fino al %s."
	case "de":
		body = "Hallo %s, das ist deine persönliche Einladung, dich mit dem Tiamatrix von %s zu verbinden. Öffne sie in tmx auf deinem Computer (Join). Sie gilt einmal, bis %s."
	case "fr":
		body = "Bonjour %s, voici ton invitation personnelle pour te connecter au Tiamatrix de %s. Ouvre-la dans tmx sur ton ordinateur (Join). Elle vaut une seule fois, jusqu'au %s."
	default:
		body = "Hi %s, this is your personal invite to connect to %s's Tiamatrix. Open it in tmx on your computer (Join). It works once, until %s."
	}
	return fmt.Sprintf(body, first, from, day) + "\n\n" + link
}

func (s *Server) agentP2PInviteWA(w http.ResponseWriter, r *http.Request) {
	c, ok := s.p2pAgent(w, r)
	if !ok {
		return
	}
	// Accept a People key or chat JID; reject unrelated fields.
	var in AgentP2PInviteWARequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "send only person (a People key or WhatsApp JID) or approval_id")
		return
	}
	if in.ApprovalID != "" {
		if in.Person != "" {
			writeError(w, 400, "resume with approval_id only")
			return
		}
		s.resumeP2P(w, r, c, in.ApprovalID, fleet.ToolP2PInviteWA)
		return
	}
	if s.wa == nil || !s.wa.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "WhatsApp is off on this machine; the invite cannot be sent from here")
		return
	}
	target, err := s.inviteWATarget(in.Person)
	if err != nil {
		if strings.Contains(in.Person, "@") {
			writeWaErr(w, err)
		} else {
			writeErr(w, err)
		}
		return
	}
	// Refuse before the owner is asked: a card that could never run is noise on their phone.
	if _, err := s.wa.ExistingDM(target.Number); err != nil {
		writeWaErr(w, err)
		return
	}
	preview := fmt.Sprintf("Send %s a join invite by WhatsApp (single use, valid %g h)", peertext.Field(target.Name), InviteWATTL.Hours())
	s.startP2P(w, r, &p2pAction{caller: c, tool: fleet.ToolP2PInviteWA, target: target, result: AgentP2PResult{Preview: preview}})
}

// executeInviteWA runs an allowed invite-by-WhatsApp action (a.mu held). It runs only if the
// destination is still the one approved and its direct chat still exists.
func (s *Server) executeInviteWA(a *p2pAction) error {
	if s.wa == nil || !s.wa.Enabled() {
		return fmt.Errorf("WhatsApp is off on this machine")
	}
	now, err := s.inviteWATarget(a.target.Key)
	if err != nil {
		return err
	}
	if now.Number != a.target.Number {
		return fmt.Errorf("the person's WhatsApp number changed after the approval; ask again")
	}
	if _, err := s.wa.ExistingDM(now.Number); err != nil {
		return err
	}
	inv, err := s.chats.CreateInvite(chat.InviteRequest{Kind: p2p.InviteDM, TTLMs: InviteWATTL.Milliseconds(), MaxUses: 1})
	if err != nil {
		return err
	}
	from := "the owner"
	if s.p2p != nil && s.p2p.DisplayName() != "" {
		from = s.p2p.DisplayName()
	}
	expires := time.UnixMilli(inv.ExpiresAt)
	msg, err := s.wa.SendInvite(a.caller.AgentID, now.Number, inviteWAText(now.Lang, now.Name, from, inv.Link, expires))
	if err != nil {
		// Not sent: an invite nobody received must not stay open.
		if rerr := s.chats.RevokeInvite(inv.ID); rerr != nil {
			s.log.Warn("invite by whatsapp: revoke after a failed send", "invite_id", inv.ID, "err", rerr)
		}
		return err
	}
	hash := InviteHash(inv.Link)
	a.result.Sent = &InviteSent{Person: now.Key, InviteID: inv.ID, ExpiresAt: inv.ExpiresAt, Hash: hash, MessageID: msg.ID}
	// The log keeps who, when, until when and a short hash: never the link, never the number.
	s.log.Info("invite sent by whatsapp", "person", now.Key, "at", time.Now().UTC().Format(time.RFC3339),
		"expires", expires.UTC().Format(time.RFC3339), "hash", hash, "invite_id", inv.ID, "agent", a.caller.AgentID)
	// One line for the owner, in the asking agent's own conversation with them.
	line := fmt.Sprintf("[tmx daemon] Join invite sent to %s by WhatsApp: single use, valid until %s (#%s). Revoke it under Contacts > Invites.",
		peertext.Field(now.Name), expires.Format("02.01. 15:04"), hash)
	if _, err := s.f.SendMessage(a.caller, fleet.SendRequest{To: model.Owner, Body: line}); err != nil {
		s.log.Warn("invite by whatsapp: owner notice failed", "person", now.Key, "err", err)
	}
	return nil
}

func (s *Server) inviteWATarget(ref string) (fleet.InviteTarget, error) {
	if strings.Contains(ref, "@") {
		c, err := s.wa.ExistingDM(ref)
		if err != nil {
			return fleet.InviteTarget{}, err
		}
		return fleet.InviteTarget{Key: c.JID, Name: c.Name, Number: c.JID}, nil
	}
	return s.f.PersonInviteTarget(ref)
}
