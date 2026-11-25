package whatsapp

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/store"
)

type scanFixtureInfo struct {
	os.FileInfo
	name string
}

func (f scanFixtureInfo) Name() string { return f.name }

// A deterministic slow filesystem fixture: thousands of directory entries with
// the scan paused halfway. No network, real account, or large on-disk fixture.
func TestMediaMaintenanceDoesNotBlockConnectionEvents(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "tmx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	media := filepath.Join(dir, "media")
	if err := os.Mkdir(media, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "fixture")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	svc := New(Options{Store: st, MediaDir: media})
	paused, release, scanned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	svc.maintenanceScan = func(visit func(os.FileInfo)) error {
		for i := 0; i < 4096; i++ {
			if i == 2048 {
				close(paused)
				<-release
			}
			visit(scanFixtureInfo{FileInfo: info, name: fmt.Sprintf("fixture-%04d.ogg", i)})
		}
		close(scanned)
		return nil
	}
	t.Cleanup(func() { once.Do(func() { close(release) }); svc.Close() })
	svc.wg.Add(1)
	go svc.mediaMaintenance()
	select {
	case <-paused:
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not reach barrier")
	}
	// The maintenance writer gate is held, but neither the event nor state locks
	// may be held across the stalled filesystem operation.
	events := make(chan struct{})
	go func() {
		svc.handle(EvConnected{Account: Account{JID: "fixture@s.whatsapp.net"}})
		svc.handle(EvDisconnected{})
		svc.handle(EvConnected{Account: Account{JID: "fixture@s.whatsapp.net"}})
		close(events)
	}()
	select {
	case <-events:
		if svc.Status().State != model.WaConnected {
			t.Fatal("connection state did not update")
		}
	case <-time.After(time.Second):
		t.Fatal("connection events blocked behind cache scan")
	}
	once.Do(func() { close(release) })
	select {
	case <-scanned:
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not resume")
	}
}
