-- +goose Up
-- +goose StatementBegin
ALTER TABLE recipes ADD COLUMN source_key TEXT;
DROP INDEX idx_recipes_source_url;
CREATE INDEX idx_recipes_source_key ON recipes(owner_id, source_key) WHERE source_key IS NOT NULL;

ALTER TABLE users ADD COLUMN assistant_brief TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN assistant_brief_updated_at TEXT;

CREATE TABLE inbox_outcomes (
    id         INTEGER PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id    TEXT NOT NULL,
    title      TEXT NOT NULL,
    source_url TEXT,
    source_key TEXT,
    outcome    TEXT NOT NULL CHECK (outcome IN ('kept','rejected')),
    reason     TEXT CHECK (reason IN ('not_our_taste','too_complicated','not_for_our_oven','already_have','poor_details')),
    note       TEXT NOT NULL DEFAULT '',
    edited     INTEGER NOT NULL DEFAULT 0 CHECK (edited IN (0,1)),
    shared     INTEGER NOT NULL DEFAULT 0 CHECK (shared IN (0,1)),
    decided_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    CHECK (outcome = 'rejected' OR (reason IS NULL AND note = '')),
    CHECK (outcome = 'kept' OR (edited = 0 AND shared = 0))
);
CREATE INDEX idx_inbox_outcomes_user   ON inbox_outcomes(user_id, id);
CREATE INDEX idx_inbox_outcomes_source ON inbox_outcomes(user_id, source_key) WHERE source_key IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS inbox_outcomes;
ALTER TABLE users DROP COLUMN assistant_brief_updated_at;
ALTER TABLE users DROP COLUMN assistant_brief;
DROP INDEX IF EXISTS idx_recipes_source_key;
ALTER TABLE recipes DROP COLUMN source_key;
CREATE INDEX idx_recipes_source_url ON recipes(owner_id, source_url) WHERE source_url IS NOT NULL;
-- +goose StatementEnd
