package whatsapp

import (
	"context"
	"testing"
	"time"
)

func TestAccountSendWindow(t *testing.T) {
	start := time.Unix(1000, 0)
	var window []time.Time
	for i := 0; i < 15; i++ {
		var retry time.Time
		window, retry = reserveSend(window, start)
		if !retry.IsZero() {
			t.Fatalf("send %d was delayed until %v", i+1, retry)
		}
	}
	if _, retry := reserveSend(window, start.Add(59*time.Second)); !retry.Equal(start.Add(time.Minute)) {
		t.Fatalf("send 16 retries at %v, want %v", retry, start.Add(time.Minute))
	}
	if next, retry := reserveSend(window, start.Add(time.Minute)); len(next) != 1 || !retry.IsZero() {
		t.Fatalf("send 16 at the next free slot: window=%v retry=%v", next, retry)
	}
}

func TestAccountSendWaitUsesLinkedAccount(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Minute + 30*time.Millisecond)
	expiry := old.Add(time.Minute)
	full := make([]time.Time, 15)
	for i := range full {
		full[i] = old
	}
	s := Service{
		ctx:         context.Background(),
		acct:        Account{Phone: "account-b"},
		sendWindows: map[string][]time.Time{"account-a": full},
	}
	if deferred, err := s.waitSendSlot(); err != nil || deferred || len(s.sendWindows["account-b"]) != 1 {
		t.Fatalf("other account did not reserve a slot: %v", err)
	}
	s.acct.Phone = "account-a"
	if _, err := s.waitSendSlot(); err != nil {
		t.Fatal(err)
	}
	if sent := s.sendWindows["account-a"][0]; sent.Before(expiry) {
		t.Fatalf("account sent at %v before its slot opened at %v", sent, expiry)
	}
}
