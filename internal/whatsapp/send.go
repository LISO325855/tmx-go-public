package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/peertext"
	"github.com/blackcode-ch/tmx-go/internal/store"
)

// Sending. A request becomes one WhatsApp message per file (the text is the first file's
// caption) or one text message. Every message is stored at once as "pending" and announced;
// a queue per chat sends them in order, and the message's state follows
// by event (sent, delivered, read, or failed with the reason).

// Upload is a file to send (the API's {name, type, data}).
type Upload struct {
	Name string
	Type string
	Data []byte
}

// SendRequest is what the owner (or an allowed agent) sends to a chat.
type SendRequest struct {
	Text    string
	ReplyTo string
	Uploads []Upload
}

type sendJob struct {
	row  store.WaMessageRow
	out  Outgoing
	file *OutFile
}

type chatQueue struct {
	jobs []sendJob
	busy bool
}

// Send stores and queues a message; origin is "owner" or the agent's id. The first message is
// returned (pending); with several files the others come by event.
func (s *Service) Send(origin, chat string, req SendRequest) ([]model.WaMessage, error) {
	if err := s.ready(true); err != nil {
		return nil, err
	}
	c, err := s.Chat(chat)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(req.Text)
	if len(text) > MaxTextBytes {
		return nil, errorf(http.StatusBadRequest, "the text is longer than %d KB", MaxTextBytes>>10)
	}
	if !utf8.ValidString(text) {
		return nil, errorf(http.StatusBadRequest, "the text is not valid UTF-8")
	}
	if len(req.Uploads) > MaxUploads {
		return nil, errorf(http.StatusBadRequest, "at most %d files per message", MaxUploads)
	}
	if text == "" && len(req.Uploads) == 0 {
		return nil, errorf(http.StatusBadRequest, "the message is empty")
	}
	var quote *Quote
	var quoted *model.WaQuoted
	if req.ReplyTo != "" {
		q, err := s.o.Store.WaGetMessage(c.JID, req.ReplyTo)
		if err != nil || q.Deleted {
			return nil, errorf(http.StatusBadRequest, "no message %q to reply to in this chat", req.ReplyTo)
		}
		v := s.messageView(q)
		quote = &Quote{ID: q.ID, Sender: q.SenderJID, Text: firstNonEmpty(q.Text, q.Caption), FromMe: q.FromMe}
		if q.FromMe {
			quote.Sender = s.selfJID()
		}
		quoted = &model.WaQuoted{ID: q.ID, Text: shorten(firstNonEmpty(v.Text, v.Caption), 200), SenderName: v.SenderName}
	}

	type item struct {
		text string
		file *OutFile
	}
	var items []item
	for i, u := range req.Uploads {
		if len(u.Data) == 0 {
			return nil, errorf(http.StatusBadRequest, "%s is empty", u.Name)
		}
		if len(u.Data) > MaxUploadBytes {
			return nil, errorf(http.StatusRequestEntityTooLarge, "%s is larger than %d MB", u.Name, MaxUploadBytes>>20)
		}
		typ := firstNonEmpty(u.Type, "application/octet-stream")
		kind, _ := MediaKind(typ)
		f := &OutFile{Name: filepath.Base(firstNonEmpty(u.Name, "file")), Mime: typ, Kind: kind, Data: u.Data}
		caption := ""
		if i == 0 && kind != "audio" {
			caption, text = text, "" // the text goes with the first file (audio has no caption)
		}
		items = append(items, item{caption, f})
	}
	if text != "" {
		items = append(items, item{text: text})
	}
	now := s.o.Now()
	self := s.selfJID()
	var out []model.WaMessage
	var jobs []sendJob
	for i, it := range items {
		id := s.o.Client.NewMessageID()
		row := store.WaMessageRow{WaMessage: model.WaMessage{ID: id, ChatJID: c.JID, SenderJID: self, FromMe: true,
			At: now.UnixMilli() + int64(i), Type: model.WaText, Text: it.text, Status: model.WaPending, Origin: origin}}
		if i == 0 {
			row.Quoted = quoted
		}
		o := Outgoing{ID: id, Chat: c.JID, Text: it.text, File: it.file}
		if i == 0 {
			o.Quote = quote
		}
		if it.file != nil {
			_, typ := MediaKind(it.file.Mime)
			row.Type, row.Text, row.Caption = typ, "", it.text
			row.Media = &model.WaMedia{Mime: it.file.Mime, Size: int64(len(it.file.Data)), Name: it.file.Name}
			row.MediaFile = s.keepFile(c.JID, id, it.file.Mime, it.file.Data)
		}
		if err := s.o.Store.WaPutMessage(row); err != nil {
			s.markMedia(row.MediaFile, false)
			s.removeFile(row.MediaFile)
			return out, err
		}
		s.markMedia(row.MediaFile, false)
		signal(s.maintenanceWake)
		jobs = append(jobs, sendJob{row: row, out: o, file: it.file})
		out = append(out, s.messageView(row))
	}
	s.evMu.Lock()
	if cur, err := s.o.Store.WaGetChat(c.JID); err == nil {
		cur.LastAt, cur.Unread = max(cur.LastAt, now.UnixMilli()), 0
		_ = s.o.Store.WaPutChat(cur)
		c = s.chatView(cur)
	}
	s.evMu.Unlock()
	for _, m := range out {
		s.publish(model.EvWaMessage, m)
	}
	s.publishChat(c)
	s.enqueue(c.JID, jobs)
	return out, nil
}

func (s *Service) enqueue(chat string, jobs []sendJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queues[chat]
	if q == nil {
		q = &chatQueue{}
		s.queues[chat] = q
	}
	q.jobs = append(q.jobs, jobs...)
	if !q.busy {
		q.busy = true
		s.wg.Add(1)
		go s.drain(chat, q)
	}
}

// drain sends a chat's queue in order.
func (s *Service) drain(chat string, q *chatQueue) {
	defer s.wg.Done()
	for {
		s.mu.Lock()
		if len(q.jobs) == 0 {
			q.busy = false
			s.mu.Unlock()
			return
		}
		j := q.jobs[0]
		q.jobs = q.jobs[1:]
		s.mu.Unlock()
		if s.ctx.Err() != nil {
			return // marked failed at the next start
		}
		s.sendOne(j)
	}
}

func (s *Service) sendOne(j sendJob) {
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Minute)
	defer cancel()
	row := j.row
	err := s.ready(true)
	if err == nil && j.file != nil {
		var ref MediaRef
		if ref, err = s.o.Client.Upload(ctx, *j.file); err == nil {
			j.out.Media = &ref
			row.MediaRef = encodeRef(ref)
		}
	}
	var at time.Time
	var deferred bool
	if err == nil {
		cancel()
		if deferred, err = s.waitSendSlot(); err == nil {
			sendCtx, sendCancel := context.WithTimeout(s.ctx, 2*time.Minute)
			at, err = s.o.Client.Send(sendCtx, j.out)
			sendCancel()
		}
	}
	if errors.Is(err, context.Canceled) && s.ctx.Err() != nil {
		return // the daemon is stopping: marked failed at the next start
	}
	s.evMu.Lock()
	cur, gerr := s.o.Store.WaGetMessage(row.ChatJID, row.ID)
	if gerr != nil || cur.Deleted {
		s.evMu.Unlock()
		return
	}
	if gerr == nil { // keep a receipt that came in meanwhile
		row.Status = cur.Status
		if row.Media != nil && cur.Media != nil {
			md := *row.Media
			md.Transcript = cur.Media.Transcript
			md.Expired = cur.Media.Expired
			md.DownloadRetry, md.TranscribeRetry = cur.Media.DownloadRetry, cur.Media.TranscribeRetry
			row.Media = &md
		}
	}
	if err != nil {
		s.log.Warn("whatsapp send", "err", err)
		row.Status, row.Error = model.WaFailed, "not sent: "+err.Error()
	} else {
		if model.WaStatusRank(row.Status) < model.WaStatusRank(model.WaSent) {
			row.Status = model.WaSent
		}
		row.Error = ""
		if !at.IsZero() {
			row.At = at.UnixMilli()
		}
	}
	if err := s.o.Store.WaPutMessage(row); err != nil {
		s.log.Warn("whatsapp send", "err", err)
	}
	s.publishMessage(row.ChatJID, row.ID)
	s.evMu.Unlock()
	s.wakeMedia()
	if deferred && s.o.Fleet != nil && row.Origin != "" && row.Origin != model.Owner {
		outcome := "was sent"
		if err != nil {
			outcome = "could not be sent"
		}
		body := fmt.Sprintf("[WhatsApp] Deferred message %s %s. Use whatsapp_read for its status.", row.ID, outcome)
		if _, err := s.o.Fleet.DeliverWhatsApp(row.Origin, body); err != nil {
			s.log.Warn("whatsapp send outcome", "err", err)
		}
	}
}

func (s *Service) waitSendSlot() (deferred bool, err error) {
	for {
		if err := s.ctx.Err(); err != nil {
			return deferred, err
		}
		s.mu.Lock()
		account := s.acct.Phone
		if account == "" {
			account = s.acct.JID
		}
		if s.sendWindows == nil {
			s.sendWindows = make(map[string][]time.Time)
		}
		now := time.Now()
		window, retryAt := reserveSend(s.sendWindows[account], now)
		s.sendWindows[account] = window
		s.mu.Unlock()
		if retryAt.IsZero() {
			return deferred, nil
		}
		deferred = true
		timer := time.NewTimer(time.Until(retryAt))
		select {
		case <-timer.C:
		case <-s.ctx.Done():
			timer.Stop()
			return deferred, s.ctx.Err()
		}
	}
}

func reserveSend(window []time.Time, now time.Time) ([]time.Time, time.Time) {
	cutoff := now.Add(-time.Minute)
	for len(window) != 0 && !window[0].After(cutoff) {
		window = window[1:]
	}
	// Fleet-wide across agents and chats for this linked account. The owner
	// approved this limit on 24 Nov 2026 to avoid bulk-sending account bans.
	if len(window) >= 15 {
		return window, window[0].Add(time.Minute)
	}
	return append(window, now), time.Time{}
}

// ---- agents

// AgentResult is the outcome of an agent's send: sent at once (Message), or waiting for the
// owner's Allow (Approval).
type AgentResult struct {
	Message  *model.WaMessage `json:"message,omitempty"`
	Approval *model.Approval  `json:"approval,omitempty"`
	Chat     model.WaChat     `json:"chat"`
}

// AgentSend is an agent's message: text only, to one existing chat. It goes at once only where
// the owner allowed it (agents_send "allowed", or the chat in allowed_chats); otherwise it
// becomes an Approval of kind whatsapp and is sent when the owner allows it.
func (s *Service) AgentSend(agentID, chat, text, replyTo string) (AgentResult, error) {
	if err := s.CanRead(agentID); err != nil {
		return AgentResult{}, err
	}
	if err := s.ready(true); err != nil {
		return AgentResult{}, err
	}
	c, err := s.AgentFindChat(agentID, chat, true) // access.go: only chats the owner opened to it
	if err != nil {
		return AgentResult{}, err
	}
	text = strings.TrimSpace(text)
	switch {
	case text == "":
		return AgentResult{}, errorf(http.StatusBadRequest, "the message is empty")
	case len(text) > MaxAgentTextBytes:
		return AgentResult{}, errorf(http.StatusBadRequest, "an agent's WhatsApp message is at most %d KB", MaxAgentTextBytes>>10)
	}
	if replyTo != "" {
		if _, err := s.o.Store.WaGetMessage(c.JID, replyTo); err != nil {
			return AgentResult{}, errorf(http.StatusBadRequest, "no message %q to reply to in this chat", replyTo)
		}
	}
	set := s.Settings()
	if set.AgentsSend == model.WaAgentsAllowed || slices.Contains(set.AllowedChats, c.JID) {
		msgs, err := s.Send(agentID, c.JID, SendRequest{Text: text, ReplyTo: replyTo})
		if err != nil {
			return AgentResult{}, err
		}
		return AgentResult{Message: &msgs[0], Chat: c}, nil
	}
	ap, err := s.o.Fleet.WhatsAppApproval(agentID, model.WaSendInput{ChatJID: c.JID, ChatName: c.Name, Text: text, ReplyTo: replyTo})
	if err != nil {
		return AgentResult{}, err
	}
	return AgentResult{Approval: &ap, Chat: c}, nil
}

func (s *Service) pendingOf(agentID string) int {
	list, err := s.o.Fleet.Approvals(model.ApprovalPending)
	if err != nil {
		return 0
	}
	n := 0
	for _, ap := range list {
		if ap.Kind == model.ApprovalWhatsApp && ap.AgentID == agentID {
			n++
		}
	}
	return n
}

// Decide applies the owner's decision on an agent's message (fleet.WhatsAppDecider): allow
// sends it now (an error keeps the approval pending), deny drops it. The agent hears either way.
func (s *Service) Decide(ap model.Approval, decision, answer, message string) error {
	var in model.WaSendInput
	if err := decodeInput(ap, &in); err != nil {
		return err
	}
	name := peertext.Field(firstNonEmpty(in.ChatName, in.ChatJID)) // the chat's name is its members'
	switch decision {
	case "allow":
		if _, err := s.Send(ap.AgentID, in.ChatJID, SendRequest{Text: in.Text, ReplyTo: in.ReplyTo}); err != nil {
			return err
		}
		s.mail(ap.AgentID, fmt.Sprintf("[WhatsApp] The owner allowed your message to %q; it is being sent.", name))
	case "deny":
		body := fmt.Sprintf("[WhatsApp] The owner did not send your message to %q. Do not send it another way.", name)
		if m := strings.TrimSpace(message); m != "" {
			body += " The owner's note: " + m
		}
		s.mail(ap.AgentID, body)
	default:
		return errorf(http.StatusBadRequest, "decision must be allow or deny for a WhatsApp message")
	}
	return nil
}

func (s *Service) mail(agentID, body string) {
	if s.o.Fleet == nil || agentID == "" {
		return
	}
	if _, err := s.o.Fleet.DeliverWhatsApp(agentID, body); err != nil {
		s.log.Warn("whatsapp mail", "agent", agentID, "err", err)
	}
}

func shorten(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
