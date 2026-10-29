-- +goose Up
-- +goose StatementBegin
CREATE TABLE app_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_by TEXT REFERENCES users(id) ON DELETE SET NULL
);
ALTER TABLE labels ADD COLUMN description TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE labels DROP COLUMN description;
DROP TABLE IF EXISTS app_settings;
-- +goose StatementEnd
