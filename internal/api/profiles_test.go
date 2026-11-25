package api_test

import (
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

func TestProfileRoutes(t *testing.T) {
	d := apitest.New(t)
	owner := client{t: t, base: d.URL, token: d.OwnerToken}
	agent := client{t: t, base: d.URL, token: d.AgentToken(t, "head")}

	var list []model.Profile
	if s := agent.do("GET", "/api/profiles", nil, &list); s != 200 || len(list) != 5 || list[3].Slug != model.FreeWorkerProfile || list[4].Slug != "reviewer" {
		t.Fatalf("list: %d %+v", s, list)
	}
	// Profiles are the owner's and the head's (17 Nov); a worker changes none.
	workers := workerProject(t, d)
	if _, err := d.Fleet.LaunchAgent(fleet.OwnerCaller, fleet.LaunchRequest{Name: "w1", ProjectID: workers}); err != nil {
		t.Fatal(err)
	}
	worker := client{t: t, base: d.URL, token: d.AgentToken(t, "w1")}
	for _, r := range []struct{ m, p string }{
		{"POST", "/api/profiles"}, {"PUT", "/api/profiles/worker"}, {"DELETE", "/api/profiles/reviewer"},
		{"POST", "/api/profiles/worker/reset"}, {"PATCH", "/api/agents/head"},
	} {
		if s := worker.do(r.m, r.p, `{}`, nil); s != 403 {
			t.Errorf("worker %s %s: %d", r.m, r.p, s)
		}
	}
	var before, after model.Profile
	owner.do("GET", "/api/profiles/worker", nil, &before)
	before.Instructions = "You are {{name}}. Report every hour."
	if s := agent.do("PUT", "/api/profiles/worker", before, &after); s != 200 || after.Instructions != before.Instructions {
		t.Fatalf("the head changing a profile: %d %+v", s, after)
	}
	if s := agent.do("POST", "/api/profiles/worker/reset", nil, &after); s != 200 || after.Instructions == before.Instructions {
		t.Fatalf("the head resetting a profile: %d", s)
	}

	p := model.Profile{Slug: "qa", Name: "QA", Level: model.LevelWorker, Model: "claude-sonnet-5-5", ReadOnly: true,
		Instructions: "Test {{project}}."}
	var got model.Profile
	if s := owner.do("POST", "/api/profiles", p, &got); s != 201 || got.Slug != "qa" || got.BuiltIn {
		t.Fatalf("create: %d %+v", s, got)
	}
	if s := owner.do("POST", "/api/profiles", p, nil); s != 409 {
		t.Fatalf("duplicate: %d", s)
	}
	got.Description = "Runs the tests"
	if s := owner.do("PUT", "/api/profiles/qa", got, &got); s != 200 || got.Description != "Runs the tests" {
		t.Fatalf("put: %d %+v", s, got)
	}
	if s := owner.do("GET", "/api/profiles/qa", nil, &got); s != 200 || got.Name != "QA" {
		t.Fatalf("get: %d", s)
	}
	if s := owner.do("GET", "/api/profiles/ghost", nil, nil); s != 404 {
		t.Fatalf("get unknown: %d", s)
	}

	var a model.Agent
	if s := owner.do("POST", "/api/agents", map[string]any{"name": "q1", "profile": "qa", "project_id": workers}, &a); s != 201 || a.Profile != "qa" || a.Level != model.LevelWorker {
		t.Fatalf("launch by profile: %d %+v", s, a)
	}
	if s := owner.do("DELETE", "/api/profiles/qa", nil, nil); s != 409 {
		t.Fatalf("delete in use: %d", s)
	}
	var up struct {
		Agent         model.Agent `json:"agent"`
		RestartNeeded bool        `json:"restart_needed"`
		Note          string      `json:"note"`
	}
	if s := owner.do("PATCH", "/api/agents/q1", map[string]any{"role": "e2e", "profile": "worker"}, &up); s != 200 ||
		up.Agent.Role != "e2e" || up.Agent.Profile != "worker" || !up.RestartNeeded || up.Note == "" {
		t.Fatalf("patch: %d %+v", s, up)
	}
	if s := owner.do("PATCH", "/api/agents/q1", map[string]any{"model": "gpt"}, nil); s != 403 {
		t.Fatalf("patch disallowed model: %d", s)
	}
	if s := owner.do("DELETE", "/api/profiles/qa", nil, nil); s != 200 {
		t.Fatalf("delete: %d", s)
	}
	if s := owner.do("DELETE", "/api/profiles/worker", nil, nil); s != 403 {
		t.Fatalf("delete built-in: %d", s)
	}
	if s := owner.do("POST", "/api/profiles/worker/reset", nil, &got); s != 200 || got.Modified {
		t.Fatalf("reset: %d %+v", s, got)
	}
	var snap model.Snapshot
	if owner.do("GET", "/api/snapshot", nil, &snap); len(snap.Profiles) != 5 {
		t.Fatalf("snapshot profiles: %+v", snap.Profiles)
	}
}
