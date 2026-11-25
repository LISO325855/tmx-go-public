package api_test

import (
	"context"
	"encoding/json"
	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/coder/websocket"
	"strings"
	"testing"
	"time"
)

func TestEventsApplicationHeartbeat(t *testing.T) {
	d := apitest.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, strings.Replace(d.URL, "http://", "ws://", 1)+"/api/events?token="+d.OwnerToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	for {
		_, body, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var ev struct {
			Type string `json:"type"`
			At   int64  `json:"at"`
		}
		if err := json.Unmarshal(body, &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Type == "heartbeat" {
			if ev.At == 0 {
				t.Fatal("heartbeat missing timestamp")
			}
			return
		}
	}
}
