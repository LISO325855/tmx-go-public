package api_test

import (
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/resources"
)

func TestWatchBoardIsTheOwners(t *testing.T) {
	d := apitest.New(t)
	owner := client{t: t, base: d.URL, token: d.OwnerToken}
	var v model.WatchView
	if s := owner.do("GET", "/api/watch", nil, &v); s != 200 || !v.Enabled || v.DigestEvery != 30 || v.Rows == nil || v.Notes == nil {
		t.Fatalf("%d %+v", s, v)
	}
	// The real guard event type reaches the board through the bus (no import between the two
	// packages: the fleet finds its WatchItems method).
	d.Fleet.Publish(model.BusEvent{Type: model.EvResourcesGuard, Data: resources.GuardEvent{Kind: "disk-reserve-low", Volume: "C:",
		Detail: "C: has 3.0 GB free; the reserve is 5.0 GB"}})
	var low *model.WatchRow
	if s := owner.do("GET", "/api/watch", nil, &v); s != 200 {
		t.Fatal(s)
	}
	for i := range v.Rows {
		if v.Rows[i].Key == "disk:C:" {
			low = &v.Rows[i]
		}
	}
	if low == nil || !low.Urgent || low.Status != "low" || low.Text != "disk is below its free-space reserve: C: has 3.0 GB free; the reserve is 5.0 GB" {
		t.Fatalf("%+v", v.Rows)
	}
	if s := (client{t: t, base: d.URL, token: "not-a-token"}).do("GET", "/api/watch", nil, nil); s != 401 && s != 403 {
		t.Fatalf("a stranger read the watch board: %d", s)
	}
}

func TestRegistryRoutes(t *testing.T) {
	d := apitest.New(t)
	owner := client{t: t, base: d.URL, token: d.OwnerToken}
	var out struct {
		Entries []model.MachineProcess `json:"entries"`
		Orphans int                    `json:"orphans"`
	}
	if s := owner.do("GET", "/api/registry", nil, &out); s != 200 || out.Entries == nil || out.Orphans != 0 {
		t.Fatalf("%d %+v", s, out)
	}
	for _, e := range out.Entries { // whatever this daemon lists, an entry is complete
		if e.ID == "" || e.Kind == "" || e.State == "" || e.Actions == nil {
			t.Fatalf("incomplete entry: %+v", e)
		}
	}
	if s := owner.do("GET", "/api/registry?project=no-such-project", nil, &out); s != 200 || len(out.Entries) != 0 {
		t.Fatalf("project filter: %d %+v", s, out.Entries)
	}
	for _, id := range []string{"pid:999999:1", "tool:nope", "nonsense", ""} {
		if s := owner.do("POST", "/api/registry/stop", map[string]string{"id": id}, nil); s != 404 {
			t.Fatalf("stop %q: %d", id, s)
		}
		if s := owner.do("POST", "/api/registry/restart", map[string]string{"id": id}, nil); s != 404 {
			t.Fatalf("restart %q: %d", id, s)
		}
	}
	stranger := client{t: t, base: d.URL, token: "not-a-token"}
	for _, call := range [][2]string{{"GET", "/api/registry"}, {"POST", "/api/registry/stop"}, {"POST", "/api/registry/restart"}} {
		if s := stranger.do(call[0], call[1], map[string]string{"id": "pid:1:1"}, nil); s != 401 && s != 403 {
			t.Fatalf("a stranger: %s %s = %d", call[0], call[1], s)
		}
	}
}
