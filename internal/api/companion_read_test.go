package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	tc "github.com/blackcode-ch/tmx-go/internal/client"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// Reading Companion's state (17 Nov, for the head's round trip with Companion): the link's state
// and the tasks and messages with their states were the owner's alone, so the head could not see
// whether its own message was queued, working or refused. Now the head reads all of it, a lead
// its own project's, a worker only its own tasks; nobody reads a key or a token.
func TestCompanionStateIsReadableByTheHeadAndLeads(t *testing.T) {
	d := apitest.New(t)
	owner := tc.New(d.URL, d.OwnerToken)
	connect(t, d)
	p, err := d.Fleet.CreateProject(fleet.OwnerCaller, fleet.ProjectRequest{Name: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Fleet.LaunchAgent(fleet.OwnerCaller, fleet.LaunchRequest{Name: "a1", ProjectID: p.ID}); err != nil {
		t.Fatal(err)
	}
	head := tc.New(d.URL, d.AgentToken(t, "head"))
	lead := tc.New(d.URL, d.AgentToken(t, "alpha-lead"))
	worker := tc.New(d.URL, d.AgentToken(t, "a1"))

	// The head's message and the owner's.
	hm, err := d.Companion.SendHeadMessage(d.Fleet.HeadID(), "How is the mailbox today?", "")
	if err != nil {
		t.Fatal(err)
	}
	var om model.CompanionTask
	if code, _ := call(t, owner, "POST", "/api/companion/messages", map[string]string{"text": "Good morning"}, &om); code != 0 {
		t.Fatalf("the owner's message: %d", code)
	}

	// The link's state: owner, head, lead; not a worker; never a key.
	for name, cl := range map[string]*tc.Client{"owner": owner, "head": head, "lead": lead} {
		var raw json.RawMessage
		if code, _ := call(t, cl, "GET", "/api/companion?probe=0", nil, &raw); code != 0 {
			t.Fatalf("%s reads the link state: %d", name, code)
		}
		var st struct {
			Connected bool `json:"connected"`
		}
		_ = json.Unmarshal(raw, &st)
		if !st.Connected {
			t.Errorf("%s: link state %s", name, raw)
		}
		for _, secret := range []string{"token\":\"", "key\":\"", "secret"} {
			if strings.Contains(strings.ToLower(string(raw)), secret) {
				t.Errorf("%s: the link state carries %q: %s", name, secret, raw)
			}
		}
	}
	if code, _ := call(t, worker, "GET", "/api/companion?probe=0", nil, nil); code != 403 {
		t.Errorf("a worker read the link state: %d", code)
	}

	// Tasks and messages: the head sees its own and the owner's; a lead and a worker neither.
	ids := func(cl *tc.Client) map[string]string {
		t.Helper()
		var list []model.CompanionTask
		if code, _ := call(t, cl, "GET", "/api/companion/tasks", nil, &list); code != 0 {
			t.Fatalf("list: %d", code)
		}
		out := map[string]string{}
		for _, x := range list {
			out[x.ID] = string(x.State)
		}
		return out
	}
	if got := ids(head); got[hm.ID] == "" || got[om.ID] == "" {
		t.Errorf("the head's list: %v", got)
	}
	for name, cl := range map[string]*tc.Client{"lead": lead, "worker": worker} {
		if got := ids(cl); len(got) != 0 {
			t.Errorf("%s sees messages that are not its project's: %v", name, got)
		}
	}
	var one model.CompanionTask
	if code, _ := call(t, head, "GET", "/api/companion/tasks/"+hm.ID, nil, &one); code != 0 || one.ID != hm.ID || one.State == "" {
		t.Errorf("the head reads its message's state: %d %+v", code, one)
	}
	if code, _ := call(t, head, "GET", "/api/companion/tasks/"+om.ID, nil, nil); code != 0 {
		t.Errorf("the head reads the owner's message: %d", code)
	}
	for name, cl := range map[string]*tc.Client{"lead": lead, "worker": worker} {
		if code, _ := call(t, cl, "GET", "/api/companion/tasks/"+hm.ID, nil, nil); code != 404 {
			t.Errorf("%s read the head's message: %d", name, code)
		}
	}

	// Cancel requires visibility and manager authority; owner tasks require the owner or head.
	if code, _ := call(t, worker, "POST", "/api/companion/tasks/"+hm.ID+"/cancel", nil, nil); code != 403 {
		t.Fatalf("worker cancel: %d", code)
	}
	if code, _ := call(t, lead, "POST", "/api/companion/tasks/"+hm.ID+"/cancel", nil, nil); code != 404 {
		t.Fatalf("cross-project cancel: %d", code)
	}
	if code, _ := call(t, head, "POST", "/api/companion/tasks/"+hm.ID+"/cancel", nil, &one); code != 0 || one.State != model.CompanionCanceled {
		t.Fatalf("head cancel: %d %+v", code, one)
	}

	var own model.CompanionTask
	if code, _ := call(t, owner, "POST", "/api/companion/tasks", map[string]any{"agent_id": "a1", "spec": model.CompanionSpec{Goal: "Check the local calendar"}}, &own); code != 0 {
		t.Fatalf("create project task: %d", code)
	}
	if code, _ := call(t, worker, "POST", "/api/companion/tasks/"+own.ID+"/cancel", nil, nil); code != 403 {
		t.Fatalf("worker cancels own task: %d", code)
	}
	if code, _ := call(t, lead, "POST", "/api/companion/tasks/"+own.ID+"/cancel", nil, nil); code != 403 {
		t.Fatalf("lead cancels owner's project task: %d", code)
	}
	if code, _ := call(t, head, "GET", "/api/companion/tasks/"+own.ID, nil, &one); code != 0 || one.Origin != "owner" || one.State == model.CompanionCanceled {
		t.Fatalf("refused cancel must preserve the owner's task: %d %+v", code, one)
	}
	if code, _ := call(t, head, "POST", "/api/companion/tasks/"+own.ID+"/cancel", nil, &one); code != 0 || one.State != model.CompanionCanceled {
		t.Fatalf("head cancels owner's project task: %d %+v", code, one)
	}

	// Writing stays as it was: the head cannot connect or read the conversation.
	for _, r := range []struct{ m, p string }{
		{"DELETE", "/api/companion/connect"},
		{"POST", "/api/companion/link-token"}, {"GET", "/api/companion/conversation"},
	} {
		if code, _ := call(t, head, r.m, r.p, nil, nil); code != 403 {
			t.Errorf("the head's %s %s: %d", r.m, r.p, code)
		}
	}
}
