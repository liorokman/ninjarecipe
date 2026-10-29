package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/liorokman/ninjarecipe/internal/domain"
	"github.com/liorokman/ninjarecipe/internal/textnorm"
)

const recipeSelect = `SELECT r.id, COALESCE(r.owner_id,''), r.status, r.visibility, r.title, r.description, r.notes, r.language,
	r.servings, r.prep_minutes, COALESCE(r.source_url,''), COALESCE(r.source_name,''), COALESCE(r.import_key,''), r.version,
	r.categorized_at, COALESCE(r.created_by,''), COALESCE(r.updated_by,''), r.created_at, r.updated_at,
	COALESCE(o.display_name,''), COALESCE(ub.display_name,''), COALESCE(cb.display_name,'')
	FROM recipes r LEFT JOIN users o ON o.id=r.owner_id LEFT JOIN users ub ON ub.id=r.updated_by LEFT JOIN users cb ON cb.id=r.created_by`

func scanRecipe(sc interface{ Scan(...any) error }) (*domain.Recipe, error) {
	var r domain.Recipe
	var serv, prep sql.NullInt64
	var cat sql.NullString
	var ca, ua string
	if err := sc.Scan(&r.ID, &r.OwnerID, &r.Status, &r.Visibility, &r.Title, &r.Description, &r.Notes, &r.Language, &serv, &prep,
		&r.SourceURL, &r.SourceName, &r.ImportKey, &r.Version, &cat, &r.CreatedBy, &r.UpdatedBy, &ca, &ua,
		&r.OwnerName, &r.UpdatedByName, &r.CreatedByName); err != nil {
		return nil, err
	}
	r.Servings, r.PrepMinutes = intPtr(serv), intPtr(prep)
	r.CategorizedAt = parseNullTime(cat)
	r.CreatedAt, r.UpdatedAt = ParseTime(ca), ParseTime(ua)
	return &r, nil
}

// GetRecipe loads a recipe with all children (no authorization).
func (q Q) GetRecipe(ctx context.Context, id string) (*domain.Recipe, error) {
	r, err := scanRecipe(q.q.QueryRowContext(ctx, recipeSelect+` WHERE r.id=?`, id))
	if err != nil {
		return nil, notFound(err)
	}
	if err := q.loadChildren(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}

func (q Q) loadChildren(ctx context.Context, r *domain.Recipe) error {
	rows, err := q.q.QueryContext(ctx, `SELECT position, COALESCE(section,''), amount, COALESCE(unit,''), item, raw_text FROM recipe_ingredients WHERE recipe_id=? ORDER BY position`, r.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var i domain.Ingredient
		var a sql.NullFloat64
		if err := rows.Scan(&i.Position, &i.Section, &a, &i.Unit, &i.Item, &i.RawText); err != nil {
			rows.Close()
			return err
		}
		if a.Valid {
			v := a.Float64
			i.Amount = &v
		}
		r.Ingredients = append(r.Ingredients, i)
	}
	rows.Close()

	rows, err = q.q.QueryContext(ctx, `SELECT position, text, timer_seconds FROM recipe_steps WHERE recipe_id=? ORDER BY position`, r.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var s domain.Step
		var t sql.NullInt64
		if err := rows.Scan(&s.Position, &s.Text, &t); err != nil {
			rows.Close()
			return err
		}
		s.TimerSeconds = intPtr(t)
		r.Steps = append(r.Steps, s)
	}
	rows.Close()

	rows, err = q.q.QueryContext(ctx, `SELECT position, mode, COALESCE(pizza_preset,''), temperature_c, duration_seconds, temperature2_c, duration2_seconds,
		preheat_minutes, woodfire, COALESCE(pellet_blend,''), COALESCE(pellet_note,''), accessories, target_core_temp_c, notes
		FROM oven_stages WHERE recipe_id=? ORDER BY position`, r.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var s domain.OvenStage
		var t1, d1, t2, d2, ph, core sql.NullInt64
		var wood int
		var acc string
		if err := rows.Scan(&s.Position, &s.Mode, &s.PizzaPreset, &t1, &d1, &t2, &d2, &ph, &wood, &s.PelletBlend, &s.PelletNote, &acc, &core, &s.Notes); err != nil {
			rows.Close()
			return err
		}
		s.TemperatureC, s.DurationSeconds, s.Temperature2C, s.Duration2Seconds = intPtr(t1), intPtr(d1), intPtr(t2), intPtr(d2)
		s.PreheatMinutes, s.TargetCoreTempC = intPtr(ph), intPtr(core)
		s.Woodfire = wood == 1
		_ = json.Unmarshal([]byte(acc), &s.Accessories)
		if s.Accessories == nil {
			s.Accessories = []string{}
		}
		r.Stages = append(r.Stages, s)
	}
	rows.Close()

	r.Images, err = q.ListImages(ctx, r.ID, "recipe")
	if err != nil {
		return err
	}
	rows, err = q.q.QueryContext(ctx, `SELECT l.id, l.type, l.slug, l.name, COALESCE(l.name_he,''), COALESCE(l.icon,''), l.sort_order, rl.source
		FROM recipe_labels rl JOIN labels l ON l.id=rl.label_id WHERE rl.recipe_id=? ORDER BY l.type, l.sort_order, l.name`, r.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var l domain.Label
		if err := rows.Scan(&l.ID, &l.Type, &l.Slug, &l.Name, &l.NameHe, &l.Icon, &l.SortOrder, &l.Source); err != nil {
			return err
		}
		r.Labels = append(r.Labels, l)
	}
	return rows.Err()
}

// InsertRecipe writes the recipe row and children; labels are attached separately via SetRecipeLabels.
func (q Q) InsertRecipe(ctx context.Context, r *domain.Recipe) error {
	_, err := q.q.ExecContext(ctx, `INSERT INTO recipes (id, owner_id, status, visibility, title, description, notes, language, servings, prep_minutes,
		source_url, source_key, source_name, import_key, version, categorized_at, created_by, updated_by, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, nullStr(r.OwnerID), r.Status, r.Visibility, r.Title, r.Description, r.Notes, r.Language, nullInt(r.Servings), nullInt(r.PrepMinutes),
		nullStr(r.SourceURL), nullStr(textnorm.SourceKey(r.SourceURL)), nullStr(r.SourceName), nullStr(r.ImportKey), r.Version, nullTime(r.CategorizedAt), nullStr(r.CreatedBy), nullStr(r.UpdatedBy),
		FormatTime(r.CreatedAt), FormatTime(r.UpdatedAt))
	if err != nil {
		return err
	}
	return q.replaceChildren(ctx, r)
}

// UpdateRecipe updates fields and replaces children when r.Version matches the stored version; the stored
// version becomes r.Version+1. Returns ErrConflict on mismatch, ErrNotFound when the row does not exist.
func (q Q) UpdateRecipe(ctx context.Context, r *domain.Recipe) error {
	res, err := q.q.ExecContext(ctx, `UPDATE recipes SET title=?, description=?, notes=?, language=?, servings=?, prep_minutes=?, source_url=?, source_key=?, source_name=?,
		updated_by=?, updated_at=?, version=version+1 WHERE id=? AND version=?`,
		r.Title, r.Description, r.Notes, r.Language, nullInt(r.Servings), nullInt(r.PrepMinutes), nullStr(r.SourceURL), nullStr(textnorm.SourceKey(r.SourceURL)), nullStr(r.SourceName),
		nullStr(r.UpdatedBy), FormatTime(r.UpdatedAt), r.ID, r.Version)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var one int
		if q.q.QueryRowContext(ctx, `SELECT 1 FROM recipes WHERE id=?`, r.ID).Scan(&one) == sql.ErrNoRows {
			return domain.ErrNotFound
		}
		return domain.ErrConflict
	}
	r.Version++
	return q.replaceChildren(ctx, r)
}

func (q Q) replaceChildren(ctx context.Context, r *domain.Recipe) error {
	for _, t := range []string{"recipe_ingredients", "recipe_steps", "oven_stages"} {
		if _, err := q.q.ExecContext(ctx, `DELETE FROM `+t+` WHERE recipe_id=?`, r.ID); err != nil {
			return err
		}
	}
	for i, in := range r.Ingredients {
		var amt any
		if in.Amount != nil {
			amt = *in.Amount
		}
		if _, err := q.q.ExecContext(ctx, `INSERT INTO recipe_ingredients (recipe_id, position, section, amount, unit, item, raw_text) VALUES (?,?,?,?,?,?,?)`,
			r.ID, i+1, nullStr(in.Section), amt, nullStr(in.Unit), in.Item, in.RawText); err != nil {
			return err
		}
	}
	for i, s := range r.Steps {
		if _, err := q.q.ExecContext(ctx, `INSERT INTO recipe_steps (recipe_id, position, text, timer_seconds) VALUES (?,?,?,?)`, r.ID, i+1, s.Text, nullInt(s.TimerSeconds)); err != nil {
			return err
		}
	}
	for i, s := range r.Stages {
		acc, _ := json.Marshal(append([]string{}, s.Accessories...))
		if _, err := q.q.ExecContext(ctx, `INSERT INTO oven_stages (recipe_id, position, mode, pizza_preset, temperature_c, duration_seconds, temperature2_c,
			duration2_seconds, preheat_minutes, woodfire, pellet_blend, pellet_note, accessories, target_core_temp_c, notes) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			r.ID, i+1, s.Mode, nullStr(s.PizzaPreset), nullInt(s.TemperatureC), nullInt(s.DurationSeconds), nullInt(s.Temperature2C), nullInt(s.Duration2Seconds),
			nullInt(s.PreheatMinutes), boolInt(s.Woodfire), nullStr(s.PelletBlend), nullStr(s.PelletNote), string(acc), nullInt(s.TargetCoreTempC), s.Notes); err != nil {
			return err
		}
	}
	return nil
}

// DeleteRecipe deletes a recipe (children cascade) and returns every storage key it had (recipe and cook-log photos).
func (q Q) DeleteRecipe(ctx context.Context, id string) ([]string, error) {
	keys, err := q.strings(ctx, `SELECT storage_key FROM recipe_images WHERE recipe_id=?`, id)
	if err != nil {
		return nil, err
	}
	if _, err := q.q.ExecContext(ctx, `DELETE FROM recipe_fts WHERE recipe_id=?`, id); err != nil {
		return nil, err
	}
	res, err := q.q.ExecContext(ctx, `DELETE FROM recipes WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, domain.ErrNotFound
	}
	return keys, nil
}

// SetVisibility updates visibility and the audit fields without bumping the version.
func (q Q) SetVisibility(ctx context.Context, id, vis string, now time.Time) error {
	_, err := q.q.ExecContext(ctx, `UPDATE recipes SET visibility=?, updated_at=? WHERE id=?`, vis, FormatTime(now), id)
	return err
}

// ActivateInbox flips an inbox item to active.
func (q Q) ActivateInbox(ctx context.Context, id, vis, actorID string, now time.Time) error {
	_, err := q.q.ExecContext(ctx, `UPDATE recipes SET status='active', visibility=?, updated_by=?, updated_at=?, version=version+1 WHERE id=? AND status='inbox'`, vis, actorID, FormatTime(now), id)
	return err
}

// SetOwnerless sets owner_id NULL (user deletion procedure).
func (q Q) SetOwnerless(ctx context.Context, id string) error {
	_, err := q.q.ExecContext(ctx, `UPDATE recipes SET owner_id=NULL WHERE id=?`, id)
	return err
}

// UserRecipeIDs lists recipes owned by a user with the given condition ("private-or-inbox" or "shared").
func (q Q) UserRecipeIDs(ctx context.Context, userID string, shared bool) ([]string, error) {
	if shared {
		return q.strings(ctx, `SELECT id FROM recipes WHERE owner_id=? AND status='active' AND visibility='shared'`, userID)
	}
	return q.strings(ctx, `SELECT id FROM recipes WHERE owner_id=? AND (status='inbox' OR visibility='private')`, userID)
}

// FindByImportKey finds the recipe of an owner with an import key.
func (q Q) FindByImportKey(ctx context.Context, ownerID, key string) (*domain.Recipe, error) {
	var id string
	if err := q.q.QueryRowContext(ctx, `SELECT id FROM recipes WHERE owner_id=? AND import_key=?`, ownerID, key).Scan(&id); err != nil {
		return nil, notFound(err)
	}
	return q.GetRecipe(ctx, id)
}

// FindBySourceKey returns the id of any recipe of the owner (any status) with this source key (40 §4.3).
func (q Q) FindBySourceKey(ctx context.Context, ownerID, key string) (string, error) {
	var id string
	err := q.q.QueryRowContext(ctx, `SELECT id FROM recipes WHERE owner_id=? AND source_key=? ORDER BY created_at LIMIT 1`, ownerID, key).Scan(&id)
	return id, notFound(err)
}

// BackfillSourceKeys sets source_key on rows that have a source_url but no key yet (30 §5a). Returns rows changed.
func (q Q) BackfillSourceKeys(ctx context.Context) (int, error) {
	rows, err := q.q.QueryContext(ctx, `SELECT id, source_url FROM recipes WHERE source_url IS NOT NULL AND source_key IS NULL`)
	if err != nil {
		return 0, err
	}
	type pair struct{ id, url string }
	var todo []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.id, &p.url); err != nil {
			rows.Close()
			return 0, err
		}
		todo = append(todo, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, p := range todo {
		key := textnorm.SourceKey(p.url)
		if key == "" {
			continue // not an absolute http(s) URL: stays NULL (and is re-checked, cheaply, at the next start)
		}
		if _, err := q.q.ExecContext(ctx, `UPDATE recipes SET source_key=? WHERE id=?`, key, p.id); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// InboxIDs lists the pending inbox item ids of a user, oldest first.
func (q Q) InboxIDs(ctx context.Context, userID string) ([]string, error) {
	return q.strings(ctx, `SELECT id FROM recipes WHERE owner_id=? AND status='inbox' ORDER BY created_at, id`, userID)
}

// CountInbox counts pending inbox items.
func (q Q) CountInbox(ctx context.Context, userID string) (int, error) {
	var n int
	err := q.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM recipes WHERE owner_id=? AND status='inbox'`, userID).Scan(&n)
	return n, err
}

// AllRecipeIDs lists active recipe ids (reindex).
func (q Q) AllRecipeIDs(ctx context.Context) ([]string, error) {
	return q.strings(ctx, `SELECT id FROM recipes WHERE status='active'`)
}
