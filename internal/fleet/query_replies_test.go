package fleet

import (
	"github.com/blackcode-ch/tmx-go/internal/model"
	"testing"
)

func TestTerminalViewerRepliesDoNotDelayOwnerMail(t *testing.T) {
	_, f, _, a, p := typeTextLead(t)
	for _, reply := range []string{"\x1b[I", "\x1b[O", "\x1b[1;1R", "\x1b[?1;1R", "\x1b[?62;22c", "\x1b[0n", "\x1b]11;rgb:ffff/ffff/ffff\x07"} {
		if err := f.TerminalInput(OwnerCaller, a.ID, []byte(reply)); err != nil {
			t.Fatal(err)
		}
	}
	f.mu.Lock()
	at := f.agents[a.ID].humanInputAt
	f.mu.Unlock()
	if !at.IsZero() || len(p.Written()) != 0 {
		t.Fatalf("viewer counted as typing: %v %q", at, p.Written())
	}
	msg, err := f.SendOwner(a.ID, "phone message after passive viewing", "")
	if err != nil || msg.State != model.MsgDelivered || len(p.Sent()) != 1 {
		t.Fatalf("phone held: %+v %v", msg, err)
	}
	if err := f.TerminalInput(OwnerCaller, a.ID, []byte("\x1b[1;1Rhuman draft\x1b[A")); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	at = f.agents[a.ID].humanInputAt
	f.mu.Unlock()
	if at.IsZero() || string(p.Written()) != "human draft\x1b[A" {
		t.Fatalf("real input lost: %v %q", at, p.Written())
	}
}
