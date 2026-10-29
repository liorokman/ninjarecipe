package categorize

import (
	"context"
	"os"
	"testing"
)

// TestLive talks to a real LocalAI when LOCALAI_URL is set (go test -run Live ./internal/categorize).
func TestLive(t *testing.T) {
	u := os.Getenv("LOCALAI_URL")
	if u == "" {
		t.Skip("LOCALAI_URL not set")
	}
	l := &LocalAI{BaseURL: u, APIKey: os.Getenv("LOCALAI_API_KEY"), Model: os.Getenv("LOCALAI_MODEL")}
	s, err := l.Suggest(context.Background(), RecipeSnapshot{Title: "Smoked salmon with dill", Ingredients: []string{"salmon fillet", "dill", "lemon"}},
		[]CategoryInfo{{Slug: "pizza", Name: "Pizza"}, {Slug: "beef", Name: "Beef"}, {Slug: "fish_seafood", Name: "Fish & Seafood"}, {Slug: "desserts", Name: "Desserts"}, {Slug: "vegetables", Name: "Vegetables"}}, nil)
	t.Logf("%+v err=%v", s, err)
	if err != nil {
		t.Fatal(err)
	}
}
