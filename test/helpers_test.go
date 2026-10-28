package test

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/liorokman/ninjarecipe/internal/domain"
	"github.com/liorokman/ninjarecipe/test/harness"
)

func nowUTC() time.Time        { return time.Now().UTC() }
func secs(n int) time.Duration { return time.Duration(n) * time.Second }
func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

func ip(i int) *int { return &i }

func seedRecipe(t testing.TB, h *harness.Harness, u *domain.User, title string) *domain.Recipe {
	t.Helper()
	r, _, err := h.Svc().CreateRecipe(context.Background(), u, domain.RecipeInput{Title: title, Language: "en",
		Stages:      []domain.OvenStage{{Mode: "max_roast", TemperatureC: ip(250), DurationSeconds: ip(600)}},
		Ingredients: []domain.Ingredient{{Item: "salt", RawText: "salt"}}, Steps: []domain.Step{{Text: "cook"}}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

var ctxb = context.Background()

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

func fmtInt(n int64) string { return strconv.FormatInt(n, 10) }
