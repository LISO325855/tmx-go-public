package api_test

import (
	"net/url"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// Over real HTTP: the head reads every chat until the owner marks one private; then that chat
// is gone for the head in its list, its reads and its writes; an agent cannot change the lists.
func TestWhatsAppPrivateChatsOverHTTP(t *testing.T) {
	d := apitest.New(t)
	d.EnableWhatsApp(t)
	d.LinkWhatsApp(t)
	waSeed(d)
	owner := client{t: t, base: d.URL, token: d.OwnerToken}
	head := client{t: t, base: d.URL, token: d.AgentToken(t, "head")}

	var chats []model.WaChat
	if s := head.do("GET", "/api/whatsapp/chats", nil, &chats); s != 200 || len(chats) != 2 {
		t.Fatalf("the head's chats by default: %d %+v", s, chats)
	}
	private := map[string]any{"private_chats": []string{waBo}}
	if s := head.do("PUT", "/api/whatsapp/settings", private, nil); s != 403 {
		t.Fatalf("an agent changed the private chats: %d", s)
	}
	if s := head.do("PUT", "/api/whatsapp/settings", map[string]any{"chat_access": map[string]any{}}, nil); s != 403 {
		t.Fatalf("an agent changed the access table: %d", s)
	}
	var set model.WaSettings
	if s := owner.do("PUT", "/api/whatsapp/settings", private, &set); s != 200 || len(set.PrivateChats) != 1 || set.PrivateChats[0] != waBo {
		t.Fatalf("the owner marks a chat private: %d %+v", s, set.PrivateChats)
	}
	chats = nil
	if s := head.do("GET", "/api/whatsapp/chats", nil, &chats); s != 200 || len(chats) != 1 || chats[0].JID != waAnna {
		t.Fatalf("the head's chats with one private: %d %+v", s, chats)
	}
	chats = nil
	if s := head.do("GET", "/api/whatsapp/chats?q=bo", nil, &chats); s != 200 || len(chats) != 0 {
		t.Fatalf("a search found the private chat: %d %+v", s, chats)
	}
	var msgs []model.WaMessage
	if s := head.do("GET", "/api/whatsapp/chats/"+url.PathEscape(waAnna)+"/messages", nil, &msgs); s != 200 || len(msgs) == 0 {
		t.Fatalf("read an open chat: %d", s)
	}
	if s := head.do("GET", "/api/whatsapp/chats/"+url.PathEscape(waBo)+"/messages", nil, nil); s != 404 {
		t.Fatalf("read the private chat: %d", s)
	}
	if s := head.do("POST", "/api/whatsapp/chats/"+url.PathEscape(waBo)+"/messages", map[string]string{"text": "hi"}, nil); s != 404 {
		t.Fatalf("wrote to the private chat: %d", s)
	}
	if s := head.do("POST", "/api/whatsapp/chats/"+url.PathEscape(waAnna)+"/messages", map[string]string{"text": "hi"}, nil); s != 202 {
		t.Fatalf("asking to write to an open chat: %d", s)
	}
	// The owner sees every chat, whatever is private.
	chats = nil
	if s := owner.do("GET", "/api/whatsapp/chats", nil, &chats); s != 200 || len(chats) < 2 {
		t.Fatalf("the owner's chats: %d %+v", s, chats)
	}
	// No longer private: the head has it back.
	var cleared model.WaSettings
	if s := owner.do("PUT", "/api/whatsapp/settings", map[string]any{"private_chats": []string{}}, &cleared); s != 200 || len(cleared.PrivateChats) != 0 {
		t.Fatalf("clearing: %d %+v", s, cleared.PrivateChats)
	}
	chats = nil
	if s := head.do("GET", "/api/whatsapp/chats", nil, &chats); s != 200 || len(chats) != 2 {
		t.Fatalf("after clearing: %d %+v", s, chats)
	}
}
