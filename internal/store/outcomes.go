package store

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	"github.com/liorokman/ninjarecipe/internal/domain"
)

// InsertOutcome records a Keep / Throw away decision (30 §5a). o.ID is set on return.
func (q Q) InsertOutcome(ctx context.Context, userID string, o *domain.Outcome) error {
	var reason any
	if o.Reason != "" {
		reason = string(o.Reason)
	}
	res, err := q.q.ExecContext(ctx, `INSERT INTO inbox_outcomes (user_id, item_id, title, source_url, source_key, outcome, reason, note, edited, shared, decided_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		userID, o.ItemID, o.Title, nullStr(o.SourceURL), nullStr(o.SourceKey), o.Outcome, reason, o.Note, boolInt(o.Edited), boolInt(o.Shared), FormatTime(o.DecidedAt))
	if err != nil {
		return err
	}
	o.ID, err = res.LastInsertId()
	return err
}

// FindOutcomeBySourceKey returns the earliest decision of the user on a source, or ErrNotFound.
func (q Q) FindOutcomeBySourceKey(ctx context.Context, userID, key string) (*domain.Outcome, error) {
	rows, err := q.listOutcomes(ctx, `SELECT `+outcomeCols+` FROM inbox_outcomes WHERE user_id=? AND source_key=? ORDER BY id LIMIT 1`, userID, key)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, domain.ErrNotFound
	}
	return &rows[0], nil
}

// ListOutcomes returns up to limit decisions with id > afterID, oldest first. RecipeID is set for kept items whose recipe still exists.
func (q Q) ListOutcomes(ctx context.Context, userID string, afterID int64, limit int) ([]domain.Outcome, error) {
	return q.listOutcomes(ctx, `SELECT `+outcomeCols+` FROM inbox_outcomes WHERE user_id=? AND id>? ORDER BY id LIMIT ?`, userID, afterID, limit)
}

const outcomeCols = `id, item_id, title, COALESCE(source_url,''), COALESCE(source_key,''), outcome, COALESCE(reason,''), note, edited, shared, decided_at,
	CASE WHEN outcome='kept' THEN COALESCE((SELECT id FROM recipes WHERE id=item_id),'') ELSE '' END`

func (q Q) listOutcomes(ctx context.Context, query string, args ...any) ([]domain.Outcome, error) {
	rows, err := q.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Outcome
	for rows.Next() {
		var o domain.Outcome
		var reason, at string
		var edited, shared int
		if err := rows.Scan(&o.ID, &o.ItemID, &o.Title, &o.SourceURL, &o.SourceKey, &o.Outcome, &reason, &o.Note, &edited, &shared, &at, &o.RecipeID); err != nil {
			return nil, err
		}
		o.Reason, o.Edited, o.Shared, o.DecidedAt = domain.RejectReason(reason), edited == 1, shared == 1, ParseTime(at)
		out = append(out, o)
	}
	return out, rows.Err()
}

// ParseOutcomeCursor parses an outcome cursor ("" = 0).
func ParseOutcomeCursor(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, domain.ErrBadRequest
	}
	return n, nil
}

// GetAssistantBrief loads the user's brief text and its update time (nil when never set).
func (q Q) GetAssistantBrief(ctx context.Context, userID string) (string, *time.Time, error) {
	var text string
	var at sql.NullString
	err := q.q.QueryRowContext(ctx, `SELECT assistant_brief, assistant_brief_updated_at FROM users WHERE id=?`, userID).Scan(&text, &at)
	if err != nil {
		return "", nil, notFound(err)
	}
	return text, parseNullTime(at), nil
}

// SetAssistantBrief stores the brief ("" clears it and its timestamp).
func (q Q) SetAssistantBrief(ctx context.Context, userID, text, now string) error {
	var at any
	if text != "" {
		at = now
	}
	res, err := q.q.ExecContext(ctx, `UPDATE users SET assistant_brief=?, assistant_brief_updated_at=? WHERE id=?`, text, at, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
