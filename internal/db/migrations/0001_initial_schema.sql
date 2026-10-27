-- +goose Up
-- +goose StatementBegin

CREATE TABLE users (
    id            TEXT PRIMARY KEY,                       -- UUID v4 (internal; never the OIDC sub)
    oidc_issuer   TEXT NOT NULL,
    oidc_subject  TEXT NOT NULL,
    email         TEXT NOT NULL DEFAULT '',               -- informational, refreshed each login, NOT unique
    display_name  TEXT NOT NULL,
    is_admin      INTEGER NOT NULL DEFAULT 0 CHECK (is_admin IN (0,1)),
    ui_language   TEXT CHECK (ui_language IN ('en','he')), -- NULL = not chosen yet
    last_login_at TEXT,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (oidc_issuer, oidc_subject)
);

CREATE TABLE oidc_login_states (
    state      TEXT PRIMARY KEY,
    verifier   TEXT NOT NULL,
    nonce      TEXT NOT NULL,
    return_to  TEXT NOT NULL DEFAULT '/',
    expires_at TEXT NOT NULL
);
CREATE INDEX idx_oidc_login_states_expires ON oidc_login_states(expires_at);

CREATE TABLE sessions (
    id_hash      TEXT PRIMARY KEY,                        -- hex SHA-256 of the cookie token; raw token never stored
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    id_token     TEXT,                                    -- for end-session id_token_hint
    user_agent   TEXT,
    ip_address   TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    last_seen_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    expires_at   TEXT NOT NULL
);
CREATE INDEX idx_sessions_user    ON sessions(user_id);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);

CREATE TABLE api_tokens (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,                    -- hex SHA-256 of the full token string
    token_prefix TEXT NOT NULL,                           -- first 8 chars after 'nwr_', for display
    scopes       TEXT NOT NULL DEFAULT 'inbox:write recipes:read', -- space-separated
    last_used_at TEXT,
    expires_at   TEXT,                                    -- NULL = never
    revoked_at   TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX idx_api_tokens_user ON api_tokens(user_id);

-- Categories (fixed, translated, admin-managed) and tags (free, user-created) share one table.
CREATE TABLE labels (
    id         INTEGER PRIMARY KEY,
    type       TEXT NOT NULL CHECK (type IN ('category','tag')),
    slug       TEXT NOT NULL,                             -- category: stable key, e.g. 'poultry'; tag: normalized name
    name       TEXT NOT NULL,                             -- category: English name; tag: the tag as typed (normalized)
    name_he    TEXT,                                      -- category: Hebrew name (required for categories); tag: NULL
    icon       TEXT,                                      -- category: emoji shown on the home tile; tag: NULL
    sort_order INTEGER NOT NULL DEFAULT 0,                -- category tile order
    created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (type, slug),
    CHECK (type = 'tag' OR name_he IS NOT NULL)
);

CREATE TABLE recipes (
    id                TEXT PRIMARY KEY,
    owner_id          TEXT REFERENCES users(id) ON DELETE SET NULL,
    status            TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('inbox','active')),
    visibility        TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private','shared')),
    title             TEXT NOT NULL,
    description       TEXT NOT NULL DEFAULT '',
    notes             TEXT NOT NULL DEFAULT '',           -- standing tips on the recipe itself (editable like any field)
    language          TEXT NOT NULL DEFAULT 'und' CHECK (language IN ('en','he','und')),
    servings          INTEGER CHECK (servings BETWEEN 1 AND 100),
    prep_minutes      INTEGER CHECK (prep_minutes >= 0),
    source_url        TEXT,
    source_name       TEXT,
    import_key        TEXT,                               -- API idempotency key (see 40 §4.2)
    version           INTEGER NOT NULL DEFAULT 1,         -- optimistic concurrency; +1 on every update
    categorized_at    TEXT,                               -- future LLM hook: NULL = not yet processed (20 §15)
    created_by        TEXT REFERENCES users(id) ON DELETE SET NULL,
    updated_by        TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    CHECK (status = 'active' OR visibility = 'private')   -- inbox items are always private
);
CREATE INDEX idx_recipes_owner_status ON recipes(owner_id, status);
CREATE INDEX idx_recipes_shared       ON recipes(updated_at DESC) WHERE status = 'active' AND visibility = 'shared';
CREATE INDEX idx_recipes_updated      ON recipes(updated_at DESC);
CREATE UNIQUE INDEX idx_recipes_import_key ON recipes(owner_id, import_key) WHERE import_key IS NOT NULL;
CREATE INDEX idx_recipes_source_url   ON recipes(owner_id, source_url) WHERE source_url IS NOT NULL;

CREATE TABLE recipe_ingredients (
    id        INTEGER PRIMARY KEY,
    recipe_id TEXT NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    position  INTEGER NOT NULL,                           -- 1..n, contiguous, set by the service from input order
    section   TEXT,                                       -- optional group header, e.g. "Dough", "Topping"
    amount    REAL CHECK (amount > 0),                    -- NULL = unquantified ("salt to taste")
    unit      TEXT CHECK (unit IN ('g','kg','ml','l','tsp','tbsp','pcs','pinch')),
    item      TEXT NOT NULL,                              -- e.g. "chicken thighs, skin-on"
    raw_text  TEXT NOT NULL,                              -- the line as the user typed/pasted it (shown if parsing failed)
    UNIQUE (recipe_id, position)
);

CREATE TABLE recipe_steps (
    id            INTEGER PRIMARY KEY,
    recipe_id     TEXT NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    position      INTEGER NOT NULL,
    text          TEXT NOT NULL,                          -- plain text; no Markdown in v1
    timer_seconds INTEGER CHECK (timer_seconds BETWEEN 1 AND 86400), -- optional tap-to-start timer (C3)
    UNIQUE (recipe_id, position)
);

-- Field semantics and validation: 25-oven-settings.md §2–§4.
CREATE TABLE oven_stages (
    id                 INTEGER PRIMARY KEY,
    recipe_id          TEXT NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    position           INTEGER NOT NULL,
    mode               TEXT NOT NULL CHECK (mode IN ('pizza','gourmet_roast','max_roast','smoker','bake','broil','dehydrate','keep_warm')),
    pizza_preset       TEXT CHECK (pizza_preset IN ('artisan','thin_crust','new_york','chicago','calzone','custom')),
    temperature_c      INTEGER CHECK (temperature_c BETWEEN 20 AND 400),  -- coarse DB guard; real limits in 25 §4. Gourmet Roast: Stage 1
    duration_seconds   INTEGER CHECK (duration_seconds >= 0),             -- Gourmet Roast: Stage 1
    temperature2_c     INTEGER CHECK (temperature2_c BETWEEN 20 AND 400), -- Gourmet Roast Stage 2 only
    duration2_seconds  INTEGER CHECK (duration2_seconds >= 0),            -- Gourmet Roast Stage 2 only
    preheat_minutes    INTEGER CHECK (preheat_minutes BETWEEN 0 AND 120),
    woodfire           INTEGER NOT NULL DEFAULT 0 CHECK (woodfire IN (0,1)),
    pellet_blend       TEXT CHECK (pellet_blend IN ('all_purpose','robust','other')),
    pellet_note        TEXT,                              -- e.g. "cherry", "hickory"
    accessories        TEXT NOT NULL DEFAULT '[]',        -- JSON array of Accessory keys (pizza_stone, pro_heat_pan, roast_rack, own_pan), sorted, unique
    target_core_temp_c INTEGER CHECK (target_core_temp_c BETWEEN 30 AND 100),
    notes              TEXT NOT NULL DEFAULT '',
    UNIQUE (recipe_id, position)
);

CREATE TABLE recipe_images (
    id          TEXT PRIMARY KEY,
    recipe_id   TEXT NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    storage_key TEXT NOT NULL UNIQUE,                     -- 32 hex chars; file layout in 20 §9
    kind        TEXT NOT NULL DEFAULT 'recipe' CHECK (kind IN ('recipe','cook_log')), -- cook_log photos are not in the gallery
    position    INTEGER,                                  -- recipe photos: 1 = cover; cook_log photos: NULL
    width       INTEGER NOT NULL,                         -- of the 'full' variant
    height      INTEGER NOT NULL,
    bytes       INTEGER NOT NULL,                         -- full + thumb on disk
    caption     TEXT,
    source_url  TEXT,                                     -- when fetched by the API
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (recipe_id, position),
    CHECK ((kind = 'recipe') = (position IS NOT NULL))
);

CREATE TABLE recipe_labels (
    recipe_id TEXT NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    label_id  INTEGER NOT NULL REFERENCES labels(id) ON DELETE CASCADE,
    source    TEXT NOT NULL DEFAULT 'user' CHECK (source IN ('user','assistant','llm')),
    PRIMARY KEY (recipe_id, label_id)
);
CREATE INDEX idx_recipe_labels_label ON recipe_labels(label_id);

CREATE TABLE recipe_cook_logs (
    id        INTEGER PRIMARY KEY,
    recipe_id TEXT NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    cooked_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    note      TEXT NOT NULL DEFAULT '',
    rating    INTEGER CHECK (rating BETWEEN 1 AND 5),
    image_id  TEXT UNIQUE REFERENCES recipe_images(id) ON DELETE SET NULL  -- optional photo (kind = 'cook_log')
);
CREATE INDEX idx_cook_logs_recipe ON recipe_cook_logs(recipe_id, cooked_at DESC);
CREATE INDEX idx_cook_logs_user   ON recipe_cook_logs(user_id, cooked_at DESC);

CREATE TABLE recipe_favourites (
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recipe_id  TEXT NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (user_id, recipe_id)
);

-- One row per (user, recipe): last time the user opened the recipe page or cook mode (B3).
CREATE TABLE recipe_views (
    user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recipe_id TEXT NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    viewed_at TEXT NOT NULL,
    PRIMARY KEY (user_id, recipe_id)
);
CREATE INDEX idx_recipe_views_user ON recipe_views(user_id, viewed_at DESC);

-- Full-text index; maintained by the service layer (20 §11). One row per ACTIVE recipe.
CREATE VIRTUAL TABLE recipe_fts USING fts5(
    recipe_id UNINDEXED,
    title,
    description,
    ingredients,
    steps,
    labels,        -- category names (EN + HE) and tag names
    notes,         -- recipes.notes + all cook-log notes
    tokenize = 'trigram remove_diacritics 1'
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS recipe_fts;
DROP TABLE IF EXISTS recipe_views;
DROP TABLE IF EXISTS recipe_favourites;
DROP TABLE IF EXISTS recipe_cook_logs;
DROP TABLE IF EXISTS recipe_labels;
DROP TABLE IF EXISTS recipe_images;
DROP TABLE IF EXISTS oven_stages;
DROP TABLE IF EXISTS recipe_steps;
DROP TABLE IF EXISTS recipe_ingredients;
DROP TABLE IF EXISTS recipes;
DROP TABLE IF EXISTS labels;
DROP TABLE IF EXISTS api_tokens;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS oidc_login_states;
DROP TABLE IF EXISTS users;
-- +goose StatementEnd
