package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

func TestPassiveTerminalSocketDoesNotHoldOwnerChat(t *testing.T) {
	d := apitest.New(t) // isolated TMXG_HOME, database, and ephemeral listening port
	owner := client{t: t, base: d.URL, token: d.OwnerToken}
	if code := owner.do("POST", "/api/agents", map[string]string{"name": "viewer-chat", "harness": "claude", "mode": "terminal"}, nil); code != 201 {
		t.Fatalf("launch: %d", code)
	}
	p := d.Proc(t, "viewer-chat")
	p.SetReplay([]byte("prompt"))
	p.State(model.StateIdle)
	deadline := time.Now().Add(3 * time.Second)
	for a, _ := d.Fleet.Agent("viewer-chat"); a.State != model.StateIdle; a, _ = d.Fleet.Agent("viewer-chat") {
		if time.Now().After(deadline) {
			t.Fatal("fixture not idle")
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws := strings.Replace(d.URL, "http://", "ws://", 1) + "/api/agents/viewer-chat/terminal?cols=86&rows=32&token=" + d.OwnerToken
	conn, _, err := websocket.Dial(ctx, ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if _, _, err = conn.Read(ctx); err != nil {
		t.Fatal(err)
	}
	for _, msg := range []string{`{"type":"input","data":"\u001b[1;1R"}`, `{"type":"input","data":"\u001b[I"}`, `{"type":"input","data":"\u001b[O"}`, `{"type":"resize","cols":87,"rows":33}`} {
		if err := conn.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
			t.Fatal(err)
		}
	}
	// The final resize confirms all preceding input frames have been handled.
	for {
		c, r := p.Size()
		if c == 87 && r == 33 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("resize not processed")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if code := owner.do("POST", "/api/agents/viewer-chat/send", map[string]string{"text": "owner chat with terminal open"}, nil); code != 200 {
		t.Fatalf("send: %d", code)
	}
	if len(p.Sent()) != 1 || len(p.Written()) != 0 {
		t.Fatalf("passive viewing held chat: sent=%q written=%q", p.Sent(), p.Written())
	}
}
