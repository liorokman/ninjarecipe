// Package categorize is the extension point for LLM categorization (20 §15). Noop and LocalAI are provided.
package categorize

import (
	"context"
	"errors"
)

// RecipeSnapshot is what a categorizer sees.
type RecipeSnapshot struct {
	ID, Language, Title, Description string
	Ingredients, Steps, OvenModes    []string
	Categories, Tags                 []string // current
}

// Suggestion is the categorizer's answer.
type Suggestion struct {
	Categories []string // known category slugs only
	Tags       []string
	Confidence float64 // 0..1
	Model      string
}

// CategoryInfo describes one category the categorizer may choose. Description is the admin's optional hint
// about what belongs in it.
type CategoryInfo struct{ Slug, Name, Description string }

// ErrDisabled means categorization is switched off; callers skip quietly and leave the recipe unprocessed.
var ErrDisabled = errors.New("categorization is disabled")

// Categorizer suggests labels.
type Categorizer interface {
	Suggest(ctx context.Context, r RecipeSnapshot, knownCategories []CategoryInfo, knownTags []string) (Suggestion, error)
}

// Noop suggests nothing.
type Noop struct{}

func (Noop) Suggest(context.Context, RecipeSnapshot, []CategoryInfo, []string) (Suggestion, error) {
	return Suggestion{}, nil
}
