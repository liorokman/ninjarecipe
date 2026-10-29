package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/liorokman/ninjarecipe/internal/domain"
	"github.com/liorokman/ninjarecipe/internal/textnorm"
)

const catCols = `id, type, slug, name, COALESCE(name_he,''), COALESCE(icon,''), sort_order, COALESCE(description,'')`

func scanLabel(sc interface{ Scan(...any) error }) (domain.Label, error) {
	var l domain.Label
	err := sc.Scan(&l.ID, &l.Type, &l.Slug, &l.Name, &l.NameHe, &l.Icon, &l.SortOrder, &l.Description)
	return l, err
}

// ListCategories returns all categories in tile order.
func (q Q) ListCategories(ctx context.Context) ([]domain.Label, error) {
	rows, err := q.q.QueryContext(ctx, `SELECT `+catCols+` FROM labels WHERE type='category' ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Label
	for rows.Next() {
		l, err := scanLabel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// CategoryByID loads one category.
func (q Q) CategoryByID(ctx context.Context, id int64) (domain.Label, error) {
	l, err := scanLabel(q.q.QueryRowContext(ctx, `SELECT `+catCols+` FROM labels WHERE type='category' AND id=?`, id))
	return l, notFound(err)
}

// CreateCategory inserts a category; a duplicate slug yields ErrConflict.
func (q Q) CreateCategory(ctx context.Context, l domain.Label, createdBy string, now time.Time) (int64, error) {
	r, err := q.q.ExecContext(ctx, `INSERT INTO labels (type, slug, name, name_he, icon, sort_order, description, created_by, created_at) VALUES ('category',?,?,?,?,?,?,?,?)`,
		l.Slug, l.Name, l.NameHe, nullStr(l.Icon), l.SortOrder, nullStr(l.Description), nullStr(createdBy), FormatTime(now))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, domain.ErrConflict
		}
		return 0, err
	}
	return r.LastInsertId()
}

// UpdateCategory updates everything but the slug.
func (q Q) UpdateCategory(ctx context.Context, l domain.Label) error {
	r, err := q.q.ExecContext(ctx, `UPDATE labels SET name=?, name_he=?, icon=?, sort_order=?, description=? WHERE id=? AND type='category'`, l.Name, l.NameHe, nullStr(l.Icon), l.SortOrder, nullStr(l.Description), l.ID)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// DeleteCategory removes a category (links cascade).
func (q Q) DeleteCategory(ctx context.Context, id int64) error {
	r, err := q.q.ExecContext(ctx, `DELETE FROM labels WHERE id=? AND type='category'`, id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// LabelRecipeIDs lists recipes carrying a label.
func (q Q) LabelRecipeIDs(ctx context.Context, labelID int64) ([]string, error) {
	return q.strings(ctx, `SELECT recipe_id FROM recipe_labels WHERE label_id=?`, labelID)
}

// CategoryCounts maps category id to the number of recipes using it.
func (q Q) CategoryCounts(ctx context.Context) (map[int64]int, error) {
	rows, err := q.q.QueryContext(ctx, `SELECT label_id, COUNT(*) FROM recipe_labels GROUP BY label_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		m[id] = n
	}
	return m, rows.Err()
}

func (q Q) strings(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := q.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// EnsureTag returns the id of a tag, creating it on first use.
func (q Q) EnsureTag(ctx context.Context, name, createdBy string, now time.Time) (int64, error) {
	if _, err := q.q.ExecContext(ctx, `INSERT OR IGNORE INTO labels (type, slug, name, created_by, created_at) VALUES ('tag',?,?,?,?)`,
		name, name, nullStr(createdBy), FormatTime(now)); err != nil {
		return 0, err
	}
	var id int64
	err := q.q.QueryRowContext(ctx, `SELECT id FROM labels WHERE type='tag' AND slug=?`, name).Scan(&id)
	return id, err
}

// SetRecipeLabels replaces the label links of a recipe.
func (q Q) SetRecipeLabels(ctx context.Context, recipeID string, ids []int64, source string) error {
	if _, err := q.q.ExecContext(ctx, `DELETE FROM recipe_labels WHERE recipe_id=?`, recipeID); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := q.q.ExecContext(ctx, `INSERT OR IGNORE INTO recipe_labels (recipe_id, label_id, source) VALUES (?,?,?)`, recipeID, id, source); err != nil {
			return err
		}
	}
	return nil
}

// SuggestTags returns up to limit tags used on recipes the actor can view: prefix matches first, then substring.
func (q Q) SuggestTags(ctx context.Context, actorID, s string, limit int) ([]string, error) {
	s = textnorm.EscapeLike(strings.ToLower(strings.TrimSpace(s)))
	const base = `SELECT DISTINCT l.name FROM labels l JOIN recipe_labels rl ON rl.label_id=l.id JOIN recipes r ON r.id=rl.recipe_id
		WHERE l.type='tag' AND r.status='active' AND (r.visibility='shared' OR r.owner_id=?) AND l.slug LIKE ? ESCAPE '\' ORDER BY l.name LIMIT ?`
	pre, err := q.strings(ctx, base, actorID, s+"%", limit)
	if err != nil {
		return nil, err
	}
	if len(pre) >= limit {
		return pre, nil
	}
	sub, err := q.strings(ctx, base, actorID, "%"+s+"%", limit)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, p := range pre {
		seen[p] = true
	}
	for _, x := range sub {
		if !seen[x] && len(pre) < limit {
			pre = append(pre, x)
		}
	}
	return pre, nil
}

// VisibleTags lists all tags in use on recipes the actor can view.
func (q Q) VisibleTags(ctx context.Context, actorID string) ([]string, error) {
	return q.strings(ctx, `SELECT DISTINCT l.name FROM labels l JOIN recipe_labels rl ON rl.label_id=l.id JOIN recipes r ON r.id=rl.recipe_id
		WHERE l.type='tag' AND r.status='active' AND (r.visibility='shared' OR r.owner_id=?) ORDER BY l.name`, actorID)
}

// VisibleCategorySlugs lists categories having at least one recipe visible to the actor (home tiles).
func (q Q) VisibleCategorySlugs(ctx context.Context, actorID string) (map[string]bool, error) {
	ss, err := q.strings(ctx, `SELECT DISTINCT l.slug FROM labels l JOIN recipe_labels rl ON rl.label_id=l.id JOIN recipes r ON r.id=rl.recipe_id
		WHERE l.type='category' AND r.status='active' AND (r.visibility='shared' OR r.owner_id=?)`, actorID)
	m := map[string]bool{}
	for _, s := range ss {
		m[s] = true
	}
	return m, err
}

// PurgeOrphanTags deletes tags without links.
func (q Q) PurgeOrphanTags(ctx context.Context) (int64, error) {
	r, err := q.q.ExecContext(ctx, `DELETE FROM labels WHERE type='tag' AND id NOT IN (SELECT label_id FROM recipe_labels)`)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

var _ = sql.ErrNoRows

// AddRecipeLabel links one label with the given source.
func (q Q) AddRecipeLabel(ctx context.Context, recipeID string, labelID int64, source string) error {
	_, err := q.q.ExecContext(ctx, `INSERT OR IGNORE INTO recipe_labels (recipe_id, label_id, source) VALUES (?,?,?)`, recipeID, labelID, source)
	return err
}

// UncategorizedIDs lists up to limit recipes (inbox or active) the categorizer has not processed yet, oldest first.
func (q Q) UncategorizedIDs(ctx context.Context, limit int) ([]string, error) {
	return q.strings(ctx, `SELECT id FROM recipes WHERE categorized_at IS NULL ORDER BY created_at LIMIT ?`, limit)
}

// MarkCategorized sets the durable "processed by the categorizer" marker.
func (q Q) MarkCategorized(ctx context.Context, id string, now time.Time) error {
	_, err := q.q.ExecContext(ctx, `UPDATE recipes SET categorized_at=? WHERE id=? AND categorized_at IS NULL`, FormatTime(now), id)
	return err
}
