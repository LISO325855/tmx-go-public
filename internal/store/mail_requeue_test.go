package store

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// RequeueUnconsumedMail queues again only what was claimed for that recipient since the given
// time and never settled, and leaves what skip names.
func TestRequeueUnconsumedMail(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "mail.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	put := func(id, to string, created int64) {
		t.Helper()
		if err := s.PutMessage(model.Message{ID: id, From: "owner", To: to, Body: id, State: model.MsgQueued, CreatedAt: created, Receipt: "queued"}); err != nil {
			t.Fatal(err)
		}
	}
	claim := func(at int64, receipt string, ids ...string) {
		t.Helper()
		if err := s.ClaimMail(ids, at); err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			if receipt != "dispatching" {
				if err := s.SetMessageReceipt(id, receipt, at); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	put("before", "a1", 1)       // claimed before the failed process started
	put("read", "a1", 2)         // claimed since, but settled
	put("dispatching", "a1", 3)  // claimed since, not settled
	put("accepted", "a1", 4)     // claimed since, written to the process
	put("unconfirmed", "a1", 5)  // claimed since, marked uncertain at the exit
	put("task", "a1", 6)         // claimed since, but a task receipt (skip)
	put("other-agent", "a2", 7)  // another recipient
	put("still-queued", "a1", 8) // never claimed
	claim(50, "accepted", "before")
	claim(120, "read", "read")
	claim(130, "dispatching", "dispatching")
	claim(140, "accepted", "accepted")
	claim(150, "unconfirmed", "unconfirmed")
	claim(160, "accepted", "task")
	claim(170, "accepted", "other-agent")

	ids, err := s.RequeueUnconsumedMail("a1", 100, 999, func(id string) bool { return id == "task" })
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"dispatching", "accepted", "unconfirmed"}; !slices.Equal(ids, want) {
		t.Fatalf("requeued %v, want %v (oldest first)", ids, want)
	}
	for id, want := range map[string]struct {
		state   model.MessageState
		receipt string
	}{
		"before":       {model.MsgDelivered, "accepted"},
		"read":         {model.MsgDelivered, "read"},
		"dispatching":  {model.MsgQueued, "queued"},
		"accepted":     {model.MsgQueued, "queued"},
		"unconfirmed":  {model.MsgQueued, "queued"},
		"task":         {model.MsgDelivered, "accepted"},
		"other-agent":  {model.MsgDelivered, "accepted"},
		"still-queued": {model.MsgQueued, "queued"},
	} {
		m, err := s.GetMessage(id)
		if err != nil {
			t.Fatal(err)
		}
		if m.State != want.state || m.Receipt != want.receipt {
			t.Errorf("%s: state %s receipt %s, want %s %s", id, m.State, m.Receipt, want.state, want.receipt)
		}
		if want.state == model.MsgQueued && id != "still-queued" && (m.DeliveredAt != 0 || m.ReceiptAt != 999) {
			t.Errorf("%s: delivered_at %d receipt_at %d after the requeue", id, m.DeliveredAt, m.ReceiptAt)
		}
	}
	// What was queued again can be claimed again, exactly like new mail.
	if err := s.ClaimMail([]string{"accepted"}, 1000); err != nil {
		t.Fatalf("a requeued message cannot be claimed again: %v", err)
	}
	// Nothing left to requeue for that window except the one just claimed.
	if again, err := s.RequeueUnconsumedMail("a1", 2000, 2001, nil); err != nil || len(again) != 0 {
		t.Fatalf("second requeue: %v %v", again, err)
	}
}
