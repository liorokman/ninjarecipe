package store

import (
	"context"
	"strings"
	"time"
)

// Settings returns every app_settings row whose key starts with prefix.
func (q Q) Settings(ctx context.Context, prefix string) (map[string]string, error) {
	rows, err := q.q.QueryContext(ctx, `SELECT key, value FROM app_settings WHERE key LIKE ? ESCAPE '\'`,
		strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix)+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// SetSetting upserts one setting.
func (q Q) SetSetting(ctx context.Context, key, value, by string, now time.Time) error {
	_, err := q.q.ExecContext(ctx, `INSERT INTO app_settings (key, value, updated_at, updated_by) VALUES (?,?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at, updated_by=excluded.updated_by`,
		key, value, FormatTime(now), nullStr(by))
	return err
}

// DeleteSetting removes one setting (the environment default applies again).
func (q Q) DeleteSetting(ctx context.Context, key string) error {
	_, err := q.q.ExecContext(ctx, `DELETE FROM app_settings WHERE key=?`, key)
	return err
}
