package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/liorokman/ninjarecipe/internal/categorize"
	"github.com/liorokman/ninjarecipe/internal/domain"
	"github.com/liorokman/ninjarecipe/internal/events"
	"github.com/liorokman/ninjarecipe/internal/store"
)

// StartCategorizer subscribes c to recipe and inbox events (20 §15). Events are only a latency optimisation:
// CategorizePending is the durable path and picks up whatever an event missed.
func (s *Service) StartCategorizer(c categorize.Categorizer) {
	s.Categorizer = c
	if s.Bus == nil {
		return
	}
	h := func(ctx context.Context, e events.Event) {
		var id string
		switch ev := e.(type) {
		case events.RecipeCreated:
			id = ev.RecipeID
		case events.InboxItemReceived:
			id = ev.RecipeID
		default:
			return
		}
		s.logErr("categorize "+id, s.CategorizeRecipe(ctx, id))
	}
	s.Bus.Subscribe(events.RecipeCreated{}.Type(), h)
	s.Bus.Subscribe(events.InboxItemReceived{}.Type(), h)
}

// CategorizeRecipe asks the categorizer about one recipe, adds the suggested categories (source "llm", never
// removing anything and never exceeding the category limit) and sets categorized_at. A categorizer error leaves
// the marker NULL so a later scan retries. Recipes that are gone or already processed are skipped silently.
func (s *Service) CategorizeRecipe(ctx context.Context, id string) error {
	if s.Categorizer == nil {
		return nil
	}
	r, err := s.Store.R().GetRecipe(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if r.CategorizedAt != nil {
		return nil
	}
	cats, err := s.Store.R().ListCategories(ctx)
	if err != nil {
		return err
	}
	bySlug := map[string]domain.Label{}
	for _, c := range cats {
		bySlug[c.Slug] = c
	}
	snap := categorize.RecipeSnapshot{ID: r.ID, Language: r.Language, Title: r.Title, Description: r.Description}
	for _, i := range r.Ingredients {
		snap.Ingredients = append(snap.Ingredients, strings.TrimSpace(i.RawText))
	}
	for _, st := range r.Steps {
		snap.Steps = append(snap.Steps, st.Text)
	}
	for _, st := range r.Stages {
		snap.OvenModes = append(snap.OvenModes, st.Mode)
	}
	have := map[string]bool{}
	for _, l := range r.Labels {
		if l.Type == "category" {
			have[l.Slug] = true
			snap.Categories = append(snap.Categories, l.Slug)
		} else {
			snap.Tags = append(snap.Tags, l.Slug)
		}
	}
	sug, err := s.Categorizer.Suggest(ctx, snap, categoryInfos(cats), snap.Tags)
	if errors.Is(err, categorize.ErrDisabled) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("suggest: %w", err)
	}
	now := s.Clock.Now()
	return s.Store.WithTx(ctx, func(tx *store.Tx) error {
		room := domain.MaxCategories - len(have)
		for _, slug := range sug.Categories {
			l, ok := bySlug[slug]
			if !ok || have[slug] || room <= 0 {
				continue
			}
			if err := tx.AddRecipeLabel(ctx, r.ID, l.ID, "llm"); err != nil {
				return err
			}
			have[slug] = true
			room--
		}
		return tx.MarkCategorized(ctx, r.ID, now)
	})
}

// CategorizePending processes up to limit recipes whose categorized_at is NULL and returns how many were done.
// It stops at the first categorizer error (an unreachable model would otherwise fail every recipe in turn).
func (s *Service) CategorizePending(ctx context.Context, limit int) (int, error) {
	if s.Categorizer == nil {
		return 0, nil
	}
	ids, err := s.Store.R().UncategorizedIDs(ctx, limit)
	if err != nil {
		return 0, err
	}
	if sw, ok := s.Categorizer.(interface{ Enabled(context.Context) bool }); ok && !sw.Enabled(ctx) {
		return 0, nil
	}
	n := 0
	for _, id := range ids {
		if err := s.CategorizeRecipe(ctx, id); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
