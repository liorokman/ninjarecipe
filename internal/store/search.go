package store

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/liorokman/ninjarecipe/internal/domain"
	"github.com/liorokman/ninjarecipe/internal/oven"
	"github.com/liorokman/ninjarecipe/internal/textnorm"
)

const defaultPage = 24

func encCursor(key string, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(key + "\x00" + id))
}

func decCursor(c string) (key, id string, ok bool) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", "", false
	}
	k, i, found := strings.Cut(string(b), "\x00")
	return k, i, found
}

// Search runs a visibility-filtered, keyset-paginated search for the actor (20 §11, 40 §2.3).
func (q Q) Search(ctx context.Context, actorID string, sq domain.SearchQuery) (domain.SearchPage, error) {
	limit := sq.Limit
	if limit <= 0 || limit > 100 {
		limit = defaultPage
	}
	var where []string
	var args []any
	add := func(cond string, a ...any) { where = append(where, cond); args = append(args, a...) }

	add(`r.status='active'`)
	add(`(r.visibility='shared' OR r.owner_id=?)`, actorID)
	switch sq.Scope {
	case "mine":
		add(`r.owner_id=?`, actorID)
	case "family":
		add(`r.visibility='shared'`)
	}
	if len(sq.Categories) > 0 {
		add(`EXISTS (SELECT 1 FROM recipe_labels rl JOIN labels l ON l.id=rl.label_id WHERE rl.recipe_id=r.id AND l.type='category' AND l.slug IN (`+placeholders(len(sq.Categories))+`))`, strArgs(sq.Categories)...)
	}
	for _, t := range sq.Tags {
		add(`EXISTS (SELECT 1 FROM recipe_labels rl JOIN labels l ON l.id=rl.label_id WHERE rl.recipe_id=r.id AND l.type='tag' AND l.slug=?)`, strings.ToLower(t))
	}
	if len(sq.Modes) > 0 {
		add(`EXISTS (SELECT 1 FROM oven_stages s WHERE s.recipe_id=r.id AND s.mode IN (`+placeholders(len(sq.Modes))+`))`, strArgs(sq.Modes)...)
	}
	if sq.Smoked {
		add(`EXISTS (SELECT 1 FROM oven_stages s WHERE s.recipe_id=r.id AND (s.mode='smoker' OR s.woodfire=1))`)
	}
	if sq.MaxTimeMin > 0 {
		add(`(SELECT COALESCE(SUM(COALESCE(duration_seconds,0)+COALESCE(duration2_seconds,0)),0) FROM oven_stages s WHERE s.recipe_id=r.id) <= ?`, sq.MaxTimeMin*60)
	}
	if sq.SourceURL != "" {
		add(`r.source_url=?`, sq.SourceURL)
	}
	if sq.Fav {
		add(`EXISTS (SELECT 1 FROM recipe_favourites f WHERE f.recipe_id=r.id AND f.user_id=?)`, actorID)
	}

	join := ""
	var joinArgs []any
	long, short := textnorm.Terms(sq.Q)
	relevance := sq.Sort == "relevance" && len(long) > 0
	if len(long) > 0 {
		join = ` JOIN (SELECT recipe_id AS rid, bm25(recipe_fts, 0, 10, 2, 3, 1, 4, 1) AS rank FROM recipe_fts WHERE recipe_fts MATCH ?) fx ON fx.rid=r.id`
		joinArgs = append(joinArgs, textnorm.MatchExpr(long))
	}
	for _, t := range short {
		pat := "%" + textnorm.EscapeLike(t) + "%"
		add(`EXISTS (SELECT 1 FROM recipe_fts f2 WHERE f2.recipe_id=r.id AND (f2.title LIKE ? ESCAPE '\' OR f2.labels LIKE ? ESCAPE '\'))`, pat, pat)
	}

	// sort key expression, direction, numeric?
	var keyExpr string
	desc := true
	numeric := false
	switch {
	case relevance:
		keyExpr, desc, numeric = `fx.rank`, false, true
	case sq.Sort == "title":
		keyExpr, desc = `lower(r.title)`, false
	case sq.Sort == "cooked":
		keyExpr, numeric = `(SELECT COUNT(*) FROM recipe_cook_logs c WHERE c.recipe_id=r.id)`, true
	case sq.Sort == "rating":
		keyExpr, numeric = `COALESCE((SELECT AVG(rating) FROM recipe_cook_logs c WHERE c.recipe_id=r.id),0)`, true
	case sq.Sort == "recent":
		keyExpr = `MAX(COALESCE((SELECT viewed_at FROM recipe_views v WHERE v.recipe_id=r.id AND v.user_id=?),''), COALESCE((SELECT MAX(cooked_at) FROM recipe_cook_logs c WHERE c.recipe_id=r.id AND c.user_id=?),''))`
		joinArgs = append(joinArgs, actorID, actorID)
		_ = joinArgs
	default:
		keyExpr = `r.updated_at`
	}
	// For "recent" the key subqueries need their args in SELECT/WHERE order; handled below by inlining actor id.
	if sq.Sort == "recent" && !relevance {
		keyExpr = strings.ReplaceAll(keyExpr, "?", "'"+strings.ReplaceAll(actorID, "'", "''")+"'")
		joinArgs = joinArgs[:len(joinArgs)-2]
		add(keyExpr + ` <> ''`)
	}

	cmp := "<"
	if !desc {
		cmp = ">"
	}
	if sq.Cursor != "" {
		if ck, cid, ok := decCursor(sq.Cursor); ok {
			if numeric {
				f, _ := strconv.ParseFloat(ck, 64)
				add(fmt.Sprintf(`(%s %s ? OR (%s = ? AND r.id %s ?))`, keyExpr, cmp, keyExpr, cmp), f, f, cid)
			} else {
				add(fmt.Sprintf(`(%s %s ? OR (%s = ? AND r.id %s ?))`, keyExpr, cmp, keyExpr, cmp), ck, ck, cid)
			}
		}
	}
	dir := "DESC"
	if !desc {
		dir = "ASC"
	}
	query := fmt.Sprintf(`SELECT r.id, %s FROM recipes r%s WHERE %s ORDER BY %s %s, r.id %s LIMIT ?`, keyExpr, join, strings.Join(where, " AND "), keyExpr, dir, dir)
	all := append(append([]any{}, joinArgs...), args...)
	all = append(all, limit+1)
	rows, err := q.q.QueryContext(ctx, query, all...)
	if err != nil {
		return domain.SearchPage{}, err
	}
	type hit struct{ id, key string }
	var hits []hit
	for rows.Next() {
		var id string
		var k any
		if err := rows.Scan(&id, &k); err != nil {
			rows.Close()
			return domain.SearchPage{}, err
		}
		var ks string
		switch v := k.(type) {
		case float64:
			ks = strconv.FormatFloat(v, 'g', -1, 64)
		case int64:
			ks = strconv.FormatInt(v, 10)
		case string:
			ks = v
		case []byte:
			ks = string(v)
		}
		hits = append(hits, hit{id, ks})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return domain.SearchPage{}, err
	}
	page := domain.SearchPage{}
	if len(hits) > limit {
		last := hits[limit-1]
		page.NextCursor = encCursor(last.key, last.id)
		hits = hits[:limit]
	}
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.id
	}
	page.Items, err = q.Summaries(ctx, actorID, ids)
	return page, err
}

// Summaries loads card data for ids, preserving the order of ids.
func (q Q) Summaries(ctx context.Context, actorID string, ids []string) ([]domain.RecipeSummary, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	in := placeholders(len(ids))
	idArgs := strArgs(ids)
	rows, err := q.q.QueryContext(ctx, `SELECT r.id, COALESCE(r.owner_id,''), COALESCE(o.display_name,''), r.status, r.visibility, r.title, r.language,
		COALESCE(r.source_url,''), r.created_at, r.updated_at,
		EXISTS(SELECT 1 FROM recipe_favourites f WHERE f.recipe_id=r.id AND f.user_id=?),
		(SELECT COUNT(*) FROM recipe_cook_logs c WHERE c.recipe_id=r.id), (SELECT MAX(cooked_at) FROM recipe_cook_logs c WHERE c.recipe_id=r.id),
		(SELECT AVG(rating) FROM recipe_cook_logs c WHERE c.recipe_id=r.id)
		FROM recipes r LEFT JOIN users o ON o.id=r.owner_id WHERE r.id IN (`+in+`)`, append([]any{actorID}, idArgs...)...)
	if err != nil {
		return nil, err
	}
	byID := map[string]*domain.RecipeSummary{}
	for rows.Next() {
		var s domain.RecipeSummary
		var ca, ua string
		var fav int
		var last, avg any
		if err := rows.Scan(&s.ID, &s.OwnerID, &s.OwnerName, &s.Status, &s.Visibility, &s.Title, &s.Language, &s.SourceURL, &ca, &ua, &fav, &s.CookCount, &last, &avg); err != nil {
			rows.Close()
			return nil, err
		}
		s.CreatedAt, s.UpdatedAt, s.IsFavourite = ParseTime(ca), ParseTime(ua), fav == 1
		if v, ok := last.(string); ok && v != "" {
			t := ParseTime(v)
			s.LastCookedAt = &t
		}
		if v, ok := avg.(float64); ok {
			s.AvgRating = &v
		}
		byID[s.ID] = &s
	}
	rows.Close()

	rows, err = q.q.QueryContext(ctx, `SELECT recipe_id, position, mode, temperature_c, duration_seconds, duration2_seconds, woodfire FROM oven_stages WHERE recipe_id IN (`+in+`) ORDER BY recipe_id, position`, idArgs...)
	if err != nil {
		return nil, err
	}
	stages := map[string][]domain.OvenStage{}
	for rows.Next() {
		var id string
		var st domain.OvenStage
		var t, d1, d2 *int
		var wood int
		if err := rows.Scan(&id, &st.Position, &st.Mode, &t, &d1, &d2, &wood); err != nil {
			rows.Close()
			return nil, err
		}
		st.TemperatureC, st.DurationSeconds, st.Duration2Seconds, st.Woodfire = t, d1, d2, wood == 1
		stages[id] = append(stages[id], st)
	}
	rows.Close()

	rows, err = q.q.QueryContext(ctx, `SELECT recipe_id, storage_key FROM recipe_images WHERE recipe_id IN (`+in+`) AND kind='recipe' AND position=1`, idArgs...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, key string
		if err := rows.Scan(&id, &key); err != nil {
			rows.Close()
			return nil, err
		}
		if s := byID[id]; s != nil {
			s.CoverKey = key
		}
	}
	rows.Close()

	rows, err = q.q.QueryContext(ctx, `SELECT rl.recipe_id, l.type, l.slug FROM recipe_labels rl JOIN labels l ON l.id=rl.label_id WHERE rl.recipe_id IN (`+in+`) ORDER BY l.sort_order, l.name`, idArgs...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, typ, slug string
		if err := rows.Scan(&id, &typ, &slug); err != nil {
			rows.Close()
			return nil, err
		}
		if s := byID[id]; s != nil {
			if typ == "category" {
				s.Categories = append(s.Categories, slug)
			} else {
				s.Tags = append(s.Tags, slug)
			}
		}
	}
	rows.Close()

	out := make([]domain.RecipeSummary, 0, len(ids))
	for _, id := range ids {
		s := byID[id]
		if s == nil {
			continue
		}
		sts := stages[id]
		if p := oven.PrimaryStage(sts); p != nil {
			s.PrimaryMode, s.PrimaryTempC = p.Mode, p.TemperatureC
		}
		s.TotalSeconds = oven.TotalSeconds(sts)
		s.UsesWoodfire = oven.UsesWoodfire(sts)
		out = append(out, *s)
	}
	return out, nil
}

// Recent returns the actor's recently cooked/viewed recipes (union, newest first, limit), restricted to what they can view.
func (q Q) Recent(ctx context.Context, actorID string, limit int) ([]domain.RecipeSummary, error) {
	rows, err := q.q.QueryContext(ctx, `SELECT id, at FROM (
		SELECT r.id AS id, MAX(t.at) AS at FROM recipes r JOIN (
			SELECT recipe_id, viewed_at AS at FROM recipe_views WHERE user_id=?
			UNION ALL SELECT recipe_id, cooked_at AS at FROM recipe_cook_logs WHERE user_id=?) t ON t.recipe_id=r.id
		WHERE r.status='active' AND (r.visibility='shared' OR r.owner_id=?) GROUP BY r.id) ORDER BY at DESC LIMIT ?`, actorID, actorID, actorID, limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	ats := map[string]time.Time{}
	for rows.Next() {
		var id, at string
		if err := rows.Scan(&id, &at); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
		ats[id] = ParseTime(at)
	}
	rows.Close()
	out, err := q.Summaries(ctx, actorID, ids)
	for i := range out {
		t := ats[out[i].ID]
		out[i].At = &t
	}
	return out, err
}

// Favourites returns the actor's favourites the actor can still view, newest first.
func (q Q) Favourites(ctx context.Context, actorID string, limit int) ([]domain.RecipeSummary, error) {
	ids, err := q.strings(ctx, `SELECT r.id FROM recipe_favourites f JOIN recipes r ON r.id=f.recipe_id
		WHERE f.user_id=? AND r.status='active' AND (r.visibility='shared' OR r.owner_id=?) ORDER BY f.created_at DESC LIMIT ?`, actorID, actorID, limit)
	if err != nil {
		return nil, err
	}
	return q.Summaries(ctx, actorID, ids)
}
