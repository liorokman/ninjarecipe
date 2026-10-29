package service

import (
	"context"
	"errors"
	"github.com/liorokman/ninjarecipe/internal/events"
	"testing"
	"time"

	"github.com/liorokman/ninjarecipe/internal/categorize"
	"github.com/liorokman/ninjarecipe/internal/domain"
)

type fakeCat struct {
	cats  []string
	err   error
	calls int
	last  categorize.RecipeSnapshot
}

func (f *fakeCat) Suggest(_ context.Context, r categorize.RecipeSnapshot, _ []categorize.CategoryInfo, _ []string) (categorize.Suggestion, error) {
	f.calls++
	f.last = r
	return categorize.Suggestion{Categories: f.cats, Confidence: 0.9, Model: "fake"}, f.err
}

func labelSources(t *testing.T, e *env, id string) map[string]string {
	t.Helper()
	r, err := e.svc.Store.R().GetRecipe(e.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, l := range r.Labels {
		if l.Type == "category" {
			m[l.Slug] = l.Source
		}
	}
	return m
}

func TestCategorizeAddsLLMCategoriesOnce(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", false)
	res := importOK(t, e, alice, "Smoked salmon", "https://example.com/salmon")
	f := &fakeCat{cats: []string{"fish_seafood", "bogus", "vegetables"}}
	e.svc.Categorizer = f

	if err := e.svc.CategorizeRecipe(e.ctx, res.ID); err != nil {
		t.Fatal(err)
	}
	got := labelSources(t, e, res.ID)
	if len(got) != 2 || got["fish_seafood"] != "llm" || got["vegetables"] != "llm" {
		t.Fatalf("labels %v (unknown slugs ignored)", got)
	}
	r, _ := e.svc.Store.R().GetRecipe(e.ctx, res.ID)
	if r.CategorizedAt == nil {
		t.Fatal("categorized_at not set")
	}
	if f.last.Title != "Smoked salmon" {
		t.Fatalf("snapshot %+v", f.last)
	}
	// processed recipes are not asked again
	if err := e.svc.CategorizeRecipe(e.ctx, res.ID); err != nil || f.calls != 1 {
		t.Fatalf("err=%v calls=%d", err, f.calls)
	}
}

func TestCategorizeKeepsExistingAndRespectsLimit(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", false)
	res, err := e.svc.ImportToInbox(e.ctx, alice, RecipeImport{Title: "Mix", SourceURL: "https://example.com/mix",
		Categories: []string{"pizza", "beef", "lamb", "pork"}}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Categorizer = &fakeCat{cats: []string{"pizza", "poultry", "sides"}}
	if err := e.svc.CategorizeRecipe(e.ctx, res.ID); err != nil {
		t.Fatal(err)
	}
	got := labelSources(t, e, res.ID)
	if len(got) != domain.MaxCategories || got["poultry"] != "llm" || got["pizza"] == "llm" || got["pizza"] == "" {
		t.Fatalf("labels %v: existing kept with their source, one llm slot left", got)
	}
}

func TestCategorizeErrorLeavesMarkerForRetry(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", false)
	a := importOK(t, e, alice, "A", "https://example.com/a")
	importOK(t, e, alice, "B", "https://example.com/b")
	f := &fakeCat{err: errors.New("model down")}
	e.svc.Categorizer = f

	n, err := e.svc.CategorizePending(e.ctx, 10)
	if err == nil || n != 0 || f.calls != 1 {
		t.Fatalf("n=%d err=%v calls=%d (stop at first failure)", n, err, f.calls)
	}
	if r, _ := e.svc.Store.R().GetRecipe(e.ctx, a.ID); r.CategorizedAt != nil {
		t.Fatal("marker set despite failure")
	}
	f.err, f.cats = nil, []string{"desserts"}
	if n, err = e.svc.CategorizePending(e.ctx, 10); err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if n, _ = e.svc.CategorizePending(e.ctx, 10); n != 0 {
		t.Fatalf("second scan found %d", n)
	}
}

func TestCategorizeWithoutCategorizerIsNoop(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", false)
	res := importOK(t, e, alice, "A", "https://example.com/a")
	if err := e.svc.CategorizeRecipe(e.ctx, res.ID); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.svc.Store.R().GetRecipe(e.ctx, res.ID); r.CategorizedAt != nil {
		t.Fatal("marker set with no categorizer")
	}
}

func TestCategorizerRunsOnInboxEvent(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", false)
	e.svc.Bus = events.NewBus(10)
	defer e.svc.Bus.Close()
	e.svc.StartCategorizer(&fakeCat{cats: []string{"pizza"}})

	res := importOK(t, e, alice, "Margherita", "https://example.com/m")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if labelSources(t, e, res.ID)["pizza"] == "llm" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("event did not trigger categorization")
}

func TestCategorizerSettingsPrecedenceAndSwitch(t *testing.T) {
	e := newEnv(t)
	admin, alice := e.user("admin", true), e.user("alice", false)
	e.svc.CatDefaults = categorize.Settings{Enabled: false, URL: "http://env:8080", APIKey: "envkey", Model: "env-model", Threshold: 0.5}
	e.svc.Categorizer = categorize.Configured{Load: e.svc.CategorizerSettings}

	got, err := e.svc.AdminCategorizer(e.ctx, admin)
	if err != nil || got.URL != "http://env:8080" || got.Enabled || got.ThresholdPct != 50 || !got.KeySet {
		t.Fatalf("env defaults: %+v %v", got, err)
	}
	if _, err := e.svc.AdminCategorizer(e.ctx, alice); !is(err, domain.ErrForbidden) {
		t.Fatalf("non-admin: %v", err)
	}

	// disabled: nothing is processed and the marker stays NULL
	res := importOK(t, e, alice, "Salmon", "https://example.com/s")
	if n, err := e.svc.CategorizePending(e.ctx, 10); err != nil || n != 0 {
		t.Fatalf("disabled scan: %d %v", n, err)
	}
	if err := e.svc.CategorizeRecipe(e.ctx, res.ID); err != nil {
		t.Fatalf("disabled single: %v", err)
	}
	if r, _ := e.svc.Store.R().GetRecipe(e.ctx, res.ID); r.CategorizedAt != nil {
		t.Fatal("marker set while disabled")
	}

	// validation
	err = e.svc.SaveCategorizer(e.ctx, admin, CategorizerInput{Enabled: true, URL: "", Model: "", ThresholdPct: "0"})
	var ve *domain.ValidationError
	if !errors.As(err, &ve) || len(ve.Errors) != 3 {
		t.Fatalf("want url/model/threshold errors, got %v", err)
	}
	if err := e.svc.SaveCategorizer(e.ctx, alice, CategorizerInput{}); !is(err, domain.ErrForbidden) {
		t.Fatalf("non-admin save: %v", err)
	}

	// saved settings override the environment; an empty key field keeps the stored/env key; clear overrides env key
	if err := e.svc.SaveCategorizer(e.ctx, admin, CategorizerInput{Enabled: true, URL: "http://db:1/", Model: "db-model", ThresholdPct: "70%"}); err != nil {
		t.Fatal(err)
	}
	eff, _ := e.svc.CategorizerSettings(e.ctx)
	if !eff.Enabled || eff.URL != "http://db:1" || eff.Model != "db-model" || eff.Threshold != 0.7 || eff.APIKey != "envkey" {
		t.Fatalf("effective %+v", eff)
	}
	if err := e.svc.SaveCategorizer(e.ctx, admin, CategorizerInput{Enabled: true, URL: "http://db:1", Model: "db-model", ThresholdPct: "70", ClearKey: true}); err != nil {
		t.Fatal(err)
	}
	if eff, _ = e.svc.CategorizerSettings(e.ctx); eff.APIKey != "" {
		t.Fatalf("clear must beat the environment key: %q", eff.APIKey)
	}
}
