package api_test

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/api"
	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	tc "github.com/blackcode-ch/tmx-go/internal/client"
	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/rules"
	"github.com/blackcode-ch/tmx-go/internal/rules/rulestest"
)

// rulesDaemon is a daemon whose rules come from a throwaway source; the settings saved through
// the API (config.json "rules") are laid over the source's own.
func rulesDaemon(t *testing.T) (*apitest.Daemon, *rulestest.Repo, *rules.Service) {
	t.Helper()
	src, _ := rulestest.NewWith(t, rulestest.Files())
	// The service refreshes in its own goroutine while apitest.New is still starting the fleet.
	var daemon atomic.Pointer[apitest.Daemon]
	svc := rules.New(rules.Options{Dir: t.TempDir(), Settings: func() config.RulesConfig {
		base := src.Settings()
		d := daemon.Load()
		if d == nil {
			return base
		}
		c, err := config.Load(d.Fleet.Paths())
		if err != nil || c.Rules == nil {
			return base
		}
		r := c.EffectiveRules()
		if r.Source == "" && !c.Rules.Off {
			r.Source = base.Source
		}
		return r
	}})
	t.Cleanup(svc.Close)
	d := apitest.New(t, func(o *fleet.Options) { o.Rules = svc })
	daemon.Store(d)
	return d, src, svc
}

func TestRulesAPI(t *testing.T) {
	d, src, _ := rulesDaemon(t)
	owner := tc.New(d.URL, d.OwnerToken)
	head := tc.New(d.URL, d.AgentToken(t, "head"))
	proj, err := d.Fleet.CreateProject(fleet.OwnerCaller, fleet.ProjectRequest{Name: "Shop"})
	if err != nil {
		t.Fatal(err)
	}
	leadAgent, err := d.Fleet.Agent(proj.LeadID)
	if err != nil {
		t.Fatal(err)
	}
	lead := tc.New(d.URL, d.AgentToken(t, leadAgent.Name))

	var v api.RulesView
	// The head's start already asked for a look in the background (rules.Kick, never waited
	// for); it may or may not have finished by now.
	if s, _ := call(t, owner, "GET", "/api/rules", nil, &v); s != 0 || !v.Enabled || (!v.Loaded && len(v.Docs) != 0) {
		t.Fatalf("before the first look: %d %+v", s, v)
	}
	// Only the owner and the head start a look at the source.
	if s, _ := call(t, lead, "POST", "/api/rules/refresh", nil, nil); s != http.StatusForbidden {
		t.Fatalf("a lead started a look: %d", s)
	}
	if s, _ := call(t, head, "POST", "/api/rules/refresh", nil, &v); s != 0 || !v.Loaded || v.Head == nil || v.Head.Commit != src.Head() || len(v.Docs) != 5 || v.Problem != "" {
		t.Fatalf("after the look: %d %+v", s, v)
	}
	if v.Signature != "" || len(v.Signers) != 0 || v.Manifest == nil || len(v.Manifest.All.Inline) != 1 {
		t.Errorf("view: %+v", v)
	}
	// Agents that were started before the rules were loaded run without them: shown as behind.
	// The head always is; the lead is when its start came before that background look ended.
	headBehind, leadBehind := false, false
	for _, a := range v.Agents {
		switch a.Name {
		case "head":
			headBehind = a.Behind
		case leadAgent.Name:
			leadBehind = a.Behind
		}
	}
	if len(v.Agents) != 2 || !headBehind {
		t.Errorf("agents: %+v", v.Agents)
	}

	var doc api.RulesDoc
	if s, _ := call(t, lead, "GET", "/api/rules/doc?name=apps/tickets.md", nil, &doc); s != 0 || doc.Title != "Ticket app" || doc.Meta["state"] != "works" || !strings.Contains(doc.Body, "Open a ticket") {
		t.Fatalf("a document: %d %+v", s, doc)
	}
	if s, _ := call(t, lead, "GET", "/api/rules/doc?name=rules/none", nil, nil); s != http.StatusNotFound {
		t.Errorf("a document that does not exist: %d", s)
	}
	var apps []api.RulesDoc
	if s, _ := call(t, owner, "GET", "/api/rules/apps", nil, &apps); s != 0 || len(apps) != 1 || apps[0].Name != "apps/tickets" || apps[0].Summary == "" {
		t.Fatalf("apps: %d %+v", s, apps)
	}

	// rules_read's route: an agent gets its own view.
	var read struct {
		Text string `json:"text"`
	}
	// The "built before" note is there exactly when the lead's start came before the look.
	if s, _ := call(t, lead, "POST", "/api/rules/read", map[string]any{}, &read); s != 0 || !strings.HasPrefix(read.Text, rules.BeginLabel) ||
		strings.Contains(read.Text, "Your instructions were built before fleet rules were loaded") != leadBehind || !strings.Contains(read.Text, "- rules/core: Core") {
		t.Fatalf("read: %d\n%s", s, read.Text)
	}
	if s, _ := call(t, lead, "POST", "/api/rules/read", map[string]any{"name": "nope"}, nil); s != http.StatusNotFound {
		t.Errorf("reading a document that does not exist: %d", s)
	}
}

// The owner's settings: owner only; a bad value is refused before anyone is asked; the source
// and the signing keys are asked for even with the gate off and saved only once confirmed.
func TestRulesConfig(t *testing.T) {
	d, src, svc := rulesDaemon(t)
	owner := tc.New(d.URL, d.OwnerToken)
	head := tc.New(d.URL, d.AgentToken(t, "head"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	first := src.Head()

	if s, _ := call(t, head, "PUT", "/api/rules/config", map[string]any{"off": true}, nil); s != http.StatusForbidden {
		t.Fatalf("an agent changed the rules settings: %d", s)
	}
	_, pinned := rulestest.Key(t, "head")
	for name, bad := range map[string]any{
		"a short pin":        map[string]any{"pin": "abc123"},
		"a source that runs": map[string]any{"source": "ext::sh -c x"},
		"a token in the url": map[string]any{"source": "https://x:secret@github.com/e/r.git"},
		"a bad branch":       map[string]any{"branch": "a..b"},
		"a bad key":          map[string]any{"signers": []any{map[string]string{"id": "head", "key": "nope"}}},
		"a long interval":    map[string]any{"fetch_minutes": 100000},
	} {
		if s, _ := call(t, owner, "PUT", "/api/rules/config", bad, nil); s != http.StatusBadRequest {
			t.Errorf("%s: %d", name, s)
		}
	}
	if n := len(d.GateAuth.Prompts()); n != 0 {
		t.Fatalf("a bad value asked the owner (%d prompts)", n)
	}

	// Pinning a key: the gate is off, and it asks anyway.
	body := map[string]any{"signers": []any{map[string]string{"id": pinned.ID, "key": pinned.Key}}}
	s, conf := call(t, owner, "PUT", "/api/rules/config", body, nil)
	if s != http.StatusPreconditionRequired || !strings.Contains(conf.Reason, "pin the keys that sign the fleet rules: head SHA256:") {
		t.Fatalf("without proof: %d %q", s, conf.Reason)
	}
	if c, _ := config.Load(d.Fleet.Paths()); c.Rules != nil {
		t.Fatalf("saved before the owner confirmed: %+v", c.Rules)
	}
	var v api.RulesView
	if s, _ := call(t, confirmed(t, owner, conf), "PUT", "/api/rules/config", body, &v); s != 0 {
		t.Fatalf("with proof: %d", s)
	}
	if c, _ := config.Load(d.Fleet.Paths()); c.Rules == nil || len(c.Rules.Signers) != 1 {
		t.Fatalf("not saved: %+v", c.Rules)
	}
	// The commit in force was taken before the key was pinned and is not signed: with a key
	// pinned it is in force no longer, and the source's unsigned tip is refused.
	if len(v.Signers) != 1 || !strings.HasPrefix(v.Signers[0].Fingerprint, "SHA256:") || v.Loaded || v.Head != nil ||
		v.Rejected == nil || v.Rejected.Commit != first || !strings.Contains(v.Problem, "not signed") {
		t.Fatalf("view after pinning: %+v", v)
	}
	if p := svc.Prompt(rules.PromptRequest{Profile: "worker", Level: "worker"}); p.Text != "" {
		t.Errorf("an unsigned commit is still put into instructions after a key was pinned:\n%s", p.Text)
	}
	src.Commit("unsigned", map[string]string{"rules/core.md": "# Core\n\nUnsigned.\n"})
	if s, _ := call(t, owner, "POST", "/api/rules/refresh", nil, &v); s != 0 || v.Loaded || v.Rejected == nil || v.Rejected.Commit != src.Head() || !strings.Contains(v.Problem, "not signed") {
		t.Fatalf("an unsigned commit after pinning: %d %+v", s, v)
	}

	// Turning the source off is a setting like others: with the gate off it just happens.
	if s, _ := call(t, owner, "PUT", "/api/rules/config", map[string]any{"off": true}, &v); s != 0 || v.Enabled || v.Loaded {
		t.Fatalf("off: %d %+v", s, v)
	}
	if svc.Prompt(rules.PromptRequest{Profile: "worker", Level: "worker"}).Text != "" {
		t.Error("rules are still put into instructions after the owner turned the source off")
	}
}
