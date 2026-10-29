// Package service owns transactions, validation, authorization, FTS maintenance and events (60 §2).
package service

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/liorokman/ninjarecipe/internal/domain"
	"github.com/liorokman/ninjarecipe/internal/events"
	"github.com/liorokman/ninjarecipe/internal/images"
	"github.com/liorokman/ninjarecipe/internal/oven"
	"github.com/liorokman/ninjarecipe/internal/recipetext"
	"github.com/liorokman/ninjarecipe/internal/store"
)

// Service is the application layer.
type Service struct {
	Store     *store.Store
	Clock     domain.Clock
	DataDir   string
	Proc      *images.Processor
	Fetcher   *images.Fetcher
	Bus       *events.Bus
	BaseURL   string
	MaxImages int
	// DefaultLang is APP_DEFAULT_LANG, the fallback language of the assistant brief (40 §8).
	DefaultLang string
}

// New builds a service. Proc and Fetcher may be nil in tests that do not touch images.
func New(st *store.Store, dataDir, baseURL string, clock domain.Clock) *Service {
	if clock == nil {
		clock = domain.SystemClock{}
	}
	return &Service{Store: st, Clock: clock, DataDir: dataDir, BaseURL: baseURL, MaxImages: 10,
		Proc: images.NewProcessor(1, 50_000_000, 15<<20), Fetcher: &images.Fetcher{}}
}

func (s *Service) publish(e events.Event) {
	if s.Bus != nil {
		s.Bus.Publish(e)
	}
}

func (s *Service) base(id string) events.Base {
	return events.Base{RecipeID: id, ActorID: "", At: s.Clock.Now()}
}

func (s *Service) removeFiles(keys []string) {
	for _, k := range keys {
		images.Remove(s.DataDir, k)
	}
}

func (s *Service) logErr(msg string, err error) {
	if err != nil && !errors.Is(err, context.Canceled) {
		slog.Warn(msg, "error", err)
	}
}

func rc(s string) int { return utf8.RuneCountInString(s) }

func issue(field, code string, kv ...any) domain.Issue {
	is := domain.Issue{Field: field, Code: code}
	if len(kv) > 0 {
		is.Params = map[string]any{}
		for i := 0; i+1 < len(kv); i += 2 {
			is.Params[kv[i].(string)] = kv[i+1]
		}
	}
	return is
}

// Validate normalizes in (in place) and returns blocking errors and warnings. cats maps slug -> category;
// strictCats makes unknown categories an error (forms) instead of a dropped warning (API import).
func Validate(in *domain.RecipeInput, cats map[string]domain.Label, strictCats bool) (errs, warns []domain.Issue) {
	errs = append(errs, in.FormIssues...)
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)
	in.Notes = strings.TrimSpace(in.Notes)
	in.SourceURL = strings.TrimSpace(in.SourceURL)
	in.SourceName = strings.TrimSpace(in.SourceName)
	if in.Title == "" {
		errs = append(errs, issue("title", "required"))
	} else if rc(in.Title) > domain.MaxTitle {
		errs = append(errs, issue("title", "too_long", "max", domain.MaxTitle))
	}
	if rc(in.Description) > domain.MaxDescription {
		errs = append(errs, issue("description", "too_long", "max", domain.MaxDescription))
	}
	if rc(in.Notes) > domain.MaxNotes {
		errs = append(errs, issue("notes", "too_long", "max", domain.MaxNotes))
	}
	switch in.Language {
	case "":
		in.Language = domain.LangUnd
	case domain.LangEN, domain.LangHE, domain.LangUnd:
	default:
		errs = append(errs, issue("language", "invalid"))
	}
	if in.Servings != nil && (*in.Servings < 1 || *in.Servings > 100) {
		errs = append(errs, issue("servings", "out_of_range", "min", 1, "max", 100))
	}
	if in.PrepMinutes != nil && *in.PrepMinutes < 0 {
		errs = append(errs, issue("prep_minutes", "out_of_range", "min", 0, "max", 100000))
	}
	if in.SourceURL != "" {
		u, err := url.Parse(in.SourceURL)
		switch {
		case rc(in.SourceURL) > domain.MaxSourceURL:
			errs = append(errs, issue("source_url", "too_long", "max", domain.MaxSourceURL))
		case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
			errs = append(errs, issue("source_url", "invalid"))
		}
	}
	if rc(in.SourceName) > domain.MaxSourceName {
		errs = append(errs, issue("source_name", "too_long", "max", domain.MaxSourceName))
	}
	switch in.Visibility {
	case "", domain.VisPrivate:
		in.Visibility = domain.VisPrivate
	case domain.VisShared:
	default:
		errs = append(errs, issue("visibility", "invalid"))
	}

	// categories
	seen := map[string]bool{}
	var keep []string
	for i, c := range in.Categories {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		if _, ok := cats[c]; !ok {
			if strictCats {
				errs = append(errs, issue("categories", "invalid", "value", c))
			} else {
				warns = append(warns, issue("categories["+itoa(i)+"]", "unknown_category", "value", c))
			}
			continue
		}
		keep = append(keep, c)
	}
	in.Categories = keep
	if len(in.Categories) > domain.MaxCategories {
		errs = append(errs, issue("categories", "too_many", "max", domain.MaxCategories))
	}
	// tags
	var tags []string
	seenT := map[string]bool{}
	for _, t := range in.Tags {
		n, ok := recipetext.NormalizeTag(t)
		if !ok {
			errs = append(errs, issue("tags", "too_long", "max", domain.MaxTag))
			continue
		}
		if !seenT[n] {
			seenT[n] = true
			tags = append(tags, n)
		}
	}
	in.Tags = tags
	if len(in.Tags) > domain.MaxTags {
		errs = append(errs, issue("tags", "too_many", "max", domain.MaxTags))
	}
	// ingredients
	if len(in.Ingredients) > domain.MaxIngredients {
		errs = append(errs, issue("ingredients", "too_many", "max", domain.MaxIngredients))
	}
	for i := range in.Ingredients {
		ing := &in.Ingredients[i]
		ing.Position = i + 1
		ing.Item = strings.TrimSpace(ing.Item)
		if ing.RawText == "" {
			ing.RawText = ing.Item
		}
		f := "ingredients[" + itoa(i) + "]"
		switch {
		case ing.Item == "":
			errs = append(errs, issue(f, "required"))
		case rc(ing.RawText) > domain.MaxIngredient || rc(ing.Item) > domain.MaxIngredient:
			errs = append(errs, issue(f, "too_long", "max", domain.MaxIngredient))
		}
		if ing.Amount != nil && *ing.Amount <= 0 {
			errs = append(errs, issue(f+".amount", "invalid"))
		}
		if ing.Unit != "" && !domain.Contains(domain.Units, ing.Unit) {
			errs = append(errs, issue(f+".unit", "invalid"))
		}
	}
	// steps
	if len(in.Steps) > domain.MaxSteps {
		errs = append(errs, issue("steps", "too_many", "max", domain.MaxSteps))
	}
	for i := range in.Steps {
		st := &in.Steps[i]
		st.Position = i + 1
		st.Text = strings.TrimSpace(st.Text)
		f := "steps[" + itoa(i) + "]"
		if st.Text == "" {
			errs = append(errs, issue(f+".text", "required"))
		} else if rc(st.Text) > domain.MaxStep {
			errs = append(errs, issue(f+".text", "too_long", "max", domain.MaxStep))
		}
		if st.TimerSeconds != nil && (*st.TimerSeconds < 1 || *st.TimerSeconds > 86400) {
			errs = append(errs, issue(f+".timer_seconds", "out_of_range", "min", 1, "max", 86400))
		}
	}
	// oven stages
	for i := range in.Stages {
		oven.Normalize(&in.Stages[i])
		in.Stages[i].Position = i + 1
	}
	e, w := oven.ValidateStages(in.Stages, "oven_stages")
	errs, warns = append(errs, e...), append(warns, w...)
	return errs, warns
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func catMap(cs []domain.Label) map[string]domain.Label {
	m := make(map[string]domain.Label, len(cs))
	for _, c := range cs {
		m[c.Slug] = c
	}
	return m
}
