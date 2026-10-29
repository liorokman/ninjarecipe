package categorize

import (
	"context"
	"net/http"
	"time"
)

// Settings is the effective engine configuration (admin-editable, 70 §4.10).
type Settings struct {
	Enabled   bool
	URL       string
	APIKey    string
	Model     string
	Threshold float64 // 0..1 exclusive
	Timeout   time.Duration
}

// Configured is a Categorizer whose settings are read on every call, so an admin change applies at once and the
// server never needs a restart. It returns ErrDisabled while categorization is off.
type Configured struct {
	Load func(ctx context.Context) (Settings, error)
}

// Engine builds the LocalAI client for the given settings.
func (s Settings) Engine() *LocalAI {
	to := s.Timeout
	if to <= 0 {
		to = 60 * time.Second
	}
	return &LocalAI{BaseURL: s.URL, APIKey: s.APIKey, Model: s.Model, Threshold: s.Threshold, HTTP: &http.Client{Timeout: to}}
}

// Enabled reports whether categorization is currently switched on.
func (c Configured) Enabled(ctx context.Context) bool {
	s, err := c.Load(ctx)
	return err == nil && s.Enabled
}

func (c Configured) Suggest(ctx context.Context, r RecipeSnapshot, cats []CategoryInfo, tags []string) (Suggestion, error) {
	s, err := c.Load(ctx)
	if err != nil {
		return Suggestion{}, err
	}
	if !s.Enabled {
		return Suggestion{}, ErrDisabled
	}
	return s.Engine().Suggest(ctx, r, cats, tags)
}
