package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"cooking/internal/authz"
	"cooking/internal/domain"
	"cooking/internal/events"
	"cooking/internal/images"
	"cooking/internal/recipetext"
	"cooking/internal/store"
)

// ---- API import types (40 §4.1) ----

type ImportIngredient struct {
	Amount  *float64 `json:"amount"`
	Unit    *string  `json:"unit"`
	Item    string   `json:"item"`
	Section *string  `json:"section"`
	Text    string   `json:"text"`
}

type ImportStep struct {
	Text         string `json:"text"`
	TimerSeconds *int   `json:"timer_seconds"`
}

// ImportStage mirrors OvenStage JSON of 25 §2 (null/omitted = unset).
type ImportStage struct {
	Mode             string   `json:"mode"`
	PizzaPreset      *string  `json:"pizza_preset"`
	TemperatureC     *int     `json:"temperature_c"`
	DurationSeconds  *int     `json:"duration_seconds"`
	Temperature2C    *int     `json:"temperature2_c"`
	Duration2Seconds *int     `json:"duration2_seconds"`
	PreheatMinutes   *int     `json:"preheat_minutes"`
	Woodfire         bool     `json:"woodfire"`
	PelletBlend      *string  `json:"pellet_blend"`
	PelletNote       *string  `json:"pellet_note"`
	Accessories      []string `json:"accessories"`
	TargetCoreTempC  *int     `json:"target_core_temp_c"`
	Notes            string   `json:"notes"`
}

type ImportImage struct {
	URL        string `json:"url"`
	DataBase64 string `json:"data_base64"`
	Caption    string `json:"caption"`
}

// RecipeImport is the POST /api/v1/inbox body.
type RecipeImport struct {
	Title       string             `json:"title"`
	Description string             `json:"description"`
	Notes       string             `json:"notes"`
	Language    string             `json:"language"`
	Servings    *int               `json:"servings"`
	PrepMinutes *int               `json:"prep_minutes"`
	SourceURL   string             `json:"source_url"`
	SourceName  string             `json:"source_name"`
	Categories  []string           `json:"categories"`
	Tags        []string           `json:"tags"`
	Ingredients []ImportIngredient `json:"ingredients"`
	Steps       []ImportStep       `json:"steps"`
	OvenStages  []ImportStage      `json:"oven_stages"`
	Images      []ImportImage      `json:"images"`
}

// ImportResult is the response of POST /api/v1/inbox.
type ImportResult struct {
	ID        string         `json:"id"`
	Status    string         `json:"status"`
	Title     string         `json:"title"`
	ReviewURL string         `json:"review_url"`
	CreatedAt string         `json:"created_at"`
	Warnings  []domain.Issue `json:"warnings"`
	Created   bool           `json:"-"` // false on idempotent replay
}

func strOr(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

// ToInput converts the import payload into the shared write model.
func (ri RecipeImport) ToInput() domain.RecipeInput {
	in := domain.RecipeInput{Title: ri.Title, Description: ri.Description, Notes: ri.Notes, Language: ri.Language, Servings: ri.Servings,
		PrepMinutes: ri.PrepMinutes, SourceURL: ri.SourceURL, SourceName: ri.SourceName, Categories: ri.Categories, Tags: ri.Tags,
		Visibility: domain.VisPrivate, LabelSource: "assistant"}
	for i, g := range ri.Ingredients {
		var ing domain.Ingredient
		if strings.TrimSpace(g.Text) != "" {
			ing = recipetext.ParseIngredientLine(g.Text)
		} else {
			ing = domain.Ingredient{Amount: g.Amount, Unit: strOr(g.Unit), Item: strings.TrimSpace(g.Item)}
			parts := []string{}
			if g.Amount != nil {
				parts = append(parts, strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", *g.Amount), "0"), "."))
			}
			if ing.Unit != "" {
				parts = append(parts, ing.Unit)
			}
			parts = append(parts, ing.Item)
			ing.RawText = strings.Join(parts, " ")
		}
		if g.Section != nil {
			ing.Section = strings.TrimSpace(*g.Section)
		}
		_ = i
		in.Ingredients = append(in.Ingredients, ing)
	}
	for _, st := range ri.Steps {
		in.Steps = append(in.Steps, domain.Step{Text: st.Text, TimerSeconds: st.TimerSeconds})
	}
	for _, st := range ri.OvenStages {
		in.Stages = append(in.Stages, domain.OvenStage{Mode: st.Mode, PizzaPreset: strOr(st.PizzaPreset), TemperatureC: st.TemperatureC,
			DurationSeconds: st.DurationSeconds, Temperature2C: st.Temperature2C, Duration2Seconds: st.Duration2Seconds,
			PreheatMinutes: st.PreheatMinutes, Woodfire: st.Woodfire, PelletBlend: strOr(st.PelletBlend), PelletNote: strOr(st.PelletNote),
			Accessories: st.Accessories, TargetCoreTempC: st.TargetCoreTempC, Notes: st.Notes})
	}
	return in
}

func (s *Service) reviewURL(id string) string { return s.BaseURL + "/inbox/" + id }

func (s *Service) resultOf(r *domain.Recipe, warns []domain.Issue, created bool) *ImportResult {
	if warns == nil {
		warns = []domain.Issue{}
	}
	return &ImportResult{ID: r.ID, Status: r.Status, Title: r.Title, ReviewURL: s.reviewURL(r.ID), CreatedAt: store.FormatTime(r.CreatedAt), Warnings: warns, Created: created}
}

// ImportToInbox implements 40 §4. allowDup skips the source_url duplicate check.
func (s *Service) ImportToInbox(ctx context.Context, actor *domain.User, ri RecipeImport, idemKey string, allowDup bool) (*ImportResult, error) {
	q := s.Store.R()
	if idemKey != "" {
		if r, err := q.FindByImportKey(ctx, actor.ID, idemKey); err == nil {
			return s.resultOf(r, nil, false), nil
		} else if !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
	}
	in := ri.ToInput()
	if in.SourceURL != "" && !allowDup {
		if id, err := q.FindBySourceURL(ctx, actor.ID, strings.TrimSpace(in.SourceURL)); err == nil {
			return nil, domain.ErrDuplicate{ExistingID: id}
		} else if !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
	}
	cats, err := s.categories(ctx, q)
	if err != nil {
		return nil, err
	}
	errs, warns := Validate(&in, cats, false)
	if len(ri.Images) > s.MaxImages {
		errs = append(errs, issue("images", "too_many", "max", s.MaxImages))
	}
	if err := validationErr(errs, warns); err != nil {
		return nil, err
	}

	// images, synchronously, in order; failures become warnings
	now := s.Clock.Now()
	rid := uuid.NewString()
	var imgs []domain.Image
	for i, im := range ri.Images {
		name := fmt.Sprintf("images[%d]", i)
		var data []byte
		switch {
		case im.URL != "":
			data, err = s.Fetcher.Fetch(ctx, im.URL)
			if err != nil {
				var fe *images.FetchError
				if errors.As(err, &fe) {
					p := map[string]any{}
					if fe.Status != 0 {
						p["status"] = fe.Status
					}
					warns = append(warns, domain.Issue{Field: name, Code: fe.Code, Params: p})
				} else {
					warns = append(warns, domain.Issue{Field: name, Code: "fetch_failed"})
				}
				continue
			}
		case im.DataBase64 != "":
			data, err = base64.StdEncoding.DecodeString(im.DataBase64)
			if err != nil {
				warns = append(warns, domain.Issue{Field: name, Code: "invalid"})
				continue
			}
		default:
			warns = append(warns, domain.Issue{Field: name, Code: "invalid"})
			continue
		}
		p, err := s.Proc.Process(ctx, strings.NewReader(string(data)))
		if err != nil {
			code := "invalid"
			switch {
			case errors.Is(err, domain.ErrTooLarge):
				code = "too_large"
			case errors.Is(err, domain.ErrUnsupportedMedia):
				code = "unsupported_type"
			case errors.Is(err, domain.ErrBusy):
				code = "busy"
			}
			warns = append(warns, domain.Issue{Field: name, Code: code})
			continue
		}
		key, err := images.Store(s.DataDir, p)
		if err != nil {
			warns = append(warns, domain.Issue{Field: name, Code: "invalid"})
			continue
		}
		imgs = append(imgs, domain.Image{ID: uuid.NewString(), RecipeID: rid, Key: key, Kind: "recipe", Width: p.Width, Height: p.Height,
			Bytes: p.Bytes(), Caption: strings.TrimSpace(im.Caption), SourceURL: im.URL, CreatedAt: now})
	}

	r := &domain.Recipe{ID: rid, OwnerID: actor.ID, Status: domain.StatusInbox, Visibility: domain.VisPrivate, Title: in.Title, Description: in.Description,
		Notes: in.Notes, Language: in.Language, Servings: in.Servings, PrepMinutes: in.PrepMinutes, SourceURL: in.SourceURL, SourceName: in.SourceName,
		ImportKey: idemKey, Version: 1, CreatedBy: actor.ID, UpdatedBy: actor.ID, CreatedAt: now, UpdatedAt: now,
		Ingredients: in.Ingredients, Steps: in.Steps, Stages: in.Stages}
	err = s.Store.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.InsertRecipe(ctx, r); err != nil {
			return err
		}
		ids, err := s.labelIDs(ctx, tx, in, cats, actor.ID, now)
		if err != nil {
			return err
		}
		if err := tx.SetRecipeLabels(ctx, r.ID, ids, "assistant"); err != nil {
			return err
		}
		for i := range imgs {
			if err := tx.InsertImage(ctx, &imgs[i]); err != nil {
				return err
			}
		}
		tx.AfterCommit(func() {
			s.publish(events.InboxItemReceived{Base: events.Base{RecipeID: r.ID, ActorID: actor.ID, At: now}})
		})
		return nil
	})
	if err != nil {
		for _, im := range imgs {
			images.Remove(s.DataDir, im.Key)
		}
		// a concurrent identical idempotency key lost the race: replay
		if idemKey != "" {
			if ex, e2 := q.FindByImportKey(ctx, actor.ID, idemKey); e2 == nil {
				return s.resultOf(ex, nil, false), nil
			}
		}
		return nil, err
	}
	return s.resultOf(r, warns, true), nil
}

// ListInbox lists the actor's pending items (oldest first).
func (s *Service) ListInbox(ctx context.Context, actor *domain.User) ([]domain.RecipeSummary, error) {
	q := s.Store.R()
	ids, err := q.InboxIDs(ctx, actor.ID)
	if err != nil {
		return nil, err
	}
	return q.Summaries(ctx, actor.ID, ids)
}

// InboxCount returns the number of pending items.
func (s *Service) InboxCount(ctx context.Context, actor *domain.User) int {
	n, _ := s.Store.R().CountInbox(ctx, actor.ID)
	return n
}

// GetInboxItem loads an own inbox item and the id of the next-oldest other item ("" if none).
func (s *Service) GetInboxItem(ctx context.Context, actor *domain.User, id string) (*domain.Recipe, string, error) {
	q := s.Store.R()
	r, err := s.load(ctx, q, actor, id)
	if err != nil {
		return nil, "", err
	}
	if r.Status != domain.StatusInbox {
		return nil, "", domain.ErrNotFound
	}
	ids, err := q.InboxIDs(ctx, actor.ID)
	if err != nil {
		return nil, "", err
	}
	next := ""
	for _, x := range ids {
		if x != id {
			next = x
			break
		}
	}
	return r, next, nil
}

func (s *Service) nextInbox(ctx context.Context, q store.Q, actor *domain.User, excluding string) string {
	ids, _ := q.InboxIDs(ctx, actor.ID)
	for _, x := range ids {
		if x != excluding {
			return x
		}
	}
	return ""
}

// AcceptInbox keeps an inbox item (optionally shared) and returns the next pending id.
func (s *Service) AcceptInbox(ctx context.Context, actor *domain.User, id string, share bool) (string, error) {
	next := ""
	err := s.Store.WithTx(ctx, func(tx *store.Tx) error {
		r, err := s.load(ctx, tx.Q, actor, id)
		if err != nil {
			return err
		}
		if !authz.CanAcceptReject(actor, r) {
			return domain.ErrNotFound
		}
		vis := domain.VisPrivate
		if share {
			vis = domain.VisShared
		}
		now := s.Clock.Now()
		if err := tx.ActivateInbox(ctx, id, vis, actor.ID, now); err != nil {
			return err
		}
		if err := tx.ReindexRecipe(ctx, id); err != nil {
			return err
		}
		next = s.nextInbox(ctx, tx.Q, actor, id)
		tx.AfterCommit(func() {
			s.publish(events.InboxItemAccepted{Base: events.Base{RecipeID: id, ActorID: actor.ID, At: now}})
		})
		return nil
	})
	return next, err
}

// EditAndAcceptInbox saves the edits and keeps the item in one transaction.
func (s *Service) EditAndAcceptInbox(ctx context.Context, actor *domain.User, id string, version int, in domain.RecipeInput, share bool) (string, []domain.Issue, error) {
	_, warns, err := s.update(ctx, actor, id, version, in, true, share)
	if err != nil {
		return "", warns, err
	}
	return s.nextInbox(ctx, s.Store.R(), actor, id), warns, nil
}

// RejectInbox throws an item away (hard delete) and returns the next pending id.
func (s *Service) RejectInbox(ctx context.Context, actor *domain.User, id string) (string, error) {
	next := ""
	err := s.Store.WithTx(ctx, func(tx *store.Tx) error {
		r, err := s.load(ctx, tx.Q, actor, id)
		if err != nil {
			return err
		}
		if !authz.CanAcceptReject(actor, r) {
			return domain.ErrNotFound
		}
		keys, err := tx.DeleteRecipe(ctx, id)
		if err != nil {
			return err
		}
		next = s.nextInbox(ctx, tx.Q, actor, id)
		tx.AfterCommit(func() { s.removeFiles(keys) })
		return nil
	})
	return next, err
}

// WithdrawInbox is the API variant: deletes an own pending item, 404 once accepted.
func (s *Service) WithdrawInbox(ctx context.Context, actor *domain.User, id string) error {
	_, err := s.RejectInbox(ctx, actor, id)
	return err
}

var _ = time.Second
