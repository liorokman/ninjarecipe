package service

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/liorokman/ninjarecipe/internal/categorize"
	"github.com/liorokman/ninjarecipe/internal/domain"
	"github.com/liorokman/ninjarecipe/internal/store"
)

// Keys of the categorizer rows in app_settings. A stored value overrides the environment default.
const (
	setEnabled   = "categorizer.enabled"
	setURL       = "categorizer.url"
	setAPIKey    = "categorizer.api_key"
	setModel     = "categorizer.model"
	setThreshold = "categorizer.threshold"
)

// CategorizerSettings returns the effective engine settings: environment defaults overridden by what an admin saved.
func (s *Service) CategorizerSettings(ctx context.Context) (categorize.Settings, error) {
	eff := s.CatDefaults
	m, err := s.Store.R().Settings(ctx, "categorizer.")
	if err != nil {
		return eff, err
	}
	if v, ok := m[setEnabled]; ok {
		eff.Enabled = v == "true"
	}
	if v, ok := m[setURL]; ok {
		eff.URL = v
	}
	if v, ok := m[setAPIKey]; ok {
		eff.APIKey = v
	}
	if v, ok := m[setModel]; ok {
		eff.Model = v
	}
	if v, ok := m[setThreshold]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 && f < 1 {
			eff.Threshold = f
		}
	}
	if eff.Threshold <= 0 || eff.Threshold >= 1 {
		eff.Threshold = 0.5
	}
	return eff, nil
}

// CategorizerAdmin is what the admin page shows. The API key is never returned, only whether one is set.
type CategorizerAdmin struct {
	Enabled      bool
	URL, Model   string
	ThresholdPct int
	KeySet       bool
}

// AdminCategorizer returns the effective settings for the admin form.
func (s *Service) AdminCategorizer(ctx context.Context, actor *domain.User) (CategorizerAdmin, error) {
	if err := requireAdmin(actor); err != nil {
		return CategorizerAdmin{}, err
	}
	e, err := s.CategorizerSettings(ctx)
	if err != nil {
		return CategorizerAdmin{}, err
	}
	return CategorizerAdmin{Enabled: e.Enabled, URL: e.URL, Model: e.Model, ThresholdPct: int(e.Threshold*100 + 0.5), KeySet: e.APIKey != ""}, nil
}

// CategorizerInput is the admin form. An empty APIKey keeps the stored key; ClearKey removes it.
type CategorizerInput struct {
	Enabled      bool
	URL, Model   string
	APIKey       string
	ClearKey     bool
	ThresholdPct string
}

// SaveCategorizer validates and stores the engine settings (admin).
func (s *Service) SaveCategorizer(ctx context.Context, actor *domain.User, in CategorizerInput) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	in.URL, in.Model, in.APIKey = strings.TrimRight(strings.TrimSpace(in.URL), "/"), strings.TrimSpace(in.Model), strings.TrimSpace(in.APIKey)
	var errs []domain.Issue
	if in.URL != "" {
		if u, err := url.Parse(in.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, issue("url", "invalid"))
		}
	} else if in.Enabled {
		errs = append(errs, issue("url", "required"))
	}
	if in.Model == "" {
		errs = append(errs, issue("model", "required"))
	} else if rc(in.Model) > 100 {
		errs = append(errs, issue("model", "too_long", "max", 100))
	}
	pct, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(in.ThresholdPct), "%"))
	if err != nil || pct < 1 || pct > 99 {
		errs = append(errs, issue("threshold", "invalid"))
	}
	if rc(in.APIKey) > 500 {
		errs = append(errs, issue("api_key", "too_long", "max", 500))
	}
	if len(errs) > 0 {
		return &domain.ValidationError{Errors: errs}
	}
	now := s.Clock.Now()
	return s.Store.WithTx(ctx, func(tx *store.Tx) error {
		for k, v := range map[string]string{setEnabled: strconv.FormatBool(in.Enabled), setURL: in.URL, setModel: in.Model,
			setThreshold: strconv.FormatFloat(float64(pct)/100, 'f', -1, 64)} {
			if err := tx.SetSetting(ctx, k, v, actor.ID, now); err != nil {
				return err
			}
		}
		switch {
		case in.ClearKey:
			return tx.SetSetting(ctx, setAPIKey, "", actor.ID, now) // stored empty: also overrides an environment key
		case in.APIKey != "":
			return tx.SetSetting(ctx, setAPIKey, in.APIKey, actor.ID, now)
		}
		return nil
	})
}

// CategorizerTest is the outcome of the admin "Test connection" button.
type CategorizerTest struct {
	Model      string
	Categories []string // names of the categories the sample recipe matched
	Confidence float64
}

// TestCategorizer sends a sample recipe to the saved engine, whether or not it is enabled, and reports what came back.
func (s *Service) TestCategorizer(ctx context.Context, actor *domain.User) (CategorizerTest, error) {
	if err := requireAdmin(actor); err != nil {
		return CategorizerTest{}, err
	}
	e, err := s.CategorizerSettings(ctx)
	if err != nil {
		return CategorizerTest{}, err
	}
	if e.URL == "" {
		return CategorizerTest{}, fmt.Errorf("no URL is set")
	}
	cats, err := s.Store.R().ListCategories(ctx)
	if err != nil {
		return CategorizerTest{}, err
	}
	sug, err := e.Engine().Suggest(ctx, categorize.RecipeSnapshot{Title: "Margherita pizza", Language: "en",
		Ingredients: []string{"pizza dough", "tomato sauce", "mozzarella", "basil"}, OvenModes: []string{"pizza"}}, categoryInfos(cats), nil)
	if err != nil {
		return CategorizerTest{}, err
	}
	out := CategorizerTest{Model: sug.Model, Confidence: sug.Confidence}
	for _, slug := range sug.Categories {
		for _, c := range cats {
			if c.Slug == slug {
				out.Categories = append(out.Categories, c.Name)
			}
		}
	}
	return out, nil
}

func categoryInfos(cats []domain.Label) []categorize.CategoryInfo {
	out := make([]categorize.CategoryInfo, len(cats))
	for i, c := range cats {
		out[i] = categorize.CategoryInfo{Slug: c.Slug, Name: c.Name, Description: c.Description}
	}
	return out
}
