package api_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/api"
	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	tc "github.com/blackcode-ch/tmx-go/internal/client"
	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
)

// The first-run setup is offered until the owner finishes or closes it: config.json
// "onboarded_at" holds when (the daemon's clock, whatever number the UI sends), 0 offers it again,
// and only the owner reads or sets it.
func TestConfigOnboardedAt(t *testing.T) {
	d, owner, agent, _ := configAndUpdateDaemon(t)
	saved := func() int64 {
		t.Helper()
		c, err := config.Load(config.Paths{Home: d.Home})
		if err != nil {
			t.Fatal(err)
		}
		return c.OnboardedAt
	}
	var v api.ConfigView
	if s, _ := call(t, owner, "GET", "/api/config", nil, &v); s != 0 || v.OnboardedAt == nil || *v.OnboardedAt != 0 {
		t.Fatalf("a new machine: %d %+v", s, v.OnboardedAt)
	}
	before := time.Now().UnixMilli()
	if s, _ := call(t, owner, "PUT", "/api/config", map[string]int64{"onboarded_at": 1}, &v); s != 0 || v.OnboardedAt == nil || *v.OnboardedAt < before {
		t.Fatalf("done: %d %+v", s, v.OnboardedAt)
	}
	at := saved()
	if at < before || at != *v.OnboardedAt {
		t.Fatalf("saved %d, answered %d", at, *v.OnboardedAt)
	}
	// Done again (another window closing it) keeps the first time; another field leaves it alone.
	if s, _ := call(t, owner, "PUT", "/api/config", map[string]int64{"onboarded_at": 5}, &v); s != 0 || *v.OnboardedAt != at {
		t.Fatalf("done twice: %d %+v", s, v.OnboardedAt)
	}
	if s, _ := call(t, owner, "PUT", "/api/config", map[string]string{"head_harness": "codex"}, &v); s != 0 || saved() != at || v.OnboardedAt == nil || *v.OnboardedAt != at {
		t.Fatalf("another field: %d %d %+v", s, saved(), v.OnboardedAt)
	}
	if s, _ := call(t, agent, "PUT", "/api/config", map[string]int64{"onboarded_at": 0}, nil); s != http.StatusForbidden || saved() != at {
		t.Fatalf("an agent: %d %d", s, saved())
	}
	if s, _ := call(t, owner, "PUT", "/api/config", map[string]int64{"onboarded_at": 0}, &v); s != 0 || *v.OnboardedAt != 0 || saved() != 0 {
		t.Fatalf("offered again: %d %+v", s, v.OnboardedAt)
	}
}

// A machine already in use is not offered the setup after the update that brings it (on purpose
// unlike Mustneer's tmx-go): at start the daemon marks it shown when the fleet has a project or
// an agent besides the head and its General. A new install is left alone, start after start,
// and a time already stored is never touched.
func TestMarkOnboardedIfInUse(t *testing.T) {
	saved := func(d *apitest.Daemon) int64 {
		t.Helper()
		c, err := config.Load(config.Paths{Home: d.Home})
		if err != nil {
			t.Fatal(err)
		}
		return c.OnboardedAt
	}

	fresh := apitest.New(t)
	for i := 0; i < 2; i++ { // a new install, and the same one restarted unused
		if marked, err := api.MarkOnboardedIfInUse(fresh.Fleet); err != nil || marked || saved(fresh) != 0 {
			t.Fatalf("a new install, start %d: marked %v, saved %d, err %v", i+1, marked, saved(fresh), err)
		}
	}

	used := apitest.New(t)
	if _, err := used.Fleet.CreateProject(fleet.OwnerCaller, fleet.ProjectRequest{Name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UnixMilli()
	marked, err := api.MarkOnboardedIfInUse(used.Fleet)
	at := saved(used)
	if err != nil || !marked || at < before {
		t.Fatalf("a machine in use: marked %v, saved %d, err %v", marked, at, err)
	}
	if marked, err := api.MarkOnboardedIfInUse(used.Fleet); err != nil || marked || saved(used) != at {
		t.Fatalf("the next start: marked %v, saved %d (was %d), err %v", marked, saved(used), at, err)
	}
	// the API then says "shown", so the UI does not offer it; 0 still offers it again by hand
	owner := tc.New(used.URL, used.OwnerToken)
	var v api.ConfigView
	if s, _ := call(t, owner, "GET", "/api/config", nil, &v); s != 0 || v.OnboardedAt == nil || *v.OnboardedAt != at {
		t.Fatalf("GET after the mark: %d %+v", s, v.OnboardedAt)
	}
}

// The setup's phone step shows the HTTPS address a phone opens: GET /api/config lists the exact
// trusted hosts this daemon answers on (none by default: local only). It is read only; a PUT
// that names hosts changes neither the answer nor config.json.
func TestConfigTrustedHostsReadOnly(t *testing.T) {
	d, owner, _, _ := configAndUpdateDaemon(t)
	var v api.ConfigView
	if s, _ := call(t, owner, "GET", "/api/config", nil, &v); s != 0 || len(v.TrustedHosts) != 0 {
		t.Fatalf("local only: %d %v", s, v.TrustedHosts)
	}

	srv := httptest.NewServer(api.New(api.Options{Fleet: d.Fleet, OwnerToken: d.OwnerToken,
		TrustedHosts: []string{"Trial.Example.ts.net:443", "not a host", "box.example.net:8443"}}).Handler())
	t.Cleanup(srv.Close)
	remote := tc.New(srv.URL, d.OwnerToken)
	want := []string{"trial.example.ts.net", "box.example.net:8443"}
	v = api.ConfigView{}
	if s, _ := call(t, remote, "GET", "/api/config", nil, &v); s != 0 || !slices.Equal(v.TrustedHosts, want) {
		t.Fatalf("trusted hosts: %d %v", s, v.TrustedHosts)
	}
	v = api.ConfigView{}
	if s, _ := call(t, remote, "PUT", "/api/config", map[string][]string{"trusted_hosts": {"evil.example.com"}}, &v); s != 0 || !slices.Equal(v.TrustedHosts, want) {
		t.Fatalf("a PUT naming hosts: %d %v", s, v.TrustedHosts)
	}
	c, err := config.Load(config.Paths{Home: d.Home})
	if err != nil || len(c.API.TrustedHosts) != 0 {
		t.Fatalf("config.json after the PUT: %v %v", c.API.TrustedHosts, err)
	}
}
