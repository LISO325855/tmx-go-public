package toolbox_test

import (
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/toolbox"
	"github.com/blackcode-ch/tmx-go/internal/toolbox/toolboxtest"
)

const now = int64(1_800_000_000_000)

func TestApplyLeavesOtherNodesDeclared(t *testing.T) {
	list := toolboxtest.Install(t)
	out := toolbox.Apply(list, toolbox.Live{Machine: "elsewhere", Now: now,
		WhatsApp: &toolbox.WhatsAppLive{Enabled: false}, Accounts: &toolbox.AccountsLive{}, Companion: &toolbox.CompanionLive{}})
	for _, id := range []string{"chat-node", "ai-accounts", "assistant"} {
		r := find(t, out, id)
		if r.Source != toolbox.SourceDeclared || r.Health != nil {
			t.Errorf("%s: a node that does not run it must show it as declared, got source %q health %+v", id, r.Source, r.Health)
		}
	}
	// A node with no service at all (nil) on the measuring machine: declared too.
	out = toolbox.Apply(list, toolbox.Live{Machine: toolboxtest.Node, Now: now})
	if r := find(t, out, "chat-node"); r.Source != toolbox.SourceDeclared || r.Health != nil {
		t.Errorf("no service: source %q health %+v", r.Source, r.Health)
	}
}

func TestApplyWhatsApp(t *testing.T) {
	cases := []struct {
		w            toolbox.WhatsAppLive
		state, level string
	}{
		{toolbox.WhatsAppLive{Enabled: true, State: "connected"}, toolbox.StateWorks, toolbox.HealthGood},
		{toolbox.WhatsAppLive{Enabled: true, State: "connecting"}, toolbox.StateWorks, toolbox.HealthWarning},
		{toolbox.WhatsAppLive{Enabled: true, State: "linking"}, toolbox.StatePartly, toolbox.HealthWarning},
		{toolbox.WhatsAppLive{Enabled: true, State: "logged_out"}, toolbox.StatePartly, toolbox.HealthDown},
		{toolbox.WhatsAppLive{Enabled: true, State: "unlinked"}, toolbox.StatePartly, toolbox.HealthDown},
		{toolbox.WhatsAppLive{Enabled: false, State: "connected"}, toolbox.StatePartly, toolbox.HealthDown},
		{toolbox.WhatsAppLive{Enabled: true, State: "error", Error: "client outdated"}, toolbox.StatePartly, toolbox.HealthDown},
	}
	list := toolboxtest.Install(t)
	for _, c := range cases {
		w := c.w
		// The machine name matches whatever its case.
		r := find(t, toolbox.Apply(list, toolbox.Live{Machine: "Node-A", Now: now, WhatsApp: &w}), "chat-node")
		if r.Source != toolbox.SourceLive || r.State != c.state || r.Health == nil || r.Health.Level != c.level || r.Health.CheckedAt != now {
			t.Errorf("%+v: source %q state %q health %+v", c.w, r.Source, r.State, r.Health)
		}
		if r.VerifiedBy != "Node-A" || r.VerifiedAt != "" {
			t.Errorf("verified by %q at %q", r.VerifiedBy, r.VerifiedAt)
		}
	}
	w := toolbox.WhatsAppLive{Enabled: true, State: "error", Error: "client outdated"}
	r := find(t, toolbox.Apply(list, toolbox.Live{Machine: toolboxtest.Node, Now: now, WhatsApp: &w}), "chat-node")
	if !strings.Contains(r.Health.Reason, "client outdated") {
		t.Errorf("the reason lost the error: %q", r.Health.Reason)
	}
	// An error text that holds an address or a number is not passed on.
	w = toolbox.WhatsAppLive{Enabled: true, State: "error", Error: "banned +41791234567"}
	r = find(t, toolbox.Apply(list, toolbox.Live{Machine: toolboxtest.Node, Now: now, WhatsApp: &w}), "chat-node")
	if strings.Contains(r.Health.Reason, "4179") {
		t.Errorf("the number leaked into the reason: %q", r.Health.Reason)
	}
}

func TestApplyAccounts(t *testing.T) {
	list := toolboxtest.Install(t)
	acc := &toolbox.AccountsLive{Accounts: []toolbox.AccountLive{
		{Label: "Claude", Installed: true, Connected: true, HasLimit: true, LimitLabel: "5-hour limit", UsedPercent: 40},
		{Label: "Claude (work)", Installed: true, Connected: true, HasLimit: true, LimitLabel: "weekly limit", UsedPercent: 93},
		{Label: "Codex", Installed: true, Connected: false, HasLimit: true, LimitLabel: "weekly limit", UsedPercent: 99},
		{Label: "Gemini", Installed: false},
	}}
	r := find(t, toolbox.Apply(list, toolbox.Live{Machine: toolboxtest.Node, Now: now, Accounts: acc}), "ai-accounts")
	if r.Source != toolbox.SourceLive || r.State != toolbox.StateWorks || r.Health.Level != toolbox.HealthWarning {
		t.Fatalf("source %q state %q health %+v", r.Source, r.State, r.Health)
	}
	if !strings.HasPrefix(r.Health.Reason, "2 of 3 accounts signed in.") {
		t.Errorf("reason %q", r.Health.Reason)
	}
	// The fullest limit of a signed-in account; a signed-out one does not count.
	if r.Meter == nil || r.Meter.UsedPercent != 93 || r.Meter.Label != "Claude (work): weekly limit" || !strings.HasPrefix(r.Meter.Text, "7% left") {
		t.Errorf("meter %+v", r.Meter)
	}
	if r.Rule.Text != "Hard work on the strong model, easy work on the cheap one." {
		t.Error("the rule is declared and must stay")
	}

	calm := &toolbox.AccountsLive{Accounts: []toolbox.AccountLive{{Label: "Codex", Installed: true, Connected: true, HasLimit: true, LimitLabel: "weekly limit", UsedPercent: 12}}}
	r = find(t, toolbox.Apply(list, toolbox.Live{Machine: toolboxtest.Node, Now: now, Accounts: calm}), "ai-accounts")
	if r.Health.Level != toolbox.HealthGood || r.Health.Reason != "1 of 1 accounts signed in." {
		t.Errorf("calm: %+v", r.Health)
	}

	none := &toolbox.AccountsLive{Accounts: []toolbox.AccountLive{{Label: "Codex", Installed: true}}}
	r = find(t, toolbox.Apply(list, toolbox.Live{Machine: toolboxtest.Node, Now: now, Accounts: none}), "ai-accounts")
	if r.Health.Level != toolbox.HealthDown || r.Meter != nil || !toolbox.NeedsAttention(r) {
		t.Errorf("no account signed in: %+v meter %+v", r.Health, r.Meter)
	}
	r = find(t, toolbox.Apply(list, toolbox.Live{Machine: toolboxtest.Node, Now: now, Accounts: &toolbox.AccountsLive{}}), "ai-accounts")
	if r.Health.Level != toolbox.HealthDown || !strings.Contains(r.Health.Reason, "No AI harness") {
		t.Errorf("nothing installed: %+v", r.Health)
	}
}

func TestApplyCompanion(t *testing.T) {
	list := toolboxtest.Install(t)
	r := find(t, toolbox.Apply(list, toolbox.Live{Machine: toolboxtest.Node, Now: now,
		Companion: &toolbox.CompanionLive{Connected: true, LinkToken: false, LinkUsedAt: now - 5*60_000}}), "assistant")
	// The declared state stays; the node adds its own link.
	if r.Source != toolbox.SourceLive || r.State != toolbox.StatePartly || r.VerifiedBy != "assistant-lead@box-b" ||
		r.Health.Level != toolbox.HealthGood || !strings.Contains(r.Health.Reason, "5 minutes ago") {
		t.Fatalf("source %q state %q by %q health %+v", r.Source, r.State, r.VerifiedBy, r.Health)
	}
	got := map[string]bool{}
	for _, s := range r.Secrets {
		if s.Stored == nil {
			t.Fatalf("%s: stored not filled", s.Name)
		}
		got[s.Name] = *s.Stored
	}
	if !got["Companion agent key"] || got["Companion link token"] {
		t.Errorf("secrets %+v", got)
	}
	r = find(t, toolbox.Apply(list, toolbox.Live{Machine: toolboxtest.Node, Now: now, Companion: &toolbox.CompanionLive{}}), "assistant")
	if r.Health.Level != toolbox.HealthWarning {
		t.Errorf("no key: %+v", r.Health)
	}
}

// Apply must not write through to the list it was given (the secrets slice is shared
// memory unless it is copied).
func TestApplyDoesNotChangeItsInput(t *testing.T) {
	list := toolboxtest.Install(t)
	toolbox.Apply(list, toolbox.Live{Machine: toolboxtest.Node, Now: now, Companion: &toolbox.CompanionLive{Connected: true, LinkToken: true},
		WhatsApp: &toolbox.WhatsAppLive{Enabled: true, State: "connected"}})
	for _, s := range find(t, list, "assistant").Secrets {
		if s.Stored != nil {
			t.Errorf("the declared record changed: %s", s.Name)
		}
	}
	if r := find(t, list, "chat-node"); r.Source != toolbox.SourceDeclared || r.Health != nil {
		t.Errorf("the declared record changed: %+v", r)
	}
}

func TestApplyHolder(t *testing.T) {
	list := toolboxtest.Install(t)
	l := toolbox.Live{Machine: toolboxtest.Node, Now: now, Peers: map[string]toolbox.PeerLive{
		"Box-B": {Online: true, Agents: map[string]bool{"sender": true, "assistant-lead": false}},
		"box-c": {Online: false, Agents: map[string]bool{"phone": true}},
	}}
	out := toolbox.Apply(list, l)
	if h := find(t, out, "chat-browser").Health; h == nil || h.Level != toolbox.HealthGood || !strings.Contains(h.Reason, "not checked") {
		t.Errorf("holder up: %+v", h)
	}
	if h := find(t, out, "connector").Health; h == nil || h.Level != toolbox.HealthDown || !strings.Contains(h.Reason, "stopped") {
		t.Errorf("holder stopped: %+v", h)
	}
	if h := find(t, out, "fleet-phone").Health; h == nil || h.Level != toolbox.HealthWarning {
		t.Errorf("machine unreachable: %+v", h)
	}
	if h := find(t, out, "network").Health; h != nil {
		t.Errorf("no holder, no health: %+v", h)
	}
	// A holder on this node.
	out = toolbox.Apply(list, toolbox.Live{Machine: toolboxtest.BoxC, Now: now, Agents: map[string]bool{"phone": true}})
	if h := find(t, out, "fleet-phone").Health; h == nil || h.Level != toolbox.HealthGood {
		t.Errorf("local holder up: %+v", h)
	}
	out = toolbox.Apply(list, toolbox.Live{Machine: toolboxtest.BoxB, Now: now, Agents: map[string]bool{"assistant-lead": true}})
	if h := find(t, out, "chat-browser").Health; h == nil || h.Level != toolbox.HealthDown || !strings.Contains(h.Reason, "not in the fleet list") {
		t.Errorf("local holder missing: %+v", h)
	}
	// A machine this node does not see: nothing is claimed.
	if h := find(t, out, "fleet-phone").Health; h != nil {
		t.Errorf("unknown machine: %+v", h)
	}
}

func TestApplySystemTools(t *testing.T) {
	list := toolboxtest.Install(t)
	out := toolbox.Apply(list, toolbox.Live{Machine: toolboxtest.Node, Now: now, SystemTools: []toolbox.SystemTool{
		{ID: "AB12cd34ef", Purpose: "Phone bridge", State: "running", Restarts: 2},
		{ID: "ffff0000", Purpose: "Mail poller", State: "retrying"},
		{ID: "0001", Purpose: "reach someone@example.com", State: "stopped"},
	}})
	if len(out) != toolboxtest.Lines+3 {
		t.Fatalf("%d lines, want %d", len(out), toolboxtest.Lines+3)
	}
	if err := toolbox.ValidateList(out); err != nil {
		t.Fatal(err)
	}
	a := find(t, out, "sys-phone-bridge-ab12cd")
	if a.Kind != toolbox.KindTool || a.Source != toolbox.SourceLive || a.State != toolbox.StateWorks || a.Health.Level != toolbox.HealthGood || !strings.Contains(a.Health.Reason, "2 times") {
		t.Errorf("%+v health %+v", a, a.Health)
	}
	if b := find(t, out, "sys-mail-poller-ffff00"); b.Health.Level != toolbox.HealthWarning {
		t.Errorf("%+v", b.Health)
	}
	// A purpose that holds an address is not shown.
	c := find(t, out, "sys-reach-someone-ex-0001")
	if c.Name != "System tool" || c.Health.Level != toolbox.HealthDown {
		t.Errorf("%+v health %+v", c, c.Health)
	}
}
