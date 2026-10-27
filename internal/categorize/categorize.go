// Package categorize is the extension point for LLM categorization (20 §15). v1 ships only Noop.
package categorize

import "context"

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

// Categorizer suggests labels.
type Categorizer interface {
	Suggest(ctx context.Context, r RecipeSnapshot, knownCategories, knownTags []string) (Suggestion, error)
}

// Noop suggests nothing.
type Noop struct{}

func (Noop) Suggest(context.Context, RecipeSnapshot, []string, []string) (Suggestion, error) {
	return Suggestion{}, nil
}
