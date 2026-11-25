package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/client"
	"github.com/blackcode-ch/tmx-go/internal/update"
)

func TestWaitUpdatePreservesSettingsDuringRestart(t *testing.T) {
	for _, pollFirst := range []bool{false, true} {
		name := "restart before first poll"
		if pollFirst {
			name = "restart after successful poll"
		}
		t.Run(name, func(t *testing.T) {
			initial := update.Status{State: update.StateAvailable, Current: "308", Latest: "321", Channel: "latest", Auto: false}
			want := initial
			if pollFirst {
				want.Channel, want.Auto = "stable", true
			}
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if pollFirst && calls == 1 {
					status := want
					status.State = update.StateDownloading
					_ = json.NewEncoder(w).Encode(status)
					return
				}
				// A failed poll must not discard previously received settings.
				http.Error(w, "daemon restarting", http.StatusServiceUnavailable)
			}))
			defer srv.Close()
			got, err := waitUpdate(context.Background(), client.New(srv.URL, ""), initial)
			want.State = update.StateInstalling
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v, %v; want %+v", got, err, want)
			}
			auto := "off"
			if want.Auto {
				auto = "on"
			}
			if text := describeUpdate(got); !strings.Contains(text, "channel "+want.Channel) || !strings.Contains(text, "auto "+auto) {
				t.Fatalf("incorrect displayed settings: %s", text)
			}
		})
	}
}
