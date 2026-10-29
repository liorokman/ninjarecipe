package categorize

import (
	"context"
	"testing"
)

func TestNoop(t *testing.T) {
	var c Categorizer = Noop{}
	s, err := c.Suggest(context.Background(), RecipeSnapshot{Title: "x"}, []CategoryInfo{{Slug: "beef"}}, nil)
	if err != nil || len(s.Categories) != 0 || s.Confidence != 0 {
		t.Fatal(s, err)
	}
}
