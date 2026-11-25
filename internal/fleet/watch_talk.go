package fleet

// Talk counters (DESIGN.md "Watch"): how much agents talk, read off the event bus. Alert only:
// a line in the head's digest when a threshold is crossed. Nothing here blocks, pauses, delays
// or drops a message, a chat or a board change, and nothing is refused because of it.

import (
	"fmt"
	"strings"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// Thresholds (generous on purpose: a busy fleet is normal, a loop is not).
const (
	watchTalkWindow = 10 * time.Minute
	// watchMailPer10: one sender to one recipient, messages per 10 min.
	watchMailPer10 = 60
	// watchBoardPer10: topics opened, changed, removed or turns moved, per 10 min.
	watchBoardPer10 = 60
	// watchCompanionPer10: messages to and from Companion, per 10 min.
	watchCompanionPer10 = 30
	// watchInboxWaiting: messages waiting in one agent's mailbox.
	watchInboxWaiting = 40
	// A chat's agents talking with no message from a person: a digest line at
	// watchChatPer10 in 10 min, urgent when it keeps going (watchChatPer30 in 30 min).
	watchChatPer10     = 50
	watchChatLong      = 30 * time.Minute
	watchChatPer30     = 200
	watchChatSystem    = "system"
	watchMailSeparator = "\x1f"
)

// SetChatNamer gives the watch the name of a chat by its ID (the daemon wires the chat
// service); without it a chat is named by its ID.
func (f *Fleet) SetChatNamer(fn func(id string) string) {
	f.mu.Lock()
	f.watch.chatName = fn
	f.mu.Unlock()
}

// watchEvent counts one bus event. The watch loop feeds it every event on the bus.
func (f *Fleet) watchEvent(ev model.BusEvent) {
	b := f.watch.board
	if !b.on() {
		return
	}
	switch ev.Type {
	case model.EvMessageCreated:
		m, ok := ev.Data.(model.Message)
		if !ok || m.ID == "" {
			return
		}
		switch {
		case m.From == model.CompanionParty || m.To == model.CompanionParty:
			b.hit("talk:companion", m.ID)
		case m.From == model.Owner || m.To == model.Owner || m.From == model.WatchParty || m.From == model.ReminderParty ||
			m.From == model.WhatsAppParty || IsExternal(m.From):
			// the owner's own words, tmx's own mail and what arrives from outside are not agent talk
		default:
			b.hit("talk:mail:"+m.From+watchMailSeparator+m.To, m.ID)
		}
	case model.EvThreadUpdated, model.EvThreadRemoved, model.EvBoardChanged:
		b.hit("talk:board", "")
	case model.EvChatMessage:
		var m model.ChatMessage
		switch d := ev.Data.(type) {
		case model.ChatMessage:
			m = d
		case *model.ChatMessage:
			if d == nil {
				return
			}
			m = *d
		default:
			return
		}
		if m.ID == "" || m.ChatID == "" || m.Sender == watchChatSystem {
			return
		}
		if m.SenderAgent == "" { // a person wrote: the agents' run in this chat is over
			if b.hit("chatperson:"+m.ChatID, m.ID) {
				b.unhit("chat:" + m.ChatID)
			}
			return
		}
		b.hit("chat:"+m.ChatID, m.ID)
	}
}

// watchTalkScan turns the counters into rows. Called by watchScan, without Fleet.mu.
func (f *Fleet) watchTalkScan() {
	b := f.watch.board
	var items []model.WatchItem
	talk, chats := map[string]bool{}, map[string]bool{}
	for key, n := range b.rates("talk:", watchTalkWindow, watchTalkWindow) {
		switch {
		case key == "talk:board" && n[0] >= watchBoardPer10:
			talk[key] = true
			items = append(items, model.WatchItem{Key: key, Status: "busy",
				Text: fmt.Sprintf("the board changed %d times in the last 10 min (topics opened, changed, moved or removed); nothing was blocked", n[0])})
		case key == "talk:companion" && n[0] >= watchCompanionPer10:
			talk[key] = true
			items = append(items, model.WatchItem{Key: key, Status: "busy",
				Text: fmt.Sprintf("%d messages went to and from Companion in the last 10 min; nothing was blocked", n[0])})
		case strings.HasPrefix(key, "talk:mail:") && n[0] >= watchMailPer10:
			from, to, _ := strings.Cut(strings.TrimPrefix(key, "talk:mail:"), watchMailSeparator)
			row := "talk:mail:" + from + ":" + to
			talk[row] = true
			items = append(items, model.WatchItem{Key: row, Status: "busy",
				Text: fmt.Sprintf("%s sent %s %d messages in the last 10 min; nothing was blocked", f.nameOf(from), f.nameOf(to), n[0])})
		}
	}
	f.mu.Lock()
	namer := f.watch.chatName
	f.mu.Unlock()
	for key, n := range b.rates("chat:", watchTalkWindow, watchChatLong) {
		if n[0] < watchChatPer10 && n[1] < watchChatPer30 {
			continue
		}
		id := strings.TrimPrefix(key, "chat:")
		name := ""
		if namer != nil {
			name = namer(id)
		}
		if name == "" {
			name = id
		}
		row := "chatrun:" + id
		chats[row] = true
		it := model.WatchItem{Key: row, Status: "busy",
			Text: fmt.Sprintf("agents wrote %d messages in the chat %q in the last 10 min with no message from a person; the chat runs on, nothing was paused", n[0], name)}
		if n[1] >= watchChatPer30 {
			it.Status, it.Urgent = "runaway", true
			it.Text = fmt.Sprintf("agents keep talking in the chat %q: %d messages in the last 30 min with no message from a person; the chat runs on, nothing was paused", name, n[1])
		}
		items = append(items, it)
	}
	b.report(items...)
	b.keep("talk:", talk)
	b.keep("chatrun:", chats)
}
