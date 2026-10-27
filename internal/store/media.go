package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"cooking/internal/domain"
)

const imgCols = `id, recipe_id, storage_key, kind, COALESCE(position,0), width, height, bytes, COALESCE(caption,''), COALESCE(source_url,''), created_at`

func scanImage(sc interface{ Scan(...any) error }) (domain.Image, error) {
	var im domain.Image
	var ca string
	err := sc.Scan(&im.ID, &im.RecipeID, &im.Key, &im.Kind, &im.Position, &im.Width, &im.Height, &im.Bytes, &im.Caption, &im.SourceURL, &ca)
	im.CreatedAt = ParseTime(ca)
	return im, err
}

// ListImages lists images of a recipe by kind.
func (q Q) ListImages(ctx context.Context, recipeID, kind string) ([]domain.Image, error) {
	rows, err := q.q.QueryContext(ctx, `SELECT `+imgCols+` FROM recipe_images WHERE recipe_id=? AND kind=? ORDER BY position, created_at`, recipeID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Image
	for rows.Next() {
		im, err := scanImage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, im)
	}
	return out, rows.Err()
}

// CountRecipePhotos counts gallery photos (cook-log photos excluded).
func (q Q) CountRecipePhotos(ctx context.Context, recipeID string) (int, error) {
	var n int
	err := q.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM recipe_images WHERE recipe_id=? AND kind='recipe'`, recipeID).Scan(&n)
	return n, err
}

// InsertImage inserts an image; recipe photos get the next free position.
func (q Q) InsertImage(ctx context.Context, im *domain.Image) error {
	var pos any
	if im.Kind == "recipe" {
		var next int
		if err := q.q.QueryRowContext(ctx, `SELECT COALESCE(MAX(position),0)+1 FROM recipe_images WHERE recipe_id=? AND kind='recipe'`, im.RecipeID).Scan(&next); err != nil {
			return err
		}
		im.Position = next
		pos = next
	}
	_, err := q.q.ExecContext(ctx, `INSERT INTO recipe_images (id, recipe_id, storage_key, kind, position, width, height, bytes, caption, source_url, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		im.ID, im.RecipeID, im.Key, im.Kind, pos, im.Width, im.Height, im.Bytes, nullStr(im.Caption), nullStr(im.SourceURL), FormatTime(im.CreatedAt))
	return err
}

// ImageByKey returns the image with the storage key.
func (q Q) ImageByKey(ctx context.Context, key string) (*domain.Image, error) {
	im, err := scanImage(q.q.QueryRowContext(ctx, `SELECT `+imgCols+` FROM recipe_images WHERE storage_key=?`, key))
	if err != nil {
		return nil, notFound(err)
	}
	return &im, nil
}

// ImageByID returns an image of a recipe.
func (q Q) ImageByID(ctx context.Context, recipeID, id string) (*domain.Image, error) {
	im, err := scanImage(q.q.QueryRowContext(ctx, `SELECT `+imgCols+` FROM recipe_images WHERE id=? AND recipe_id=?`, id, recipeID))
	if err != nil {
		return nil, notFound(err)
	}
	return &im, nil
}

// DeleteImage deletes the row and renumbers the remaining gallery photos 1..n.
func (q Q) DeleteImage(ctx context.Context, recipeID, id string) error {
	im, err := q.ImageByID(ctx, recipeID, id)
	if err != nil {
		return err
	}
	if _, err := q.q.ExecContext(ctx, `DELETE FROM recipe_images WHERE id=?`, id); err != nil {
		return err
	}
	if im.Kind != "recipe" {
		return nil
	}
	rest, err := q.ListImages(ctx, recipeID, "recipe")
	if err != nil {
		return err
	}
	ids := make([]string, len(rest))
	for i, r := range rest {
		ids[i] = r.ID
	}
	return q.SetImageOrder(ctx, recipeID, ids)
}

// SetImageOrder assigns positions 1..n in the given order (ids must be exactly the gallery set).
func (q Q) SetImageOrder(ctx context.Context, recipeID string, ids []string) error {
	if _, err := q.q.ExecContext(ctx, `UPDATE recipe_images SET position = position + 1000 WHERE recipe_id=? AND kind='recipe'`, recipeID); err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := q.q.ExecContext(ctx, `UPDATE recipe_images SET position=? WHERE id=? AND recipe_id=? AND kind='recipe'`, i+1, id, recipeID); err != nil {
			return err
		}
	}
	return nil
}

// AllImageKeys returns every storage key (sweeper).
func (q Q) AllImageKeys(ctx context.Context) (map[string]bool, error) {
	ks, err := q.strings(ctx, `SELECT storage_key FROM recipe_images`)
	m := map[string]bool{}
	for _, k := range ks {
		m[k] = true
	}
	return m, err
}

// ---- cook logs ----

// InsertCookLog stores a log and returns its id.
func (q Q) InsertCookLog(ctx context.Context, l *domain.CookLog) error {
	var imgID any
	if l.Image != nil {
		imgID = l.Image.ID
	}
	r, err := q.q.ExecContext(ctx, `INSERT INTO recipe_cook_logs (recipe_id, user_id, cooked_at, note, rating, image_id) VALUES (?,?,?,?,?,?)`,
		l.RecipeID, l.UserID, FormatTime(l.CookedAt), l.Note, nullInt(l.Rating), imgID)
	if err != nil {
		return err
	}
	l.ID, err = r.LastInsertId()
	return err
}

const logSelect = `SELECT c.id, c.recipe_id, c.user_id, COALESCE(u.display_name,''), c.cooked_at, c.note, c.rating, COALESCE(i.id,''), COALESCE(i.storage_key,''), COALESCE(i.width,0), COALESCE(i.height,0)
	FROM recipe_cook_logs c LEFT JOIN users u ON u.id=c.user_id LEFT JOIN recipe_images i ON i.id=c.image_id`

func scanLog(sc interface{ Scan(...any) error }) (domain.CookLog, error) {
	var l domain.CookLog
	var ca string
	var rt sql.NullInt64
	var imgID, key string
	var w, h int
	if err := sc.Scan(&l.ID, &l.RecipeID, &l.UserID, &l.AuthorName, &ca, &l.Note, &rt, &imgID, &key, &w, &h); err != nil {
		return l, err
	}
	l.CookedAt, l.Rating = ParseTime(ca), intPtr(rt)
	if imgID != "" {
		l.Image = &domain.Image{ID: imgID, RecipeID: l.RecipeID, Key: key, Kind: "cook_log", Width: w, Height: h}
	}
	return l, nil
}

// ListCookLogs lists logs of a recipe, newest first.
func (q Q) ListCookLogs(ctx context.Context, recipeID string) ([]domain.CookLog, error) {
	rows, err := q.q.QueryContext(ctx, logSelect+` WHERE c.recipe_id=? ORDER BY c.cooked_at DESC, c.id DESC`, recipeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.CookLog
	for rows.Next() {
		l, err := scanLog(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// GetCookLog loads one log of a recipe.
func (q Q) GetCookLog(ctx context.Context, recipeID string, id int64) (*domain.CookLog, error) {
	l, err := scanLog(q.q.QueryRowContext(ctx, logSelect+` WHERE c.recipe_id=? AND c.id=?`, recipeID, id))
	if err != nil {
		return nil, notFound(err)
	}
	return &l, nil
}

// DeleteCookLog deletes a log and its photo row; the returned key (if any) must be removed after commit.
func (q Q) DeleteCookLog(ctx context.Context, l *domain.CookLog) (string, error) {
	if _, err := q.q.ExecContext(ctx, `DELETE FROM recipe_cook_logs WHERE id=?`, l.ID); err != nil {
		return "", err
	}
	if l.Image == nil {
		return "", nil
	}
	_, err := q.q.ExecContext(ctx, `DELETE FROM recipe_images WHERE id=?`, l.Image.ID)
	return l.Image.Key, err
}

// CookStats aggregates the logs of one recipe.
func (q Q) CookStats(ctx context.Context, recipeID string) (domain.CookStats, error) {
	var st domain.CookStats
	var last sql.NullString
	var avg sql.NullFloat64
	err := q.q.QueryRowContext(ctx, `SELECT COUNT(*), MAX(cooked_at), AVG(rating), COUNT(rating) FROM recipe_cook_logs WHERE recipe_id=?`, recipeID).
		Scan(&st.Count, &last, &avg, &st.RatingCount)
	st.LastAt = parseNullTime(last)
	if avg.Valid {
		v := avg.Float64
		st.AvgRating = &v
	}
	return st, err
}

// ---- favourites and views ----

// SetFavourite marks or unmarks (idempotent).
func (q Q) SetFavourite(ctx context.Context, userID, recipeID string, on bool, now time.Time) error {
	if on {
		_, err := q.q.ExecContext(ctx, `INSERT OR IGNORE INTO recipe_favourites (user_id, recipe_id, created_at) VALUES (?,?,?)`, userID, recipeID, FormatTime(now))
		return err
	}
	_, err := q.q.ExecContext(ctx, `DELETE FROM recipe_favourites WHERE user_id=? AND recipe_id=?`, userID, recipeID)
	return err
}

// IsFavourite reports whether the user marked the recipe.
func (q Q) IsFavourite(ctx context.Context, userID, recipeID string) (bool, error) {
	var one int
	err := q.q.QueryRowContext(ctx, `SELECT 1 FROM recipe_favourites WHERE user_id=? AND recipe_id=?`, userID, recipeID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// UpsertView records a view at most once per window.
func (q Q) UpsertView(ctx context.Context, userID, recipeID string, now time.Time, window time.Duration) error {
	_, err := q.q.ExecContext(ctx, `INSERT INTO recipe_views (user_id, recipe_id, viewed_at) VALUES (?,?,?)
		ON CONFLICT(user_id, recipe_id) DO UPDATE SET viewed_at=excluded.viewed_at WHERE recipe_views.viewed_at < ?`,
		userID, recipeID, FormatTime(now), FormatTime(now.Add(-window)))
	return err
}

// DropOthersFavsViews deletes favourites and views of users other than the owner (unshare).
func (q Q) DropOthersFavsViews(ctx context.Context, recipeID, ownerID string) error {
	for _, t := range []string{"recipe_favourites", "recipe_views"} {
		if _, err := q.q.ExecContext(ctx, `DELETE FROM `+t+` WHERE recipe_id=? AND user_id<>?`, recipeID, ownerID); err != nil {
			return err
		}
	}
	return nil
}

var _ = strings.TrimSpace

// UserCookLogImageKeys lists photo keys of a user's cook logs (deleted with the user).
func (q Q) UserCookLogImageKeys(ctx context.Context, userID string) ([]string, error) {
	return q.strings(ctx, `SELECT i.storage_key FROM recipe_cook_logs c JOIN recipe_images i ON i.id=c.image_id WHERE c.user_id=?`, userID)
}

// ExecRaw executes a statement (maintenance).
func (q Q) ExecRaw(ctx context.Context, stmt string, args ...any) (sql.Result, error) {
	return q.q.ExecContext(ctx, stmt, args...)
}
