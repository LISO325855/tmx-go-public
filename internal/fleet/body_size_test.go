package fleet

import (
	"strings"
	"testing"
)

// A message between agents of this machine may be long (256 KB ceiling, 32 KB until 17 Nov);
// mail to another machine keeps the 32 KB the peer wire and older builds accept.
func TestMessageBodySizes(t *testing.T) {
	_, f, _, _, wc := peerEnv(t)                                               // wc: the lead of project alpha
	long := strings.Repeat("a long report, as long as it needs to be. ", 2500) // ~107 KB
	m, err := f.SendMessage(wc, SendRequest{To: "head", Body: long})
	if err != nil || m.To != f.HeadID() || len(m.Body) != len(long) {
		t.Fatalf("a long local message: %d bytes stored, %v", len(m.Body), err)
	}
	if _, err := f.SendMessage(wc, SendRequest{To: "head", Body: strings.Repeat("x", MaxBodyBytes+1)}); KindOf(err) != KindInvalid {
		t.Fatalf("beyond the ceiling: %v", err)
	}
	_, err = f.SendMessage(wc, SendRequest{To: "bob@hostc", Body: long})
	if KindOf(err) != KindInvalid || !strings.Contains(err.Error(), "another machine") {
		t.Fatalf("a long message to another machine: %v", err)
	}
	if _, err := f.SendMessage(wc, SendRequest{To: "bob@hostc", Body: strings.Repeat("x", MaxPeerBodyBytes)}); err != nil {
		t.Fatalf("32 KB to another machine: %v", err)
	}
}
