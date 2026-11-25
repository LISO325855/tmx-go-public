package whatsapp_test

import (
	"net/http"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/whatsapp"
)

// accessEnv: the seeded account with an EMPTY access table and no private chats, the head (h1),
// two leads (l1, l2) whose profile may use WhatsApp, and a worker (w1) whose profile may not.
func accessEnv(t *testing.T) *env {
	t.Helper()
	e := linkedEnv(t)
	e.seed()
	e.fleet.mu.Lock()
	e.fleet.profiles["l1"], e.fleet.profiles["l2"] = "lead", "lead"
	e.fleet.mu.Unlock()
	none := map[string]model.WaChatAccess{}
	profiles := []string{"head", "lead"}
	if _, err := e.svc.SetSettings(whatsapp.SettingsPatch{ChatAccess: &none, AgentProfiles: &profiles}); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) access(table map[string]model.WaChatAccess) {
	e.t.Helper()
	if _, err := e.svc.SetSettings(whatsapp.SettingsPatch{ChatAccess: &table}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) private(chats ...string) {
	e.t.Helper()
	if _, err := e.svc.SetSettings(whatsapp.SettingsPatch{PrivateChats: &chats}); err != nil {
		e.t.Fatal(err)
	}
}

func chatJIDs(list []model.WaChat) []string {
	out := []string{}
	for _, c := range list {
		out = append(out, c.JID)
	}
	return out
}

// pendingApprovals are the owner cards the fake fleet holds.
func (f *fakeFleet) pendingApprovals() []model.Approval {
	list, _ := f.Approvals(model.ApprovalPending)
	return list
}

// The head reads every chat, as it always did; a chat the owner marks private does not exist
// for it any more: not in its list, not found by any name, not readable, not writable, and no
// note about its new messages.
func TestTheHeadReadsEveryChatExceptThePrivateOnes(t *testing.T) {
	e := accessEnv(t)
	all, err := e.svc.Chats("", 0)
	if err != nil || len(all) < 3 {
		t.Fatalf("the owner's chats: %v %v", chatJIDs(all), err)
	}
	if list, err := e.svc.AgentChats("h1", "", 0); err != nil || len(list) != len(all) {
		t.Fatalf("the head's chats by default: %v %v", chatJIDs(list), err)
	}
	if msgs, err := e.svc.AgentMessages("h1", "Anna Muster", "", 0); err != nil || len(msgs) == 0 {
		t.Fatalf("the head reads a chat: %d %v", len(msgs), err)
	}

	e.private(anna)
	list, err := e.svc.AgentChats("h1", "", 0)
	if err != nil || len(list) != len(all)-1 {
		t.Fatalf("the head's chats with one private: %v %v", chatJIDs(list), err)
	}
	for _, c := range list {
		if c.JID == anna {
			t.Fatal("the private chat is in the head's list")
		}
	}
	if list, _ := e.svc.AgentChats("h1", "anna", 0); len(list) != 0 {
		t.Fatalf("a search found the private chat: %v", chatJIDs(list))
	}
	for _, ref := range []string{"Anna Muster", anna, "+41 79 111 11 11"} {
		if _, err := e.svc.AgentMessages("h1", ref, "", 0); status(err) != http.StatusNotFound {
			t.Fatalf("the head read the private chat as %q: %v", ref, err)
		}
		if res, err := e.svc.AgentSend("h1", ref, "hi", ""); status(err) != http.StatusNotFound || res.Approval != nil {
			t.Fatalf("the head wrote to the private chat as %q: %+v %v", ref, res, err)
		}
	}
	if n := len(e.fleet.pendingApprovals()); n != 0 {
		t.Fatalf("%d owner cards for a private chat", n)
	}
	// Private wins over every other list that names the chat.
	lists := []string{anna}
	if _, err := e.svc.SetSettings(whatsapp.SettingsPatch{AllowedChats: &lists, NotifyHeadChats: &lists}); err != nil {
		t.Fatal(err)
	}
	e.access(map[string]model.WaChatAccess{"head": {Read: []string{anna}}})
	if e.svc.CanReadChat("h1", anna) {
		t.Fatal("another list reopened a private chat to the head")
	}
	// The other chats are untouched, and the owner still sees everything.
	if _, err := e.svc.AgentMessages("h1", bo, "", 0); err != nil {
		t.Fatalf("the head reads another chat: %v", err)
	}
	if list, _ := e.svc.Chats("", 0); len(list) != len(all) {
		t.Fatalf("the owner's chats: %v", chatJIDs(list))
	}
	// Taken off the private list, the chat is back.
	e.private()
	if !e.svc.CanReadChat("h1", anna) {
		t.Fatal("the chat stayed closed after it was no longer private")
	}
}

// The head may be given a private chat to write to without reading it.
func TestTheHeadMayAskToWriteToAPrivateChatOnItsSendList(t *testing.T) {
	e := accessEnv(t)
	e.private(bo)
	e.access(map[string]model.WaChatAccess{"head": {Send: []string{bo}}})
	if res, err := e.svc.AgentSend("h1", bo, "The report is ready", ""); err != nil || res.Approval == nil || res.Message != nil {
		t.Fatalf("ask to write: %+v %v", res, err)
	}
	if _, err := e.svc.AgentMessages("h1", bo, "", 0); status(err) != http.StatusNotFound {
		t.Fatalf("the private chat was read: %v", err)
	}
}

// Every other agent: nothing by default.
func TestNoChatIsOpenToOtherAgentsByDefault(t *testing.T) {
	e := accessEnv(t)
	if list, err := e.svc.AgentChats("l1", "", 0); err != nil || len(list) != 0 {
		t.Fatalf("a lead's chats: %v %v", chatJIDs(list), err)
	}
	if list, err := e.svc.AgentChats("l1", "anna", 0); err != nil || len(list) != 0 {
		t.Fatalf("a search found a closed chat: %v %v", chatJIDs(list), err)
	}
	for _, ref := range []string{"Anna Muster", anna, "+41 79 111 11 11", "Family", group} {
		if _, err := e.svc.AgentMessages("l1", ref, "", 0); status(err) != http.StatusNotFound {
			t.Fatalf("read %q: %v", ref, err)
		}
		if res, err := e.svc.AgentSend("l1", ref, "hi", ""); status(err) != http.StatusNotFound || res.Approval != nil || res.Message != nil {
			t.Fatalf("write to %q: %+v %v", ref, res, err)
		}
	}
	if n := len(e.fleet.pendingApprovals()); n != 0 {
		t.Fatalf("%d owner cards for chats that are closed", n)
	}
	// What the head is told about is not thereby open to others.
	notify := []string{anna}
	if _, err := e.svc.SetSettings(whatsapp.SettingsPatch{NotifyHeadChats: &notify}); err != nil {
		t.Fatal(err)
	}
	if e.svc.CanReadChat("l1", anna) {
		t.Fatal("notify_head_chats opened a chat to a lead")
	}
}

// A chat opened to a profile is listed, found and read by its agents; the others stay closed.
func TestAChatOpenedToAProfileIsReadByItsAgents(t *testing.T) {
	e := accessEnv(t)
	e.access(map[string]model.WaChatAccess{"lead": {Read: []string{anna}}})

	list, err := e.svc.AgentChats("l1", "", 0)
	if err != nil || len(list) != 1 || list[0].JID != anna {
		t.Fatalf("the lead's chats: %v %v", chatJIDs(list), err)
	}
	if list, _ := e.svc.AgentChats("l1", "bo", 0); len(list) != 0 {
		t.Fatalf("a search found a closed chat: %v", chatJIDs(list))
	}
	if msgs, err := e.svc.AgentMessages("l1", "Anna Muster", "", 0); err != nil || len(msgs) == 0 {
		t.Fatalf("read the open chat: %d %v", len(msgs), err)
	}
	if _, err := e.svc.AgentMessages("l1", bo, "", 0); status(err) != http.StatusNotFound {
		t.Fatalf("read a closed chat: %v", err)
	}
	// Writing to a readable chat still waits for the owner's card.
	if res, err := e.svc.AgentSend("l1", anna, "On my way", ""); err != nil || res.Approval == nil || res.Message != nil {
		t.Fatalf("write to the open chat: %+v %v", res, err)
	}
	// A chat that is private for the head stays open to a profile the owner opened it to.
	e.private(anna)
	if !e.svc.CanReadChat("l1", anna) || e.svc.CanReadChat("h1", anna) {
		t.Fatalf("private is the head's: lead %v, head %v", e.svc.CanReadChat("l1", anna), e.svc.CanReadChat("h1", anna))
	}
}

// Send only: the agent may ask to write there (a card), and neither sees nor reads the chat.
func TestASendOnlyChat(t *testing.T) {
	e := accessEnv(t)
	e.access(map[string]model.WaChatAccess{"lead": {Send: []string{bo}}})
	if res, err := e.svc.AgentSend("l1", bo, "The report is ready", ""); err != nil || res.Approval == nil || res.Message != nil {
		t.Fatalf("ask to write: %+v %v", res, err)
	}
	if list, _ := e.svc.AgentChats("l1", "", 0); len(list) != 0 {
		t.Fatalf("a send-only chat is listed: %v", chatJIDs(list))
	}
	if _, err := e.svc.AgentMessages("l1", bo, "", 0); status(err) != http.StatusNotFound {
		t.Fatalf("a send-only chat was read: %v", err)
	}
	if _, err := e.svc.AgentSend("l1", anna, "hi", ""); status(err) != http.StatusNotFound {
		t.Fatalf("wrote to a chat outside the list: %v", err)
	}
}

// One agent by name; the chats agents write to without asking; a profile that may not use
// WhatsApp at all.
func TestAccessByAgentNameAndByAllowedChats(t *testing.T) {
	e := accessEnv(t)
	e.access(map[string]model.WaChatAccess{"agent:l1": {Read: []string{group}}})
	if list, _ := e.svc.AgentChats("l1", "", 0); len(list) != 1 || list[0].JID != group {
		t.Fatalf("l1's chats: %v", chatJIDs(list))
	}
	if list, _ := e.svc.AgentChats("l2", "", 0); len(list) != 0 {
		t.Fatalf("another agent of the same profile: %v", chatJIDs(list))
	}
	// allowed_chats: agents write there without asking, so they read it too.
	allowed := []string{bo}
	if _, err := e.svc.SetSettings(whatsapp.SettingsPatch{AllowedChats: &allowed}); err != nil {
		t.Fatal(err)
	}
	if !e.svc.CanReadChat("l2", bo) {
		t.Fatal("an allowed chat is not readable")
	}
	// A profile that may not use WhatsApp gains nothing from an entry of its own.
	e.access(map[string]model.WaChatAccess{"agent:w1": {Read: []string{anna}, Send: []string{anna}}, "worker": {Read: []string{anna}}})
	if e.svc.CanReadChat("w1", anna) || e.svc.CanSendChat("w1", anna) || e.svc.CanReadChat("w1", bo) {
		t.Fatal("a worker reads or writes through chat_access although its profile may not use WhatsApp")
	}
	if _, err := e.svc.AgentChats("w1", "", 0); status(err) != http.StatusForbidden {
		t.Fatalf("a worker lists chats: %v", err)
	}
}

func TestChatAccessIsValidatedAndStored(t *testing.T) {
	e := accessEnv(t)
	for name, bad := range map[string]map[string]model.WaChatAccess{
		"a key that is no slug": {"Head Agents!": {Read: []string{anna}}},
		"a chat that is no JID": {"head": {Read: []string{"Anna Muster"}}},
		"an empty agent name":   {"agent:": {Read: []string{anna}}},
	} {
		if _, err := e.svc.SetSettings(whatsapp.SettingsPatch{ChatAccess: &bad}); status(err) != http.StatusBadRequest {
			t.Fatalf("%s: %v", name, err)
		}
	}
	notAChat := []string{"Anna Muster"}
	if _, err := e.svc.SetSettings(whatsapp.SettingsPatch{PrivateChats: &notAChat}); status(err) != http.StatusBadRequest {
		t.Fatalf("a private chat that is no JID: %v", err)
	}
	if got := e.svc.Settings(); len(got.ChatAccess) != 0 || len(got.PrivateChats) != 0 {
		t.Fatalf("a refused change was stored: %+v %v", got.ChatAccess, got.PrivateChats)
	}
	table := map[string]model.WaChatAccess{"lead": {Read: []string{anna, " ", anna}, Send: []string{bo}}, "head": {}}
	set, err := e.svc.SetSettings(whatsapp.SettingsPatch{ChatAccess: &table})
	if err != nil {
		t.Fatal(err)
	}
	if l := set.ChatAccess["lead"]; len(set.ChatAccess) != 1 || len(l.Read) != 1 || l.Read[0] != anna || len(l.Send) != 1 || l.Send[0] != bo {
		t.Fatalf("stored: %+v", set.ChatAccess)
	}
	e.private(group, group)
	// Another settings change keeps the table and the private list.
	receipts := true
	if set, err = e.svc.SetSettings(whatsapp.SettingsPatch{ReadReceipts: &receipts}); err != nil || len(set.ChatAccess["lead"].Read) != 1 || len(set.PrivateChats) != 1 {
		t.Fatalf("after another change: %+v %v %v", set.ChatAccess, set.PrivateChats, err)
	}
}
