package fleet

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
)

func TestFreeWorkerLaunchAndUsage(t *testing.T) {
	e := newUnguardedEnv(t)
	f := e.started()
	project, err := f.CreateProject(OwnerCaller, ProjectRequest{Name: "fae-fixture"})
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, caller, choice, want string
	}{
		{"head-free", f.HeadID(), "", model.FaeDefaultModel},
		{"lead-free", project.LeadID, "fae/gpt-oss:20b", "fae/gpt-oss:20b"},
	} {
		a, err := f.LaunchAgent(AgentCaller(tc.caller), LaunchRequest{
			Name: tc.name, ProjectID: project.ID, Profile: model.FreeWorkerProfile, Model: tc.choice,
		})
		if err != nil || a.Harness != model.HarnessOpenCode || a.Account != "none" ||
			a.Mode != model.ModeTerminal || a.Model != tc.want {
			t.Fatalf("launch: %+v, %v", a, err)
		}
		p := e.term.Last(a.ID)
		spec := p.Spec
		if spec.Model != tc.want || spec.Provider != model.FaeProvider || spec.SessionID != "" ||
			spec.OpenCodeDataDir != filepath.Join(f.opts.Paths.AgentDir(a.ID), "opencode-data") || spec.MCP == nil {
			t.Fatalf("spec: %+v", spec)
		}
		p.Emit(runner.Event{Kind: runner.EvUsage, CostUSD: 1.0, Turns: 1})
		eventually(t, "free worker usage event", func() bool {
			return mustAgent(t, f, a.ID).Turns == 1
		})
		if got := mustAgent(t, f, a.ID); got.CostUSD != 0 {
			t.Fatalf("free worker cost: %v", got.CostUSD)
		}
	}

	a := mustAgent(t, f, "lead-free")
	e.term.Last(a.ID).Emit(runner.Event{Kind: runner.EvSession, SessionID: "ses_fixture"})
	eventually(t, "saved OpenCode session", func() bool {
		return mustAgent(t, f, a.ID).SessionID == "ses_fixture"
	})
	reopened := e.fleet()
	spec := reopened.harnessSpecLocked(reopened.agents[a.ID], runner.Spec{})
	if spec.SessionID != "ses_fixture" {
		t.Fatalf("session after daemon reopen: %q", spec.SessionID)
	}

	for _, req := range []LaunchRequest{
		{Harness: model.HarnessClaude},
		{Account: "paid"},
		{Mode: model.ModeChat},
		{Model: "opencode/gpt"},
	} {
		req.Name, req.ProjectID, req.Profile = "invalid", project.ID, model.FreeWorkerProfile
		if _, err := f.LaunchAgent(AgentCaller(project.LeadID), req); err == nil {
			t.Fatalf("accepted request: %+v", req)
		}
	}
}

func TestFreeWorkerUpgradeKeepsExistingProfileSettings(t *testing.T) {
	e := newEnv(t)
	for _, p := range model.DefaultProfiles() {
		if p.Slug == model.FreeWorkerProfile {
			continue
		}
		p.CanLaunch = slices.DeleteFunc(p.CanLaunch, func(slug string) bool { return slug == model.FreeWorkerProfile })
		if p.Slug == "lead" {
			p.Instructions = "Existing lead instructions"
		}
		if err := e.st.PutProfile(p); err != nil {
			t.Fatal(err)
		}
	}
	f := e.started()
	for _, slug := range []string{"head", "lead"} {
		p, _ := f.GetProfile(slug)
		if !slices.Contains(p.CanLaunch, model.FreeWorkerProfile) {
			t.Fatalf("%s cannot launch a free worker after upgrade", slug)
		}
	}
	p, _ := f.GetProfile("lead")
	if p.Instructions != "Existing lead instructions" {
		t.Fatal(p.Instructions)
	}
	p.CanLaunch = []string{"worker"}
	if _, err := f.UpdateProfile(OwnerCaller, p.Slug, p); err != nil {
		t.Fatal(err)
	}
	f = e.started()
	p, _ = f.GetProfile("lead")
	if !slices.Equal(p.CanLaunch, []string{"worker"}) {
		t.Fatalf("migration overrode later edits: %v", p.CanLaunch)
	}
}
