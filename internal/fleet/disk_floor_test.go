package fleet

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// With the data disk below the floor, agents' mail is refused in a plain sentence and the
// owner's message is still stored; with room again, mail flows.
func TestMailDiskFloor(t *testing.T) {
	free := uint64(50 << 30)
	old := mailDiskSpace
	mailDiskSpace = func(string) (uint64, uint64, error) { return free, 500 << 30, nil }
	t.Cleanup(func() { mailDiskSpace = old })

	e := newUnguardedEnv(t)
	f := e.started()
	proj := workerProject(t, f)
	a, _ := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: proj, Name: "sender"})
	rcv, err := f.LaunchAgent(OwnerCaller, LaunchRequest{ProjectID: proj, Name: "receiver"})
	if err != nil {
		t.Fatal(err)
	}
	again := func() { // forget the last reading
		f.mu.Lock()
		f.diskReadAt = time.Time{}
		f.mu.Unlock()
	}
	if _, err := f.SendMessage(AgentCaller(a.ID), SendRequest{To: "receiver", Body: "plenty of room"}); err != nil {
		t.Fatalf("with room: %v", err)
	}

	free = 1 << 30 // under 2 GB
	again()
	_, err = f.SendMessage(AgentCaller(a.ID), SendRequest{To: "receiver", Body: "disk nearly full"})
	if KindOf(err) != KindTooMany || !strings.Contains(err.Error(), "nearly full") {
		t.Fatalf("below the floor: %v", err)
	}
	if _, err := f.SendOwner("receiver", "the owner still gets through", ""); err != nil {
		t.Fatalf("the owner's message below the floor: %v", err)
	}
	if _, err := f.DeliverCompanion(rcv.ID, "Companion acts for the owner"); err != nil {
		t.Fatalf("Companion's message below the floor: %v", err)
	}
	if _, err := f.DeliverExternal(rcv.ID, ExternalPrefix+"chat1", "from outside"); KindOf(err) != KindTooMany {
		t.Fatalf("mail from outside below the floor: %v", err)
	}

	free = 50 << 30
	again()
	if _, err := f.SendMessage(AgentCaller(a.ID), SendRequest{To: "receiver", Body: "room again"}); err != nil {
		t.Fatalf("with room again: %v", err)
	}

	// A small volume: a tenth of it is the floor, not 2 GB.
	mailDiskSpace = func(string) (uint64, uint64, error) { return 1 << 30, 8 << 30, nil }
	again()
	if _, err := f.SendMessage(AgentCaller(a.ID), SendRequest{To: "receiver", Body: "small disk, enough free"}); err != nil {
		t.Fatalf("1 GB free of 8 GB: %v", err)
	}
	// A reading that fails refuses nothing.
	mailDiskSpace = func(string) (uint64, uint64, error) { return 0, 0, errors.New("no such volume") }
	again()
	if _, err := f.SendMessage(AgentCaller(a.ID), SendRequest{To: "receiver", Body: "unknown disk"}); err != nil {
		t.Fatalf("with an unreadable disk: %v", err)
	}
}
