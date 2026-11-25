package whatsapp_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/whatsapp"
	"github.com/blackcode-ch/tmx-go/internal/whatsapp/watest"
)

func TestMediaStreamingCapAndQuota(t *testing.T) {
	for _, quota := range []bool{false, true} {
		t.Run(map[bool]string{false: "file_cap", true: "quota"}[quota], func(t *testing.T) {
			f := watest.Linked()
			msgs := mediaFixtures(t, f)
			f.Media["/incoming"] = make([]byte, (1<<20)+1) // declared size deliberately too small
			e := newEnv(t, f)
			e.mutateConfig(func(c *config.WhatsAppConfig) {
				if quota {
					c.MediaQuotaMB = 1
				} else {
					c.MediaMaxMB = 1
				}
			})
			f.Emit(whatsapp.EvConnected{Account: watest.Self})
			f.Emit(whatsapp.EvMessage{In: msgs[0]})
			if _, err := e.svc.Media(context.Background(), anna, "incoming"); err == nil {
				t.Fatal("oversize download accepted")
			}
			files, err := os.ReadDir(e.media)
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != 0 {
				t.Fatalf("partial files remain: %v", files)
			}
			row, _ := e.st.WaGetMessage(anna, "incoming")
			if row.MediaFile != "" {
				t.Fatal("failed download cached")
			}
		})
	}
}

func TestAutoMediaDiskFloorAndBackoff(t *testing.T) {
	f := watest.Linked()
	msgs := mediaFixtures(t, f)
	e := newEnv(t, f, func(o *whatsapp.Options) {
		o.AutoMedia = true
		o.DiskFree = func(string) (uint64, error) { return 4 << 30, nil }
	})
	f.Emit(whatsapp.EvConnected{Account: watest.Self})
	f.Emit(whatsapp.EvMessage{In: msgs[0]})
	awaitMedia(t, func() bool {
		r, _ := e.st.WaGetMessage(anna, "incoming")
		return r.Media != nil && r.Media.DownloadRetry != nil
	})
	f.Emit(whatsapp.EvMessage{In: msgs[0]})
	time.Sleep(30 * time.Millisecond)
	row, _ := e.st.WaGetMessage(anna, "incoming")
	n, _ := f.Counts()
	if n != 0 || row.Media.DownloadRetry.Attempts != 1 {
		t.Fatalf("floor/backoff: downloads=%d row=%+v", n, row.Media)
	}
	// An explicit owner download remains available below the automatic floor.
	if _, err := e.svc.Media(context.Background(), anna, "incoming"); err != nil {
		t.Fatal(err)
	}
}

func TestMediaDeleteAndScavenge(t *testing.T) {
	f := watest.Linked()
	msgs := mediaFixtures(t, f)
	e := newEnv(t, f)
	f.Emit(whatsapp.EvConnected{Account: watest.Self})
	f.Emit(whatsapp.EvHistory{Messages: msgs})
	a, err := e.svc.Media(context.Background(), anna, "incoming")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"orphan.ogg", ".download-leftover"} {
		if err := os.WriteFile(filepath.Join(e.media, name), []byte("stale"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.svc.Media(context.Background(), anna, "photo"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"orphan.ogg", ".download-leftover"} {
		awaitMedia(t, func() bool { _, err := os.Stat(filepath.Join(e.media, name)); return os.IsNotExist(err) })
		if _, err := os.Stat(filepath.Join(e.media, name)); !os.IsNotExist(err) {
			t.Fatalf("orphan retained %s: %v", name, err)
		}
	}
	f.Emit(whatsapp.EvChat{Chat: whatsapp.ChatUpdate{JID: anna, Delete: true}})
	if _, err := os.Stat(a.Path); !os.IsNotExist(err) {
		t.Fatalf("chat file retained %v", err)
	}
	f.Emit(whatsapp.EvHistory{Messages: msgs})
	if _, err := e.svc.Media(context.Background(), anna, "incoming"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Unlink(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.media); !os.IsNotExist(err) {
		t.Fatalf("account cache retained: %v", err)
	}
}

func TestAutoMediaSeparateQueuesAndRecentWindow(t *testing.T) {
	f := watest.Linked()
	msgs := mediaFixtures(t, f)
	started := make(chan struct{})
	release := make(chan struct{})
	e := newEnv(t, f, func(o *whatsapp.Options) {
		o.AutoMedia = true
		o.Transcribe = func(ctx context.Context, _ string) (string, error) {
			close(started)
			select {
			case <-release:
				return "speech", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	})
	f.Emit(whatsapp.EvConnected{Account: watest.Self})
	f.Emit(whatsapp.EvMessage{In: msgs[0]})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("ASR did not start")
	}
	old := msgs[2]
	old.Msg.ID = "old"
	old.Msg.At = e.clock.now().Add(-8 * 24 * time.Hour).UnixMilli()
	f.Emit(whatsapp.EvHistory{Messages: []whatsapp.Incoming{old, msgs[2], msgs[3]}})
	awaitMedia(t, func() bool {
		r, _ := e.st.WaGetMessage(anna, "file")
		p, _ := e.st.WaGetMessage(anna, "photo")
		return r.MediaFile != "" && p.MediaFile != ""
	})
	row, _ := e.st.WaGetMessage(anna, "old")
	if row.MediaFile != "" {
		t.Fatal("old history auto-downloaded")
	}
	close(release)
}

func TestMediaQueueNewestFirst(t *testing.T) {
	f := watest.Linked()
	msgs := mediaFixtures(t, f)
	e := newEnv(t, f)
	for i := range msgs {
		msgs[i].Msg.At = int64(i + 1)
	}
	f.Emit(whatsapp.EvHistory{Messages: msgs})
	batch, err := e.st.WaMediaBatch(0, e.clock.now().UnixMilli(), false, 20)
	if err != nil || len(batch) != len(msgs) {
		t.Fatalf("queue: %v %v", batch, err)
	}
	for i, item := range batch {
		if item.ID != msgs[len(msgs)-1-i].Msg.ID {
			t.Fatalf("not newest first: %v", batch)
		}
	}
}

func TestMediaStartupScavengerAndQuota(t *testing.T) {
	f := watest.Linked()
	msgs := mediaFixtures(t, f)
	f.Media["/incoming"] = make([]byte, 600<<10)
	f.Media["/outgoing"] = make([]byte, 600<<10)
	e := newEnv(t, f)
	f.Emit(whatsapp.EvConnected{Account: watest.Self})
	f.Emit(whatsapp.EvHistory{Messages: msgs[:2]})
	oldest, err := e.svc.Media(context.Background(), anna, "incoming")
	if err != nil {
		t.Fatal(err)
	}
	recent, err := e.svc.Media(context.Background(), anna, "outgoing")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(oldest.Path, old, old); err != nil {
		t.Fatal(err)
	}
	e.svc.Close()
	for _, name := range []string{"orphan.ogg", ".download-crashed"} {
		if err := os.WriteFile(filepath.Join(e.media, name), []byte("stale"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	next := watest.Linked()
	on := true
	svc := whatsapp.New(whatsapp.Options{Store: e.st, Client: next, MediaDir: e.media, AutoMedia: true,
		DiskFree: func(string) (uint64, error) { return 20 << 30, nil },
		Config:   func() *config.WhatsAppConfig { return &config.WhatsAppConfig{Enabled: &on, MediaQuotaMB: 1} }})
	t.Cleanup(svc.Close)
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	awaitMedia(t, func() bool {
		r, _ := e.st.WaGetMessage(anna, "incoming")
		_, a := os.Stat(filepath.Join(e.media, "orphan.ogg"))
		_, b := os.Stat(filepath.Join(e.media, ".download-crashed"))
		return r.Media != nil && r.Media.Expired && os.IsNotExist(a) && os.IsNotExist(b)
	})
	if _, err := os.Stat(oldest.Path); !os.IsNotExist(err) {
		t.Fatalf("oldest quota eviction: %v", err)
	}
	if _, err := os.Stat(recent.Path); err != nil {
		t.Fatalf("newer cache lost: %v", err)
	}
	if n, _ := next.Counts(); n != 0 {
		t.Fatal("quota eviction triggered redownload")
	}
}

func TestAutoMediaExpiredReferenceBackoff(t *testing.T) {
	f := watest.Linked()
	msgs := mediaFixtures(t, f)
	delete(f.Media, "/incoming")
	e := newEnv(t, f, func(o *whatsapp.Options) { o.AutoMedia = true })
	f.Emit(whatsapp.EvConnected{Account: watest.Self})
	f.Emit(whatsapp.EvMessage{In: msgs[0]})
	awaitMedia(t, func() bool {
		r, _ := e.st.WaGetMessage(anna, "incoming")
		return r.Media != nil && r.Media.DownloadRetry != nil
	})
	for i := 0; i < 5; i++ {
		f.Emit(whatsapp.EvMessage{In: msgs[0]})
	}
	time.Sleep(30 * time.Millisecond)
	if n, _ := f.Counts(); n != 1 {
		t.Fatalf("failed reference retried every wake: %d", n)
	}
	e.clock.add(time.Minute + time.Second)
	f.Emit(whatsapp.EvMessage{In: msgs[0]})
	awaitMedia(t, func() bool {
		r, _ := e.st.WaGetMessage(anna, "incoming")
		return r.Media != nil && r.Media.DownloadRetry != nil && r.Media.DownloadRetry.Attempts == 2
	})
	if n, _ := f.Counts(); n != 2 {
		t.Fatalf("due retry count: %d", n)
	}
}
