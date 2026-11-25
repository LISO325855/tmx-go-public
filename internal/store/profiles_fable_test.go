package store

import (
	"slices"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// Fable 5.2 among the allowed models of the built-in profiles (the owner #11, 17 Nov 2026).

func TestDefaultProfilesAllowFableButNeverStartOnIt(t *testing.T) {
	for _, slug := range []string{"head", "lead", "worker"} {
		p, ok := model.DefaultProfile(slug)
		if !ok {
			t.Fatalf("no default profile %s", slug)
		}
		if !slices.Contains(p.AllowedModels, model.FableModel) {
			t.Errorf("%s does not allow Fable: %v", slug, p.AllowedModels)
		}
		if p.Model == model.FableModel {
			t.Errorf("%s starts on Fable; it must only be allowed", slug)
		}
		if !slices.Contains(p.AllowedModels, p.Model) {
			t.Errorf("%s: its default model %s is not among its allowed models %v", slug, p.Model, p.AllowedModels)
		}
	}
	if p, _ := model.DefaultProfile("reviewer"); slices.Contains(p.AllowedModels, model.FableModel) {
		t.Errorf("the reviewer was not asked to get Fable: %v", p.AllowedModels)
	}
	// the policy derived per level says the same
	for lv, lp := range model.DefaultPolicy().Levels {
		if !slices.Contains(lp.AllowedModels, model.FableModel) || lp.Model == model.FableModel {
			t.Errorf("policy %s: model %s allowed %v", lv, lp.Model, lp.AllowedModels)
		}
	}
}

// withoutFable is a built-in profile as a store seeded before Fable holds it.
func withoutFable(t *testing.T, slug string) model.Profile {
	t.Helper()
	p, ok := model.DefaultProfile(slug)
	if !ok {
		t.Fatalf("no default profile %s", slug)
	}
	p.AllowedModels = slices.DeleteFunc(slices.Clone(p.AllowedModels), func(m string) bool { return m == model.FableModel })
	p.CreatedAt = 123
	return p
}

func stored(t *testing.T, s *Store, slug string) model.Profile {
	t.Helper()
	list, err := s.ListProfiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range list {
		if p.Slug == slug {
			return p
		}
	}
	t.Fatalf("no stored profile %s", slug)
	return model.Profile{}
}

func TestUpgradeFableAllowedAddsItToUntouchedModelLists(t *testing.T) {
	s := openTest(t)
	for _, slug := range []string{"head", "lead", "worker", "reviewer"} {
		p := withoutFable(t, slug)
		if slug == "lead" {
			p.Description = "owner's own words" // other settings may differ: only the model list decides
		}
		if err := s.PutProfile(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpgradeFableAllowed(); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"head", "lead", "worker"} {
		got := stored(t, s, slug)
		want, _ := model.DefaultProfile(slug)
		if !slices.Equal(got.AllowedModels, want.AllowedModels) {
			t.Errorf("%s allowed models = %v, want %v", slug, got.AllowedModels, want.AllowedModels)
		}
		if got.Model != want.Model {
			t.Errorf("%s default model changed to %s", slug, got.Model)
		}
		if got.CreatedAt != 123 {
			t.Errorf("%s was re-created", slug)
		}
	}
	if got := stored(t, s, "lead"); got.Description != "owner's own words" {
		t.Errorf("the lead's other settings were touched: %q", got.Description)
	}
	if got := stored(t, s, "reviewer"); slices.Contains(got.AllowedModels, model.FableModel) {
		t.Errorf("the reviewer got Fable: %v", got.AllowedModels)
	}
}

func TestUpgradeFableAllowedLeavesAnOwnersListAndRunsOnce(t *testing.T) {
	s := openTest(t)
	// the owner narrowed the worker to one model, and widened the head by hand
	worker := withoutFable(t, "worker")
	worker.AllowedModels = []string{worker.Model}
	head := withoutFable(t, "head")
	head.AllowedModels = append(head.AllowedModels, "claude-sonnet-5")
	lead := withoutFable(t, "lead")
	for _, p := range []model.Profile{worker, head, lead} {
		if err := s.PutProfile(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpgradeFableAllowed(); err != nil {
		t.Fatal(err)
	}
	if got := stored(t, s, "worker"); !slices.Equal(got.AllowedModels, worker.AllowedModels) {
		t.Errorf("the owner's narrowed worker list was changed: %v", got.AllowedModels)
	}
	if got := stored(t, s, "head"); !slices.Equal(got.AllowedModels, head.AllowedModels) {
		t.Errorf("the owner's widened head list was changed: %v", got.AllowedModels)
	}
	if got := stored(t, s, "lead"); !slices.Contains(got.AllowedModels, model.FableModel) {
		t.Fatalf("the untouched lead did not get Fable: %v", got.AllowedModels)
	}

	// once: an owner who takes Fable out again is not given it back at the next daemon start
	l := stored(t, s, "lead")
	l.AllowedModels = slices.DeleteFunc(slices.Clone(l.AllowedModels), func(m string) bool { return m == model.FableModel })
	if err := s.PutProfile(l); err != nil {
		t.Fatal(err)
	}
	if err := s.UpgradeFableAllowed(); err != nil {
		t.Fatal(err)
	}
	if got := stored(t, s, "lead"); slices.Contains(got.AllowedModels, model.FableModel) {
		t.Errorf("Fable came back after the owner removed it: %v", got.AllowedModels)
	}
}

func TestUpgradeFableAllowedOnAnEmptyStoreIsHarmless(t *testing.T) {
	s := openTest(t)
	if err := s.UpgradeFableAllowed(); err != nil {
		t.Fatal(err)
	}
	if list, err := s.ListProfiles(); err != nil || len(list) != 0 {
		t.Fatalf("profiles appeared: %v %v", list, err)
	}
}
