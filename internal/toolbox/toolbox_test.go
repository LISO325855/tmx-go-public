package toolbox_test

import (
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/toolbox"
	"github.com/blackcode-ch/tmx-go/internal/toolbox/toolboxtest"
)

func find(t *testing.T, list []toolbox.Resource, id string) toolbox.Resource {
	t.Helper()
	r, ok := toolbox.Find(list, id)
	if !ok {
		t.Fatalf("no resource %q", id)
	}
	return r
}

func TestSeedIsACopy(t *testing.T) {
	list := toolboxtest.Install(t)
	if len(list) != toolboxtest.Lines {
		t.Fatalf("%d lines, want %d", len(list), toolboxtest.Lines)
	}
	for _, r := range list {
		if r.Source != toolbox.SourceDeclared {
			t.Errorf("%s: source %q, want declared", r.ID, r.Source)
		}
		if r.Actions == nil {
			t.Errorf("%s: actions must be a list, not null", r.ID)
		}
	}
	list[0].Name = "changed"
	list[0].Actions[0].Name = "changed"
	again := toolbox.Seed()
	if again[0].Name == "changed" || again[0].Actions[0].Name == "changed" {
		t.Error("a caller changed the declared list")
	}
}

func TestSetSeedRefusesABadList(t *testing.T) {
	before := toolboxtest.Install(t)
	for name, js := range map[string]string{
		"not JSON":  `{`,
		"bad kind":  `[{"id":"x-1","name":"X","kind":"gadget","summary":"s","identity":{"who":"fleet"},"state":"works","rule":{"text":"r"}}]`,
		"a number":  `[{"id":"x-1","name":"X +41791234567","kind":"tool","summary":"s","identity":{"who":"fleet"},"state":"works","rule":{"text":"r"}}]`,
		"an orphan": `[{"id":"x-1","name":"X","kind":"tool","summary":"s","identity":{"who":"fleet"},"state":"works","rule":{"text":"r"},"parent":"nowhere"}]`,
	} {
		if err := toolbox.SetSeed([]byte(js)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if len(toolbox.Seed()) != len(before) {
		t.Error("a refused list replaced the declared one")
	}
	if err := toolbox.SetSeed([]byte(`[]`)); err != nil || len(toolbox.Seed()) != 0 {
		t.Errorf("an empty list is a valid list: %v", err)
	}
}

func TestSecretShape(t *testing.T) {
	prefixed := strings.Join([]string{"sk", strings.Repeat("x", 20)}, "-")
	longWord := strings.Repeat("xY8-", 10)
	bad := map[string]string{
		"write to someone@example.com":                        "a mail address",
		"call +41 79 123 45 67":                               "a phone number",
		"0791234567":                                          "a phone number",
		"on 2026-11-17 call 0791234567":                       "a phone number",
		"password: hunter2":                                   "a password or key",
		strings.Join([]string{"api", "key", "=", "abc"}, " "): "a password or key",
		prefixed: "a token",
		longWord: "a token",
	}
	for s, want := range bad {
		if got := toolbox.SecretShape(s); got != want {
			t.Errorf("SecretShape(%q) = %q, want %q", s, got, want)
		}
	}
	for _, s := range []string{
		"The fleet's alias agents@; it delivers to the research@ mailbox",
		"sender@box-b", "tested 17 Nov 16:50", "11 of 11 calls", "2026-11-17", "2026-11-17T18:20:25Z", "cap of 30 sends per hour",
		"Companion link token", "Only the name of a secret is ever listed.", "b/sign, b/sales, b/billing, b/issues",
	} {
		if got := toolbox.SecretShape(s); got != "" {
			t.Errorf("SecretShape(%q) = %q, want none", s, got)
		}
	}
}

func TestValidateRefuses(t *testing.T) {
	ok := toolbox.Resource{ID: "x-1", Name: "X", Kind: toolbox.KindTool, Summary: "s", Identity: toolbox.Identity{Who: "fleet"},
		State: toolbox.StateWorks, Rule: toolbox.Rule{Text: "r"}}
	if err := toolbox.Validate(ok); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*toolbox.Resource){
		"id":       func(r *toolbox.Resource) { r.ID = "Bad Id" },
		"kind":     func(r *toolbox.Resource) { r.Kind = "gadget" },
		"state":    func(r *toolbox.Resource) { r.State = "fine" },
		"rule":     func(r *toolbox.Resource) { r.Rule.Text = " " },
		"identity": func(r *toolbox.Resource) { r.Identity.Who = "me" },
		"address":  func(r *toolbox.Resource) { r.Identity.Label = "someone@example.com" },
		"number":   func(r *toolbox.Resource) { r.Summary = "text +41791234567" },
		"secret":   func(r *toolbox.Resource) { r.Secrets = []toolbox.Secret{{Name: "token: abc123"}} },
		"holder":   func(r *toolbox.Resource) { r.HolderNote = "password=hunter2" },
		"route": func(r *toolbox.Resource) {
			r.Actions = []toolbox.Action{{Name: "send", Type: toolbox.ActionSendFleet, Route: toolbox.Route{Kind: toolbox.RouteTool}}}
		},
		"mail route": func(r *toolbox.Resource) {
			r.Actions = []toolbox.Action{{Name: "send", Type: toolbox.ActionSendFleet, Route: toolbox.Route{Kind: toolbox.RouteMail}}}
		},
		"action type": func(r *toolbox.Resource) {
			r.Actions = []toolbox.Action{{Name: "send", Type: "shout", Route: toolbox.Route{Kind: toolbox.RouteNone}}}
		},
		"live": func(r *toolbox.Resource) { r.Live = "magic" },
		"page": func(r *toolbox.Resource) { r.Page = "https://example.org" },
	}
	for name, change := range cases {
		r := ok
		change(&r)
		if err := toolbox.Validate(r); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := toolbox.ValidateList([]toolbox.Resource{ok, ok}); err == nil {
		t.Error("a duplicate id was accepted")
	}
}

func TestFilterSortFind(t *testing.T) {
	list := toolboxtest.Install(t)
	if got := toolbox.Filter(list, toolbox.KindDevice, ""); len(got) != 2 {
		t.Errorf("%d devices, want 2", len(got))
	}
	if n := len(toolbox.Filter(list, "", toolbox.StatePlanned)); n != 2 {
		t.Errorf("%d planned lines, want 2", n)
	}
	if n := len(toolbox.Filter(list, toolbox.KindCommunication, toolbox.StateWorks)); n != 2 {
		t.Errorf("%d working channels, want 2", n)
	}
	if got := toolbox.Filter(list, "", toolbox.HealthDown); got == nil || len(got) != 0 {
		t.Errorf("down in a declared list: %v (want an empty list, not null)", got)
	}
	if err := toolbox.CheckFilter("gadget", ""); err == nil {
		t.Error("unknown kind accepted")
	}
	if err := toolbox.CheckFilter("", "broken"); err == nil {
		t.Error("unknown state accepted")
	}
	if err := toolbox.CheckFilter(toolbox.KindTool, toolbox.HealthDown); err != nil {
		t.Error(err)
	}
	if err := toolbox.CheckFilter("", toolbox.StateNotConnected); err != nil {
		t.Error(err)
	}
	// Not connected yet is a state of its own; it does not ask for attention as "not verified" does.
	nc := toolbox.Resource{ID: "x-1", Name: "X", Kind: toolbox.KindStorage, Summary: "s", Identity: toolbox.Identity{Who: "owner"},
		State: toolbox.StateNotConnected, Rule: toolbox.Rule{Text: "r"}}
	if err := toolbox.Validate(nc); err != nil || toolbox.NeedsAttention(nc) {
		t.Errorf("not connected: %v, attention %v", err, toolbox.NeedsAttention(nc))
	}
	if _, ok := toolbox.Find(list, "nothing"); ok {
		t.Error("found a resource that is not there")
	}

	toolbox.Sort(list)
	var ids []string
	for _, r := range list {
		ids = append(ids, r.ID)
	}
	// By kind in page order; inside a kind the lines needing attention first, then as declared.
	want := "chat-node,chat-browser,fleet-phone,voice,network,assistant,connector,ai-accounts,sim-stick,virtual-phone,disks"
	if got := strings.Join(ids, ","); got != want {
		t.Errorf("order:\n got %s\nwant %s", got, want)
	}
}
