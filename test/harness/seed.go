package harness

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"

	"github.com/liorokman/ninjarecipe/internal/domain"
	"github.com/liorokman/ninjarecipe/internal/service"
	"github.com/liorokman/ninjarecipe/internal/web/fixtures"
)

// JPEG makes a deterministic test photo.
func JPEG(w, h int, seed int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8((x*255)/w + seed*40), uint8((y*255)/h + seed*20), uint8(120 + seed*30), 255})
		}
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: 80})
	return b.Bytes()
}

// Seeded describes the data created by SeedUI.
type Seeded struct {
	Users   map[string]*domain.User
	Cookies map[string]*http.Cookie
	Recipes map[string]string // fixture name -> real id
	Token   string            // alice's API token (inbox:write recipes:read)
}

var fixtureNames = map[string]string{"fx-ribs": "ribs", "fx-chicken": "chicken", "fx-pizza-he": "hebrew", "fx-ownerless": "ownerless",
	"fx-bread": "bread", "fx-nostage": "noStages", "fx-bob-private": "bobPrivate", "fx-inbox-1": "inbox1", "fx-inbox-2": "inbox2"}

// SeedUI creates alice, bob and admin plus the fixture recipes through the real service layer.
func (h *Harness) SeedUI() (*Seeded, error) {
	ctx := context.Background()
	fx := fixtures.New(h.nowFn())
	s := &Seeded{Users: map[string]*domain.User{}, Cookies: map[string]*http.Cookie{}, Recipes: map[string]string{}}
	names := map[string]string{"u-alice": "alice", "u-bob": "bob", "u-admin": "admin"}
	for key, fu := range fx.Users {
		u := h.CreateUser(key, fu.Email, fu.IsAdmin, "")
		s.Users[key] = u
		s.Cookies[key] = h.SessionCookie(u)
	}
	svc := h.Svc()
	byFixtureUser := func(id string) *domain.User { return s.Users[names[id]] }

	toInput := func(r *domain.Recipe) domain.RecipeInput {
		in := domain.RecipeInput{Title: r.Title, Description: r.Description, Notes: r.Notes, Language: r.Language, Servings: r.Servings,
			PrepMinutes: r.PrepMinutes, SourceURL: r.SourceURL, SourceName: r.SourceName, Visibility: "private"}
		for _, l := range r.Labels {
			if l.Type == "category" {
				in.Categories = append(in.Categories, l.Slug)
			} else {
				in.Tags = append(in.Tags, l.Name)
			}
		}
		in.Ingredients = append(in.Ingredients, r.Ingredients...)
		in.Steps = append(in.Steps, r.Steps...)
		for _, st := range r.Stages {
			in.Stages = append(in.Stages, st)
		}
		return in
	}
	for i, r := range fx.Recipes {
		owner := byFixtureUser(r.OwnerID)
		if owner == nil {
			owner = s.Users["alice"]
		}
		created, _, err := svc.CreateRecipe(ctx, owner, toInput(r))
		if err != nil {
			return nil, err
		}
		s.Recipes[fixtureNames[r.ID]] = created.ID
		for n, im := range r.Images {
			_ = im
			if _, err := svc.AddImage(ctx, owner, created.ID, bytes.NewReader(JPEG(1200, 900, i*3+n))); err != nil {
				return nil, err
			}
		}
		if r.Visibility == "shared" {
			if err := svc.SetVisibility(ctx, owner, created.ID, true); err != nil {
				return nil, err
			}
		}
		if r.OwnerID == "" {
			if _, err := svc.Store.W().ExecRaw(ctx, `UPDATE recipes SET owner_id=NULL WHERE id=?`, created.ID); err != nil {
				return nil, err
			}
		}
		for _, l := range reverseLogs(fx.Logs[r.ID]) {
			author := byFixtureUser(l.UserID)
			in := service.CookLogInput{Note: l.Note, Rating: l.Rating}
			if l.Image != nil {
				in.Photo = bytes.NewReader(JPEG(800, 600, 7))
			}
			if _, err := svc.LogCook(ctx, author, created.ID, in); err != nil {
				return nil, err
			}
		}
	}
	for uid, favs := range fx.Favs {
		for rid := range favs {
			if id := s.Recipes[fixtureNames[rid]]; id != "" {
				if err := svc.SetFavourite(ctx, byFixtureUser(uid), id, true); err != nil {
					return nil, err
				}
			}
		}
	}
	alice := s.Users["alice"]
	for _, in := range fx.Inbox {
		imp := service.RecipeImport{Title: in.Title, Language: in.Language, SourceURL: in.SourceURL, SourceName: in.SourceName, Categories: []string{"lamb"}}
		for _, ing := range in.Ingredients {
			imp.Ingredients = append(imp.Ingredients, service.ImportIngredient{Text: ing.RawText})
		}
		for _, st := range in.Steps {
			imp.Steps = append(imp.Steps, service.ImportStep{Text: st.Text})
		}
		for _, st := range in.Stages {
			imp.OvenStages = append(imp.OvenStages, service.ImportStage{Mode: st.Mode, TemperatureC: st.TemperatureC, DurationSeconds: st.DurationSeconds})
		}
		if len(in.Images) > 0 {
			imp.Images = append(imp.Images, service.ImportImage{DataBase64: base64.StdEncoding.EncodeToString(JPEG(1000, 750, 9))})
		}
		res, err := svc.ImportToInbox(ctx, alice, imp, "", false)
		if err != nil {
			return nil, err
		}
		s.Recipes[fixtureNames[in.ID]] = res.ID
	}
	s.Token = h.Token(alice)
	return s, nil
}

func reverseLogs(l []domain.CookLog) []domain.CookLog {
	out := make([]domain.CookLog, len(l))
	for i := range l {
		out[len(l)-1-i] = l[i]
	}
	return out
}
