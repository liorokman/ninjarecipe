package service

import (
	"context"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/liorokman/ninjarecipe/internal/authz"
	"github.com/liorokman/ninjarecipe/internal/domain"
	"github.com/liorokman/ninjarecipe/internal/events"
	"github.com/liorokman/ninjarecipe/internal/images"
	"github.com/liorokman/ninjarecipe/internal/store"
)

// RecipeDetail is what the recipe page needs.
type RecipeDetail struct {
	Recipe      *domain.Recipe
	IsFavourite bool
	Logs        []domain.CookLog
	Stats       domain.CookStats
}

// load fetches a recipe the actor can view, else ErrNotFound (existence is not leaked).
func (s *Service) load(ctx context.Context, q store.Q, actor *domain.User, id string) (*domain.Recipe, error) {
	r, err := q.GetRecipe(ctx, id)
	if err != nil {
		return nil, err
	}
	if !authz.CanView(actor, r) {
		return nil, domain.ErrNotFound
	}
	return r, nil
}

// GetRecipe returns the detail for the page; record=true upserts a view (throttled to once per 10 min).
func (s *Service) GetRecipe(ctx context.Context, actor *domain.User, id string, record bool) (*RecipeDetail, error) {
	q := s.Store.R()
	r, err := s.load(ctx, q, actor, id)
	if err != nil {
		return nil, err
	}
	d := &RecipeDetail{Recipe: r}
	if r.Status == domain.StatusActive {
		if record {
			s.logErr("record view", s.Store.W().UpsertView(ctx, actor.ID, id, s.Clock.Now(), 10*time.Minute))
		}
		if d.IsFavourite, err = q.IsFavourite(ctx, actor.ID, id); err != nil {
			return nil, err
		}
		if d.Logs, err = q.ListCookLogs(ctx, id); err != nil {
			return nil, err
		}
		if d.Stats, err = q.CookStats(ctx, id); err != nil {
			return nil, err
		}
	}
	return d, nil
}

// Detail refreshes only the cook-log section data.
func (s *Service) CookLogs(ctx context.Context, actor *domain.User, id string) (*RecipeDetail, error) {
	return s.GetRecipe(ctx, actor, id, false)
}

func (s *Service) categories(ctx context.Context, q store.Q) (map[string]domain.Label, error) {
	cs, err := q.ListCategories(ctx)
	return catMap(cs), err
}

// ListCategories returns all categories in tile order.
func (s *Service) ListCategories(ctx context.Context) ([]domain.Label, error) {
	return s.Store.R().ListCategories(ctx)
}

func (s *Service) labelIDs(ctx context.Context, tx *store.Tx, in domain.RecipeInput, cats map[string]domain.Label, actorID string, now time.Time) ([]int64, error) {
	var ids []int64
	for _, c := range in.Categories {
		ids = append(ids, cats[c].ID)
	}
	for _, t := range in.Tags {
		id, err := tx.EnsureTag(ctx, t, actorID, now)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// setLabels writes label links keeping the previous source of unchanged labels.
func (s *Service) setLabels(ctx context.Context, tx *store.Tx, recipeID string, ids []int64, prev []domain.Label, source string) error {
	prevSrc := map[int64]string{}
	for _, l := range prev {
		prevSrc[l.ID] = l.Source
	}
	// group by source so SetRecipeLabels (single source) can be reused
	if err := tx.SetRecipeLabels(ctx, recipeID, nil, source); err != nil {
		return err
	}
	return s.insertLabels(ctx, tx, recipeID, ids, prevSrc, source)
}

func (s *Service) insertLabels(ctx context.Context, tx *store.Tx, recipeID string, ids []int64, prevSrc map[int64]string, source string) error {
	for _, id := range ids {
		src := source
		if p, ok := prevSrc[id]; ok && p != "" {
			src = p
		}
		if err := tx.AddRecipeLabel(ctx, recipeID, id, src); err != nil {
			return err
		}
	}
	return nil
}

func validationErr(errs, warns []domain.Issue) error {
	if len(errs) == 0 {
		return nil
	}
	return &domain.ValidationError{Errors: errs, Warnings: warns}
}

// CreateRecipe creates an active recipe owned by actor. Warnings are returned even on success.
func (s *Service) CreateRecipe(ctx context.Context, actor *domain.User, in domain.RecipeInput) (*domain.Recipe, []domain.Issue, error) {
	cats, err := s.categories(ctx, s.Store.R())
	if err != nil {
		return nil, nil, err
	}
	errs, warns := Validate(&in, cats, true)
	if err := validationErr(errs, warns); err != nil {
		return nil, warns, err
	}
	now := s.Clock.Now()
	r := &domain.Recipe{ID: uuid.NewString(), OwnerID: actor.ID, Status: domain.StatusActive, Visibility: in.Visibility,
		Title: in.Title, Description: in.Description, Notes: in.Notes, Language: in.Language, Servings: in.Servings, PrepMinutes: in.PrepMinutes,
		SourceURL: in.SourceURL, SourceName: in.SourceName, Version: 1, CreatedBy: actor.ID, UpdatedBy: actor.ID, CreatedAt: now, UpdatedAt: now,
		Ingredients: in.Ingredients, Steps: in.Steps, Stages: in.Stages}
	err = s.Store.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.InsertRecipe(ctx, r); err != nil {
			return err
		}
		ids, err := s.labelIDs(ctx, tx, in, cats, actor.ID, now)
		if err != nil {
			return err
		}
		if err := tx.SetRecipeLabels(ctx, r.ID, ids, "user"); err != nil {
			return err
		}
		if err := tx.ReindexRecipe(ctx, r.ID); err != nil {
			return err
		}
		tx.AfterCommit(func() { s.publish(events.RecipeCreated{Base: events.Base{RecipeID: r.ID, ActorID: actor.ID, At: now}}) })
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return r, warns, nil
}

// UpdateRecipe edits a recipe the actor can edit; version must match.
func (s *Service) UpdateRecipe(ctx context.Context, actor *domain.User, id string, version int, in domain.RecipeInput) (*domain.Recipe, []domain.Issue, error) {
	return s.update(ctx, actor, id, version, in, false, false)
}

// update implements UpdateRecipe and EditAndAcceptInbox.
func (s *Service) update(ctx context.Context, actor *domain.User, id string, version int, in domain.RecipeInput, accept, share bool) (*domain.Recipe, []domain.Issue, error) {
	var out *domain.Recipe
	var warns []domain.Issue
	cats, err := s.categories(ctx, s.Store.R())
	if err != nil {
		return nil, nil, err
	}
	err = s.Store.WithTx(ctx, func(tx *store.Tx) error {
		cur, err := s.load(ctx, tx.Q, actor, id)
		if err != nil {
			return err
		}
		if accept {
			if !authz.CanAcceptReject(actor, cur) {
				return domain.ErrNotFound
			}
		} else if !authz.CanEdit(actor, cur) {
			return domain.ErrForbidden
		}
		var errs []domain.Issue
		errs, warns = Validate(&in, cats, true)
		if err := validationErr(errs, warns); err != nil {
			return err
		}
		now := s.Clock.Now()
		submittedURL := cur.SourceURL // as the assistant sent it; duplicate detection keys on this (30 §5a)
		cur.Title, cur.Description, cur.Notes, cur.Language = in.Title, in.Description, in.Notes, in.Language
		cur.Servings, cur.PrepMinutes, cur.SourceURL, cur.SourceName = in.Servings, in.PrepMinutes, in.SourceURL, in.SourceName
		cur.Ingredients, cur.Steps, cur.Stages = in.Ingredients, in.Steps, in.Stages
		cur.UpdatedBy, cur.UpdatedAt, cur.Version = actor.ID, now, version
		if err := tx.UpdateRecipe(ctx, cur); err != nil {
			return err
		}
		ids, err := s.labelIDs(ctx, tx, in, cats, actor.ID, now)
		if err != nil {
			return err
		}
		if err := s.setLabels(ctx, tx, id, ids, cur.Labels, "user"); err != nil {
			return err
		}
		if accept {
			vis := domain.VisPrivate
			if share {
				vis = domain.VisShared
			}
			if err := tx.ActivateInbox(ctx, id, vis, actor.ID, now); err != nil {
				return err
			}
			cur.Status, cur.Visibility = domain.StatusActive, vis
			cur.Version++
			if err := tx.InsertOutcome(ctx, actor.ID, keptOutcome(cur, submittedURL, true, share, now)); err != nil {
				return err
			}
		}
		if err := tx.ReindexRecipe(ctx, id); err != nil {
			return err
		}
		out = cur
		tx.AfterCommit(func() {
			b := events.Base{RecipeID: id, ActorID: actor.ID, At: now}
			if accept {
				s.publish(events.InboxItemAccepted{Base: b})
			} else {
				s.publish(events.RecipeUpdated{Base: b})
			}
		})
		return nil
	})
	if err != nil {
		if ve, ok := err.(*domain.ValidationError); ok {
			return nil, ve.Warnings, err
		}
		return nil, nil, err
	}
	return out, warns, nil
}

// DeleteRecipe hard-deletes a recipe (or rejects an inbox item).
func (s *Service) DeleteRecipe(ctx context.Context, actor *domain.User, id string) error {
	return s.Store.WithTx(ctx, func(tx *store.Tx) error {
		r, err := s.load(ctx, tx.Q, actor, id)
		if err != nil {
			return err
		}
		if !authz.CanDelete(actor, r) {
			return domain.ErrForbidden
		}
		keys, err := tx.DeleteRecipe(ctx, id)
		if err != nil {
			return err
		}
		tx.AfterCommit(func() {
			s.removeFiles(keys)
			s.publish(events.RecipeDeleted{Base: events.Base{RecipeID: id, ActorID: actor.ID, At: s.Clock.Now()}})
		})
		return nil
	})
}

// SetVisibility shares or unshares (owner only).
func (s *Service) SetVisibility(ctx context.Context, actor *domain.User, id string, shared bool) error {
	return s.Store.WithTx(ctx, func(tx *store.Tx) error {
		r, err := s.load(ctx, tx.Q, actor, id)
		if err != nil {
			return err
		}
		if !authz.CanChangeVisibility(actor, r) {
			return domain.ErrForbidden
		}
		vis := domain.VisPrivate
		if shared {
			vis = domain.VisShared
		}
		if vis == r.Visibility {
			return nil
		}
		if err := tx.SetVisibility(ctx, id, vis, s.Clock.Now()); err != nil {
			return err
		}
		if !shared {
			return tx.DropOthersFavsViews(ctx, id, actor.ID)
		}
		return nil
	})
}

// CopyRecipe creates a private copy owned by the actor, duplicating image files under new keys.
func (s *Service) CopyRecipe(ctx context.Context, actor *domain.User, id string) (*domain.Recipe, error) {
	var out *domain.Recipe
	var created []string
	err := s.Store.WithTx(ctx, func(tx *store.Tx) error {
		src, err := s.load(ctx, tx.Q, actor, id)
		if err != nil {
			return err
		}
		if !authz.CanCopy(actor, src) {
			return domain.ErrForbidden
		}
		now := s.Clock.Now()
		n := &domain.Recipe{ID: uuid.NewString(), OwnerID: actor.ID, Status: domain.StatusActive, Visibility: domain.VisPrivate,
			Title: src.Title, Description: src.Description, Notes: src.Notes, Language: src.Language, Servings: src.Servings,
			PrepMinutes: src.PrepMinutes, SourceURL: src.SourceURL, SourceName: src.SourceName, Version: 1,
			CreatedBy: actor.ID, UpdatedBy: actor.ID, CreatedAt: now, UpdatedAt: now,
			Ingredients: src.Ingredients, Steps: src.Steps, Stages: src.Stages}
		if err := tx.InsertRecipe(ctx, n); err != nil {
			return err
		}
		var lids []int64
		for _, l := range src.Labels {
			lids = append(lids, l.ID)
		}
		if err := tx.SetRecipeLabels(ctx, n.ID, lids, "user"); err != nil {
			return err
		}
		for _, im := range src.Images {
			key, err := images.Copy(s.DataDir, im.Key)
			if err != nil {
				s.logErr("copy image", err)
				continue
			}
			created = append(created, key)
			ni := domain.Image{ID: uuid.NewString(), RecipeID: n.ID, Key: key, Kind: "recipe", Width: im.Width, Height: im.Height, Bytes: im.Bytes,
				Caption: im.Caption, SourceURL: im.SourceURL, CreatedAt: now}
			if err := tx.InsertImage(ctx, &ni); err != nil {
				return err
			}
		}
		if err := tx.ReindexRecipe(ctx, n.ID); err != nil {
			return err
		}
		out = n
		tx.AfterCommit(func() { s.publish(events.RecipeCreated{Base: events.Base{RecipeID: n.ID, ActorID: actor.ID, At: now}}) })
		return nil
	})
	if err != nil {
		s.removeFiles(created)
		return nil, err
	}
	return out, nil
}

// SetFavourite marks/unmarks (idempotent).
func (s *Service) SetFavourite(ctx context.Context, actor *domain.User, id string, on bool) error {
	r, err := s.load(ctx, s.Store.R(), actor, id)
	if err != nil {
		return err
	}
	if !authz.CanFavourite(actor, r) {
		return domain.ErrForbidden
	}
	return s.Store.W().SetFavourite(ctx, actor.ID, id, on, s.Clock.Now())
}

// CookLogInput is the payload of LogCook (D6).
type CookLogInput struct {
	Note   string
	Rating *int
	Photo  io.Reader
}

// LogCook records a cook. A failing photo never fails the log: photoErr reports it.
func (s *Service) LogCook(ctx context.Context, actor *domain.User, id string, in CookLogInput) (photoErr error, err error) {
	r, err := s.load(ctx, s.Store.R(), actor, id)
	if err != nil {
		return nil, err
	}
	if !authz.CanLogCook(actor, r) {
		return nil, domain.ErrForbidden
	}
	if rc(in.Note) > domain.MaxCookLogNote {
		return nil, &domain.ValidationError{Errors: []domain.Issue{issue("note", "too_long", "max", domain.MaxCookLogNote)}}
	}
	if in.Rating != nil && (*in.Rating < 1 || *in.Rating > 5) {
		return nil, &domain.ValidationError{Errors: []domain.Issue{issue("rating", "out_of_range", "min", 1, "max", 5)}}
	}
	now := s.Clock.Now()
	var img *domain.Image
	if in.Photo != nil {
		p, perr := s.Proc.Process(ctx, in.Photo)
		if perr == nil {
			var key string
			if key, perr = images.Store(s.DataDir, p); perr == nil {
				img = &domain.Image{ID: uuid.NewString(), RecipeID: id, Key: key, Kind: "cook_log", Width: p.Width, Height: p.Height, Bytes: p.Bytes(), CreatedAt: now}
			}
		}
		photoErr = perr
	}
	l := &domain.CookLog{RecipeID: id, UserID: actor.ID, CookedAt: now, Note: in.Note, Rating: in.Rating, Image: img}
	err = s.Store.WithTx(ctx, func(tx *store.Tx) error {
		if img != nil {
			if err := tx.InsertImage(ctx, img); err != nil {
				return err
			}
		}
		if err := tx.InsertCookLog(ctx, l); err != nil {
			return err
		}
		return tx.ReindexRecipe(ctx, id)
	})
	if err != nil {
		if img != nil {
			images.Remove(s.DataDir, img.Key)
		}
		return nil, err
	}
	return photoErr, nil
}

// DeleteCookLog deletes one entry (author only) and its photo.
func (s *Service) DeleteCookLog(ctx context.Context, actor *domain.User, recipeID string, logID int64) error {
	return s.Store.WithTx(ctx, func(tx *store.Tx) error {
		r, err := s.load(ctx, tx.Q, actor, recipeID)
		if err != nil {
			return err
		}
		l, err := tx.GetCookLog(ctx, recipeID, logID)
		if err != nil {
			return err
		}
		if !authz.CanDeleteCookLog(actor, l) {
			return domain.ErrForbidden
		}
		key, err := tx.DeleteCookLog(ctx, l)
		if err != nil {
			return err
		}
		if r.Status == domain.StatusActive {
			if err := tx.ReindexRecipe(ctx, recipeID); err != nil {
				return err
			}
		}
		if key != "" {
			tx.AfterCommit(func() { images.Remove(s.DataDir, key) })
		}
		return nil
	})
}

// AddImage processes and attaches one gallery photo.
func (s *Service) AddImage(ctx context.Context, actor *domain.User, recipeID string, r io.Reader) (*domain.Image, error) {
	rec, err := s.load(ctx, s.Store.R(), actor, recipeID)
	if err != nil {
		return nil, err
	}
	if !authz.CanEdit(actor, rec) {
		return nil, domain.ErrForbidden
	}
	if n, err := s.Store.R().CountRecipePhotos(ctx, recipeID); err != nil {
		return nil, err
	} else if n >= s.MaxImages {
		return nil, domain.ErrLimit
	}
	p, err := s.Proc.Process(ctx, r)
	if err != nil {
		return nil, err
	}
	key, err := images.Store(s.DataDir, p)
	if err != nil {
		return nil, err
	}
	im := &domain.Image{ID: uuid.NewString(), RecipeID: recipeID, Key: key, Kind: "recipe", Width: p.Width, Height: p.Height, Bytes: p.Bytes(), CreatedAt: s.Clock.Now()}
	if err := s.Store.WithTx(ctx, func(tx *store.Tx) error { return tx.InsertImage(ctx, im) }); err != nil {
		images.Remove(s.DataDir, key)
		return nil, err
	}
	return im, nil
}

// DeleteImage removes one gallery photo.
func (s *Service) DeleteImage(ctx context.Context, actor *domain.User, recipeID, imageID string) error {
	return s.Store.WithTx(ctx, func(tx *store.Tx) error {
		rec, err := s.load(ctx, tx.Q, actor, recipeID)
		if err != nil {
			return err
		}
		if !authz.CanEdit(actor, rec) {
			return domain.ErrForbidden
		}
		im, err := tx.ImageByID(ctx, recipeID, imageID)
		if err != nil {
			return err
		}
		if err := tx.DeleteImage(ctx, recipeID, imageID); err != nil {
			return err
		}
		tx.AfterCommit(func() { images.Remove(s.DataDir, im.Key) })
		return nil
	})
}

// ReorderImages sets the gallery order; ids must be exactly the recipe's gallery photos (first = cover).
func (s *Service) ReorderImages(ctx context.Context, actor *domain.User, recipeID string, ids []string) error {
	return s.Store.WithTx(ctx, func(tx *store.Tx) error {
		rec, err := s.load(ctx, tx.Q, actor, recipeID)
		if err != nil {
			return err
		}
		if !authz.CanEdit(actor, rec) {
			return domain.ErrForbidden
		}
		cur, err := tx.ListImages(ctx, recipeID, "recipe")
		if err != nil {
			return err
		}
		if len(cur) != len(ids) {
			return domain.ErrBadRequest
		}
		have := map[string]bool{}
		for _, c := range cur {
			have[c.ID] = true
		}
		for _, id := range ids {
			if !have[id] {
				return domain.ErrBadRequest
			}
			delete(have, id)
		}
		return tx.SetImageOrder(ctx, recipeID, ids)
	})
}
