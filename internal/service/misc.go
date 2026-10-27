package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"cooking/internal/authz"
	"cooking/internal/domain"
	"cooking/internal/images"
	"cooking/internal/store"
)

// ---- search / home ----

// Search runs the list/search for the actor.
func (s *Service) Search(ctx context.Context, actor *domain.User, q domain.SearchQuery) (domain.SearchPage, error) {
	return s.Store.R().Search(ctx, actor.ID, q)
}

// HomeData feeds the home page.
type HomeData struct {
	Tiles      []domain.Label
	Recent     []domain.RecipeSummary
	Favourites []domain.RecipeSummary
}

// Home loads tiles (only categories with a visible recipe), recents (10) and favourites.
func (s *Service) Home(ctx context.Context, actor *domain.User) (*HomeData, error) {
	q := s.Store.R()
	cats, err := q.ListCategories(ctx)
	if err != nil {
		return nil, err
	}
	have, err := q.VisibleCategorySlugs(ctx, actor.ID)
	if err != nil {
		return nil, err
	}
	h := &HomeData{}
	for _, c := range cats {
		if have[c.Slug] {
			h.Tiles = append(h.Tiles, c)
		}
	}
	if h.Recent, err = q.Recent(ctx, actor.ID, 10); err != nil {
		return nil, err
	}
	if h.Favourites, err = q.Favourites(ctx, actor.ID, 12); err != nil {
		return nil, err
	}
	return h, nil
}

// Suggestion is one /labels/suggest entry.
type Suggestion struct {
	Type string `json:"type"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// Suggest returns matching categories (in lang) plus up to 10 tags (D21).
func (s *Service) Suggest(ctx context.Context, actor *domain.User, query, lang string) ([]Suggestion, error) {
	q := s.Store.R()
	cats, err := q.ListCategories(ctx)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	out := []Suggestion{}
	for _, c := range cats {
		if needle == "" || strings.Contains(strings.ToLower(c.Name), needle) || strings.Contains(strings.ToLower(c.NameHe), needle) {
			out = append(out, Suggestion{Type: "category", Slug: c.Slug, Name: c.LocalName(lang)})
		}
	}
	tags, err := q.SuggestTags(ctx, actor.ID, needle, 10)
	if err != nil {
		return nil, err
	}
	for _, t := range tags {
		out = append(out, Suggestion{Type: "tag", Slug: t, Name: t})
	}
	return out, nil
}

// VisibleTags lists tags in use on recipes the actor can view (API /categories).
func (s *Service) VisibleTags(ctx context.Context, actor *domain.User) ([]string, error) {
	return s.Store.R().VisibleTags(ctx, actor.ID)
}

// CookStats exposes stats for one recipe (used by fixtures/handlers).
func (s *Service) Media(ctx context.Context, key string) (*domain.Image, *domain.Recipe, error) {
	q := s.Store.R()
	im, err := q.ImageByKey(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	r, err := q.GetRecipe(ctx, im.RecipeID)
	return im, r, err
}

// MediaFor returns the image and checks view access for actor.
func (s *Service) MediaFor(ctx context.Context, actor *domain.User, key string) (*domain.Image, error) {
	im, r, err := s.Media(ctx, key)
	if err != nil {
		return nil, err
	}
	if !authz.CanView(actor, r) {
		return nil, domain.ErrNotFound
	}
	return im, nil
}

// ---- tokens ----

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// NewTokenString returns "nwr_" + 32 random bytes in base62 (43 chars).
func NewTokenString() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	n := new(big.Int).SetBytes(b)
	base := big.NewInt(62)
	out := make([]byte, 43)
	for i := 42; i >= 0; i-- {
		var m big.Int
		n.DivMod(n, base, &m)
		out[i] = base62[m.Int64()]
	}
	return "nwr_" + string(out)
}

// HashToken is hex(SHA-256(token)).
func HashToken(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// CreateToken makes a token; the plain value is returned once.
func (s *Service) CreateToken(ctx context.Context, actor *domain.User, name string, scopes []string, expiresInDays int) (string, *domain.APIToken, error) {
	name = strings.TrimSpace(name)
	var errs []domain.Issue
	if name == "" {
		errs = append(errs, issue("name", "required"))
	} else if rc(name) > 100 {
		errs = append(errs, issue("name", "too_long", "max", 100))
	}
	seen := map[string]bool{}
	var sc []string
	for _, x := range scopes {
		if (x == domain.ScopeInboxWrite || x == domain.ScopeRecipesRead) && !seen[x] {
			seen[x] = true
			sc = append(sc, x)
		}
	}
	if len(sc) == 0 {
		errs = append(errs, issue("scopes", "required"))
	}
	switch expiresInDays {
	case 0, 30, 90, 365:
	default:
		errs = append(errs, issue("expires_in_days", "invalid"))
	}
	if len(errs) > 0 {
		return "", nil, &domain.ValidationError{Errors: errs}
	}
	now := s.Clock.Now()
	plain := NewTokenString()
	t := &domain.APIToken{ID: uuid.NewString(), UserID: actor.ID, Name: name, Hash: HashToken(plain), Prefix: plain[4:12], Scopes: sc, CreatedAt: now}
	if expiresInDays > 0 {
		e := now.Add(time.Duration(expiresInDays) * 24 * time.Hour)
		t.ExpiresAt = &e
	}
	err := s.Store.WithTx(ctx, func(tx *store.Tx) error {
		n, err := tx.CountActiveTokens(ctx, actor.ID, now)
		if err != nil {
			return err
		}
		if n >= domain.MaxActiveTokens {
			return domain.ErrLimit
		}
		return tx.CreateToken(ctx, *t)
	})
	if err != nil {
		return "", nil, err
	}
	return plain, t, nil
}

// RevokeToken revokes an own token.
func (s *Service) RevokeToken(ctx context.Context, actor *domain.User, id string) error {
	return s.Store.W().RevokeToken(ctx, id, actor.ID, s.Clock.Now())
}

// ListTokens lists own tokens.
func (s *Service) ListTokens(ctx context.Context, actor *domain.User) ([]domain.APIToken, error) {
	return s.Store.R().ListTokens(ctx, actor.ID)
}

// AuthenticateToken validates a bearer token string.
func (s *Service) AuthenticateToken(ctx context.Context, raw string) (*domain.APIToken, *domain.User, error) {
	if !strings.HasPrefix(raw, "nwr_") {
		return nil, nil, domain.ErrNotFound
	}
	q := s.Store.R()
	t, err := q.TokenByHash(ctx, HashToken(raw))
	if err != nil {
		return nil, nil, err
	}
	now := s.Clock.Now()
	if t.Status(now) != "active" {
		return nil, nil, domain.ErrNotFound
	}
	u, err := q.GetUser(ctx, t.UserID)
	if err != nil {
		return nil, nil, err
	}
	_ = s.Store.W().TouchToken(ctx, t.ID, now)
	return t, u, nil
}

// ---- users ----

// OIDCClaims are the mapped claims at login.
type OIDCClaims struct {
	Issuer, Subject, Email, DisplayName string
	IsAdmin                             bool
	Locale                              string // initial language candidate ("", "en", "he")
}

// UpsertOIDCUser provisions or refreshes the user at login (6: ui_language only when unset).
func (s *Service) UpsertOIDCUser(ctx context.Context, c OIDCClaims, fallbackLang string) (*domain.User, error) {
	lang := ""
	switch {
	case strings.HasPrefix(c.Locale, "he"):
		lang = "he"
	case strings.HasPrefix(c.Locale, "en"):
		lang = "en"
	default:
		lang = fallbackLang
	}
	if lang != "en" && lang != "he" {
		lang = ""
	}
	name := c.DisplayName
	if name == "" {
		name = c.Email
	}
	if name == "" {
		name = "User"
	}
	return s.Store.W().UpsertUser(ctx, uuid.NewString(), domain.User{OIDCIssuer: c.Issuer, OIDCSubject: c.Subject, Email: c.Email, DisplayName: name, IsAdmin: c.IsAdmin, UILanguage: lang}, s.Clock.Now())
}

// SetLanguage stores the user's chosen UI language.
func (s *Service) SetLanguage(ctx context.Context, actor *domain.User, lang string) error {
	if lang != "en" && lang != "he" {
		return domain.ErrBadRequest
	}
	return s.Store.W().SetUILanguage(ctx, actor.ID, lang, s.Clock.Now())
}

// DeletePlan describes what the user deletion procedure will remove.
type DeletePlan struct {
	User          *domain.User
	DeleteRecipes int
	Ownerless     int
}

// PlanUserDeletion counts affected recipes.
func (s *Service) PlanUserDeletion(ctx context.Context, u *domain.User) (*DeletePlan, error) {
	q := s.Store.R()
	priv, err := q.UserRecipeIDs(ctx, u.ID, false)
	if err != nil {
		return nil, err
	}
	sh, err := q.UserRecipeIDs(ctx, u.ID, true)
	if err != nil {
		return nil, err
	}
	return &DeletePlan{User: u, DeleteRecipes: len(priv), Ownerless: len(sh)}, nil
}

// DeleteUser runs the procedure of 30 §3 in one transaction.
func (s *Service) DeleteUser(ctx context.Context, u *domain.User) error {
	return s.Store.WithTx(ctx, func(tx *store.Tx) error {
		priv, err := tx.UserRecipeIDs(ctx, u.ID, false)
		if err != nil {
			return err
		}
		for _, id := range priv {
			keys, err := tx.DeleteRecipe(ctx, id)
			if err != nil {
				return err
			}
			tx.AfterCommit(func() { s.removeFiles(keys) })
		}
		sh, err := tx.UserRecipeIDs(ctx, u.ID, true)
		if err != nil {
			return err
		}
		for _, id := range sh {
			if err := tx.SetOwnerless(ctx, id); err != nil {
				return err
			}
		}
		// the user's cook-log photos on other recipes go away with their logs
		rows, err := tx.UserCookLogImageKeys(ctx, u.ID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecRaw(ctx, `DELETE FROM recipe_images WHERE id IN (SELECT image_id FROM recipe_cook_logs WHERE user_id=? AND image_id IS NOT NULL)`, u.ID); err != nil {
			return err
		}
		if err := tx.DeleteUserRow(ctx, u.ID); err != nil {
			return err
		}
		tx.AfterCommit(func() { s.removeFiles(rows) })
		return nil
	})
}

// ---- admin categories ----

var slugRe = regexp.MustCompile(`^[a-z0-9_]{2,40}$`)

// CategoryInput is the admin form.
type CategoryInput struct {
	Slug, Name, NameHe, Icon string
	SortOrder                int
}

func validateCategory(in *CategoryInput, creating bool) []domain.Issue {
	var errs []domain.Issue
	in.Name, in.NameHe, in.Icon = strings.TrimSpace(in.Name), strings.TrimSpace(in.NameHe), strings.TrimSpace(in.Icon)
	if creating && !slugRe.MatchString(in.Slug) {
		errs = append(errs, issue("slug", "invalid"))
	}
	if in.Name == "" {
		errs = append(errs, issue("name", "required"))
	} else if rc(in.Name) > 40 {
		errs = append(errs, issue("name", "too_long", "max", 40))
	}
	if in.NameHe == "" {
		errs = append(errs, issue("name_he", "required"))
	} else if rc(in.NameHe) > 40 {
		errs = append(errs, issue("name_he", "too_long", "max", 40))
	}
	if rc(in.Icon) > 8 {
		errs = append(errs, issue("icon", "too_long", "max", 8))
	}
	return errs
}

func requireAdmin(a *domain.User) error {
	if !authz.CanAdmin(a) {
		return domain.ErrForbidden
	}
	return nil
}

// CreateCategory adds a category (admin).
func (s *Service) CreateCategory(ctx context.Context, actor *domain.User, in CategoryInput) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	if errs := validateCategory(&in, true); len(errs) > 0 {
		return &domain.ValidationError{Errors: errs}
	}
	_, err := s.Store.W().CreateCategory(ctx, domain.Label{Slug: in.Slug, Name: in.Name, NameHe: in.NameHe, Icon: in.Icon, SortOrder: in.SortOrder}, actor.ID, s.Clock.Now())
	if err == domain.ErrConflict {
		return &domain.ValidationError{Errors: []domain.Issue{issue("slug", "duplicate")}}
	}
	return err
}

// UpdateCategory renames/reorders a category and re-indexes affected recipes.
func (s *Service) UpdateCategory(ctx context.Context, actor *domain.User, id int64, in CategoryInput) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	if errs := validateCategory(&in, false); len(errs) > 0 {
		return &domain.ValidationError{Errors: errs}
	}
	return s.Store.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.UpdateCategory(ctx, domain.Label{ID: id, Name: in.Name, NameHe: in.NameHe, Icon: in.Icon, SortOrder: in.SortOrder}); err != nil {
			return err
		}
		return s.reindexLabel(ctx, tx, id)
	})
}

func (s *Service) reindexLabel(ctx context.Context, tx *store.Tx, id int64) error {
	ids, err := tx.LabelRecipeIDs(ctx, id)
	if err != nil {
		return err
	}
	for _, rid := range ids {
		if err := tx.ReindexRecipe(ctx, rid); err != nil {
			return err
		}
	}
	return nil
}

// DeleteCategory removes a category; recipes stay.
func (s *Service) DeleteCategory(ctx context.Context, actor *domain.User, id int64) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	return s.Store.WithTx(ctx, func(tx *store.Tx) error {
		ids, err := tx.LabelRecipeIDs(ctx, id)
		if err != nil {
			return err
		}
		if err := tx.DeleteCategory(ctx, id); err != nil {
			return err
		}
		for _, rid := range ids {
			if err := tx.ReindexRecipe(ctx, rid); err != nil {
				return err
			}
		}
		return nil
	})
}

// AdminCategories lists categories with usage counts.
func (s *Service) AdminCategories(ctx context.Context, actor *domain.User) ([]domain.Label, map[int64]int, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, nil, err
	}
	q := s.Store.R()
	cs, err := q.ListCategories(ctx)
	if err != nil {
		return nil, nil, err
	}
	counts, err := q.CategoryCounts(ctx)
	return cs, counts, err
}

// ---- maintenance ----

// Reindex rebuilds the whole FTS table.
func (s *Service) Reindex(ctx context.Context) (int, error) {
	n := 0
	err := s.Store.WithTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.ExecRaw(ctx, `DELETE FROM recipe_fts`); err != nil {
			return err
		}
		ids, err := tx.AllRecipeIDs(ctx)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := tx.ReindexRecipe(ctx, id); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

// Sweep deletes unreferenced media older than an hour and stale tmp files.
func (s *Service) Sweep(ctx context.Context) (int, error) {
	keys, err := s.Store.R().AllImageKeys(ctx)
	if err != nil {
		return 0, err
	}
	return images.Sweep(ctx, s.DataDir, keys, time.Hour, s.Clock.Now()), nil
}

// PurgeHourly deletes expired sessions/login states and orphan tags.
func (s *Service) PurgeHourly(ctx context.Context) (int64, error) {
	n, err := s.Store.W().PurgeExpired(ctx, s.Clock.Now())
	if err != nil {
		return n, err
	}
	m, err := s.Store.W().PurgeOrphanTags(ctx)
	return n + m, err
}
