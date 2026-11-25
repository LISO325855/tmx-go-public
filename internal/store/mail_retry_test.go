package store

import (
	"path/filepath"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

func TestMailRetryBindingAndClaimSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mail.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	m := model.Message{ID: "one", From: "sender", To: "recipient", Body: "body", State: model.MsgQueued, RetryID: "retry", RetryHash: "binding", Receipt: "queued"}
	if err = s.PutMessage(m); err != nil {
		t.Fatal(err)
	}
	duplicate := m
	duplicate.ID = "two"
	if err = s.PutMessage(duplicate); err == nil {
		t.Fatal("duplicate sender identity admitted")
	}
	if err = s.ClaimMail([]string{m.ID, "missing"}, 10); err == nil {
		t.Fatal("partial claim accepted")
	}
	got, _ := s.GetMessage(m.ID)
	if got.State != model.MsgQueued {
		t.Fatal("partial claim was not rolled back")
	}
	if err = s.ClaimMail([]string{m.ID}, 11); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.RecoverMailReceipts(); err != nil {
		t.Fatal(err)
	}
	got, err = s.MessageByRetry("sender", "retry")
	if err != nil || got.ID != m.ID || got.RetryHash != "binding" || got.Receipt != "unconfirmed" || got.State != model.MsgSending {
		t.Fatalf("recovered %+v %v", got, err)
	}
	if err = s.ClaimMail([]string{m.ID}, 12); err == nil {
		t.Fatal("uncertain mail re-claimed")
	}
}

func TestMenuWaitSurvivesDaemonRestartAsQueuedMail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "menu.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, receipt := range []string{"menu_wait", "submitted", "dispatching"} {
		if err := s.PutMessage(model.Message{ID: receipt, From: "owner", To: "target", Body: receipt, State: model.MsgDelivered, Receipt: receipt, CreatedAt: 1, DeliveredAt: 2}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.RecoverMailReceipts(); err != nil {
		t.Fatal(err)
	}
	held, err := s.GetMessage("menu_wait")
	if err != nil || held.State != model.MsgQueued || held.Receipt != "queued" || held.DeliveredAt != 0 {
		t.Fatalf("%+v %v", held, err)
	}
	for _, id := range []string{"submitted", "dispatching"} {
		m, err := s.GetMessage(id)
		want := model.MsgDelivered
		if id == "dispatching" {
			want = model.MsgSending
		}
		if err != nil || m.State != want {
			t.Fatalf("uncertain/submitted message replayed: %+v %v", m, err)
		}
	}
	if err := s.ClaimMail([]string{held.ID}, 3); err != nil {
		t.Fatal("recovered message cannot be delivered:", err)
	}
}

func TestClaimMailIsSendingUntilSubmittedReceipt(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "sending.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.PutMessage(model.Message{ID: "m", From: "owner", To: "a", State: model.MsgQueued, Receipt: "queued", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimMail([]string{"m"}, 10); err != nil {
		t.Fatal(err)
	}
	m, _ := s.GetMessage("m")
	if m.State != model.MsgSending || m.DeliveredAt != 0 || m.Receipt != "dispatching" {
		t.Fatal(m)
	}
	if err := s.ClaimMail([]string{"m"}, 11); err == nil {
		t.Fatal("sending message was claimed twice")
	}
	for _, receipt := range []string{"menu_wait", "dispatching", "unconfirmed", "stalled"} {
		if err := s.SetMessageReceipt("m", receipt, 12); err != nil {
			t.Fatal(err)
		}
		m, _ = s.GetMessage("m")
		if m.State != model.MsgSending || m.DeliveredAt != 0 {
			t.Fatal(m)
		}
	}
	if err := s.SetMessageReceipt("m", "submitted", 20); err != nil {
		t.Fatal(err)
	}
	m, _ = s.GetMessage("m")
	if m.State != model.MsgDelivered || m.DeliveredAt != 20 {
		t.Fatal(m)
	}
	if err := s.SetMessageReceipt("m", "read", 30); err != nil {
		t.Fatal(err)
	}
	m, _ = s.GetMessage("m")
	if m.DeliveredAt != 20 {
		t.Fatal("reading changed submission time", m)
	}
}
