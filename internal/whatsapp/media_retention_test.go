package whatsapp_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/whatsapp"
	"github.com/blackcode-ch/tmx-go/internal/whatsapp/watest"
)

func TestMediaRetention(t *testing.T) {
	for _, tc := range []struct {
		name             string
		configured, days int
	}{
		{"default30", 0, 30}, {"configured2", 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := watest.Linked()
			msgs := mediaFixtures(t, f)
			msgs[1].Msg.Media.Transcript = "saved speech"
			e := newEnv(t, f)
			e.mutateConfig(func(c *config.WhatsAppConfig) { c.MediaRetentionDays = tc.configured })
			f.Emit(whatsapp.EvConnected{Account: watest.Self})
			f.Emit(whatsapp.EvHistory{Messages: msgs})
			var expired []string
			for _, id := range []string{"incoming", "outgoing"} {
				file, err := e.svc.Media(context.Background(), anna, id)
				if err != nil {
					t.Fatal(err)
				}
				expired = append(expired, file.Path)
			}
			photo, err := e.svc.Media(context.Background(), anna, "photo")
			if err != nil {
				t.Fatal(err)
			}
			old := e.clock.now().Add(-time.Duration(tc.days)*24*time.Hour - time.Hour)
			for _, path := range expired {
				if err := os.Chtimes(path, old, old); err != nil {
					t.Fatal(err)
				}
			}
			recent := e.clock.now().Add(-time.Duration(tc.days-1) * 24 * time.Hour)
			if err := os.Chtimes(photo.Path, recent, recent); err != nil {
				t.Fatal(err)
			}
			// A successful write prunes by file mtime, not the ancient message timestamp.
			if _, err := e.svc.Media(context.Background(), anna, "file"); err != nil {
				t.Fatal(err)
			}
			for _, path := range expired {
				awaitMedia(t, func() bool { _, err := os.Stat(path); return os.IsNotExist(err) })
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("expired file retained: %s %v", path, err)
				}
			}
			if _, err := os.Stat(photo.Path); err != nil {
				t.Fatalf("recent file removed: %v", err)
			}
			row, err := e.st.WaGetMessage(anna, "outgoing")
			if err != nil || row.Media.Transcript != "saved speech" || row.MediaFile == "" {
				t.Fatalf("retention lost transcript/tombstone: %+v %v", row, err)
			}
			e.svc.Close()
			// Restart must not automatically refill expired entries, even audio whose
			// transcription had failed and therefore still appears in the work query.
			next := watest.Linked()
			mediaFixtures(t, next)
			on := true
			svc := whatsapp.New(whatsapp.Options{Store: e.st, Client: next, MediaDir: e.media, AutoMedia: true,
				DiskFree: func(string) (uint64, error) { return 20 << 30, nil },
				Config: func() *config.WhatsAppConfig {
					return &config.WhatsAppConfig{Enabled: &on, MediaRetentionDays: tc.configured}
				},
				Transcribe: func(context.Context, string) (string, error) { return "new speech", nil }})
			t.Cleanup(svc.Close)
			if err := svc.Start(); err != nil {
				t.Fatal(err)
			}
			awaitMedia(t, func() bool { r, err := e.st.WaGetMessage(anna, "video"); return err == nil && r.MediaFile != "" })
			for _, path := range expired {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("automatic refill: %s %v", path, err)
				}
			}
			list, err := svc.Messages(anna, "", 20)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range list {
				if m.ID == "incoming" && m.Media.Downloaded {
					t.Fatal("expired file shown as downloaded")
				}
			}
			// Explicit owner download can refresh an expired attachment.
			file, err := svc.Media(context.Background(), anna, "incoming")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(file.Path); err != nil {
				t.Fatal(err)
			}
		})
	}
}
