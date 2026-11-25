package toolbox

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The fleet's own list (seed_private.go). A build without that file (the public snapshot)
// has no list of its own and skips these; a build with it must have loaded it.
func loadPrivateSeed(t *testing.T) []Resource {
	t.Helper()
	if _, err := os.Stat("seed_private.go"); err != nil {
		t.Skip("this build declares no resources of its own")
	}
	list := Seed()
	if len(list) == 0 {
		t.Fatal("seed_private.go is here but the declared list is empty")
	}
	return list
}

func privateFind(t *testing.T, list []Resource, id string) Resource {
	t.Helper()
	r, ok := Find(list, id)
	if !ok {
		t.Fatalf("no resource %q", id)
	}
	return r
}

func TestPrivateSeedIsValidAndComplete(t *testing.T) {
	list := loadPrivateSeed(t)
	if err := ValidateList(list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 29 {
		t.Fatalf("the list has %d lines, want 29", len(list))
	}
	kinds := map[string]int{}
	for _, r := range list {
		kinds[r.Kind]++
		if r.Health != nil || r.Meter != nil {
			t.Errorf("%s: health and meter are measured, never declared", r.ID)
		}
		if r.State == StateWorks && (r.VerifiedAt == "" || r.VerifiedBy == "") {
			t.Errorf("%s: \"works\" needs who verified it and when", r.ID)
		}
		for _, s := range r.Secrets {
			if s.Stored != nil {
				t.Errorf("%s: whether %q is stored is measured, never declared", r.ID, s.Name)
			}
		}
	}
	for _, k := range Kinds {
		if kinds[k] == 0 {
			t.Errorf("no resource of kind %s", k)
		}
	}
	// What the owner named on 17 Nov each has a line: WhatsApp, the agents' mailbox,
	// Companion, the phone number, the 4G stick with its SIM, an Android phone and a virtual
	// Android for calls.
	for _, id := range []string{"wa-owner-go", "wa-owner-companion", "agents-mail", "companion", "agent-phone", "hotspot-sim", "android-phone", "android-virtual", "voice-calls"} {
		privateFind(t, list, id)
	}
}

// tmx-lead's corrections to the spec, 17 Nov.
func TestPrivateSeedCorrections(t *testing.T) {
	list := loadPrivateSeed(t)
	if got := privateFind(t, list, "ai-accounts").Rule.Text; got != "Hard work on Fable, easy work on Codex/GPT-6 (max 5)." {
		t.Errorf("AI accounts rule = %q", got)
	}
	mail := privateFind(t, list, "owner-work-google")
	if !strings.Contains(mail.How, "claude.ai connectors") || strings.Contains(strings.ToLower(mail.How+mail.HolderNote+mail.Rule.Text), "composio") {
		t.Errorf("the work mail goes through the claude.ai connectors, not Composio: %q", mail.How)
	}
	if c := privateFind(t, list, "composio"); !strings.Contains(c.How, "not the road to his work mail") {
		t.Errorf("the Composio line must not claim the work mail: %q", c.How)
	}
	alias := privateFind(t, list, "agents-mail")
	if alias.Maintainer != "TBD" || !strings.Contains(alias.HolderNote, "Workspace") || !strings.Contains(alias.HolderNote, "alias") {
		t.Errorf("agents' mailbox: maintainer %q, holder %q", alias.Maintainer, alias.HolderNote)
	}
}

// The lines the owner added on 17 Nov, 19:42 ("never finished before"): not connected yet,
// with the owners as named.
func TestPrivateSeedNotConnectedLines(t *testing.T) {
	list := loadPrivateSeed(t)
	want := map[string]string{
		"google-drive-work":      "connectors-lead-f",
		"google-one":             "connectors-lead-f",
		"digital-estate":         "digital-estate (project)",
		"companion-memory":       "companion-lead-h@hosta",
		"companion-skills":       "companion-lead-h@hosta",
		"companion-integrations": "companion-lead-h@hosta",
		"arc-browser":            "companion-lead-h@hosta",
	}
	for id, maintainer := range want {
		r := privateFind(t, list, id)
		if r.State != StateNotConnected || r.Maintainer != maintainer {
			t.Errorf("%s: state %q maintainer %q", id, r.State, r.Maintainer)
		}
		if strings.HasPrefix(id, "companion-") || id == "arc-browser" {
			if r.Parent != "companion" {
				t.Errorf("%s: parent %q, want companion", id, r.Parent)
			}
		}
	}
	// The private account is storage only: never his personal mail, and its rule is pending.
	one := privateFind(t, list, "google-one")
	if !strings.Contains(one.How, "not his personal mail") || !strings.Contains(one.Rule.Text, "pending") {
		t.Errorf("google-one: how %q rule %q", one.How, one.Rule.Text)
	}
	if cu := privateFind(t, list, "computer-use"); cu.Parent != "companion" || !strings.Contains(cu.How, "Mac version is to be built") {
		t.Errorf("computer-use: parent %q how %q", cu.Parent, cu.How)
	}
}

// No secrets in resource records: identities are names and handles, never tokens,
// passwords, full addresses or numbers. Checked on the bytes that leave the node.
func TestPrivateSeedHoldsNoSecretShapes(t *testing.T) {
	for _, r := range loadPrivateSeed(t) {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatal(err)
		}
		var walk func(any)
		walk = func(x any) {
			switch x := x.(type) {
			case string:
				if shape := SecretShape(x); shape != "" {
					t.Errorf("%s: %q holds %s", r.ID, x, shape)
				}
			case []any:
				for _, e := range x {
					walk(e)
				}
			case map[string]any:
				for _, e := range x {
					walk(e)
				}
			}
		}
		walk(v)
	}
}
