# 60 — Go Backend: Code Conventions & Testing

Owner: Go lead. Status: **canonical** for dependencies, code structure rules, error handling, view models and the
test strategy. It does not restate architecture (`20`), schema (`30`), routes (`40`) or oven rules (`25`); where those
documents define something, this one only says how to implement it.

## 1. Toolchain and dependencies

- Go 1.25.x pinned in `go.mod` (`go 1.25.0` + `toolchain`) and in the Dockerfile `GO_VERSION`.
- `CGO_ENABLED=0` for every build artifact. cgo is used only by `go test -race`.
- Allowed direct dependencies (anything else needs a spec change):

| Module | Purpose |
|---|---|
| `modernc.org/sqlite` | SQLite driver (pure Go; bundles SQLite ≥ 3.45 for trigram `remove_diacritics`) |
| `github.com/pressly/goose/v3` | Migrations |
| `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2` | OIDC client |
| `github.com/nicksnyder/go-i18n/v2`, `github.com/BurntSushi/toml`, `golang.org/x/text` | i18n, language matching, NFKC |
| `golang.org/x/image` | WebP decode, CatmullRom resize |
| `github.com/rwcarlsen/goexif` | EXIF orientation |
| `github.com/google/uuid` | UUID v4 |
| `github.com/modelcontextprotocol/go-sdk` | MCP server (`40 §9.2`); latest release at implementation time |
| `golang.org/x/net` | `idna` for `textnorm.SourceKey` (`40 §4.3`); `html` is **tests only**: HTML parsing for route/flag tests (§6.3) |
| `github.com/google/jsonschema-go` | **tests only**: validating examples against `openapi.json` (§6.3 item 6); already required by the MCP SDK |

No HEIC decoder (HEIC is rejected, `20 §8`). No web framework, no ORM, no JS build tooling.
Front-end vendored files: `web/static/vendor/htmx.min.js` (pinned version + SHA-256 in `Taskfile.yml`).

## 2. Structure rules

Package layout: `20 §3`. In addition:

1. **Routing**: one `routes.go` per surface (`internal/web`, `internal/api`) registering every route of `40` with a
   method pattern: `mux.Handle("POST /recipes/{id}/edit", h.updateRecipe)`. No catch-all handlers that inspect
   `r.URL.Path` suffixes or `r.Method`. Path parameters via `r.PathValue`.
2. **URL building**: templates get route URLs from a template func `url "recipe.edit" .Recipe.ID` backed by one Go
   table of named routes, also used by `routes.go`. Hard-coded paths in templates are not allowed
   (`task lint` greps for `href="/` and `action="/` in `web/templates`, allowing only `/static` via the `asset` func).
3. **Handlers are thin**: parse input → call one service method → render. They do not run SQL, do not check
   permissions themselves, and do not build user-visible text except through i18n keys.
4. **Typed view models**: each page has a struct (`web/views.go`), never `map[string]any`. Every view model embeds
   `Layout{Lang, Dir, User, InboxCount, Flash, CSRFOrigin, AssetVersion}`. Action flags are fields computed with
   `authz` (`CanEdit bool` etc., `20 §7`); the fields required per page are listed in `70 §4`. A test asserts each
   flag (§6.3).
5. **Services** (`internal/service`) are the only place that opens write transactions, calls `authz`,
   validates, maintains FTS and publishes events. Signatures take `ctx, actor *domain.User` first.
   Minimum set:

```go
CreateRecipe(ctx, actor, in RecipeInput) (*Recipe, error)
UpdateRecipe(ctx, actor, id string, version int, in RecipeInput) (*Recipe, error)
DeleteRecipe(ctx, actor, id string) error
SetVisibility(ctx, actor, id string, shared bool) error
CopyRecipe(ctx, actor, id string) (*Recipe, error)
SetFavourite(ctx, actor, id string, on bool) error
LogCook(ctx, actor, id string, note string, rating *int) error
DeleteCookLog(ctx, actor, recipeID string, logID int64) error
AddImage(ctx, actor, recipeID string, r io.Reader) (*Image, error)
DeleteImage(ctx, actor, recipeID, imageID string) error
ReorderImages(ctx, actor, recipeID string, ids []string) error
GetRecipe(ctx, actor, id string) (*RecipeView, error)        // includes the actor's permission flags
Search(ctx, actor, q SearchQuery) (SearchPage, error)
Home(ctx, actor) (HomeData, error)
ImportToInbox(ctx, actor, in RecipeImport, idemKey string) (*ImportResult, error)
AcceptInbox(ctx, actor, id string, share bool) (nextID string, err error)
EditAndAcceptInbox(ctx, actor, id string, in RecipeInput) (nextID string, err error)
RejectInbox(ctx, actor, id string, reason domain.RejectReason, note string) (nextID string, err error)
ListOutcomes(ctx, actor, after string, limit int) (OutcomePage, error)
GetAssistantBrief(ctx, actor) (AssistantBrief, error)
SetAssistantBrief(ctx, actor, text string) error
// tokens, users, labels, admin categories likewise
```

6. **Errors**: services return sentinel or typed errors from `internal/domain`:
   `ErrNotFound`, `ErrForbidden`, `ErrConflict` (version), `ErrDuplicate{ExistingID}`, `*ValidationError{Errors, Warnings []oven.Issue-like}`,
   `ErrTooLarge`, `ErrUnsupportedMedia`. One function per surface maps them to HTTP (`web.renderError`,
   `api.writeProblem`). Raw `err.Error()` strings are never shown to users (they are logged with the request ID).
7. **Transactions**: `store.WithTx(ctx, writer, func(tx *sql.Tx) error)`; `BEGIN IMMEDIATE` via `_txlock=immediate`.
   File-system side effects (image deletion) are queued in the tx context and run after commit.
8. **Time**: inject a `Clock` interface in services and jobs so tests can control `now`.
9. **Background jobs** (`internal/jobs`): hourly purge, nightly sweeper, daily backup; each a goroutine with a ticker,
   stopped on shutdown; each run logged with duration.

## 3. Database layer

- `db.Open(cfg) (reader, writer *sql.DB, err error)` applies the DSN of `20 §14` and the pool sizes; runs goose and the
  version guard.
- Repositories live in `internal/store`, one file per aggregate, using `database/sql` with hand-written SQL
  (no ORM). Each repository method takes a `Querier` (`*sql.DB` or `*sql.Tx`).
- Timestamps are parsed/formatted with a single helper pair (`store.formatTime`, `store.parseTime`) matching `30 §1`.
- Recipe children (ingredients, steps, stages, labels) are replaced wholesale on update (delete + insert) inside the
  transaction; positions are renumbered from 1.

## 4. Oven validation

Implement exactly `25 §4` as data in `internal/oven/spec.go` (one struct per mode with recommended/accepted bands and
field rules) and a generic `Validate` that interprets the table. No per-mode `switch` with hard-coded numbers.
`GET /api/v1/meta/oven` and the editor's embedded JSON are serialized from the same table.

## 5. Images

Implement `20 §9`. `internal/images` exposes `Process`, `Fetch` (SSRF-guarded client), `Store`/`Remove` and the sweeper.
The semaphore (`IMAGE_WORKERS`) wraps `Process` for both browser uploads and API imports.

## 6. Testing strategy

All tests run with `task test` (unit + integration, no network) and `task test:e2e` (against the dev mock stack).
Coverage target: ≥ 80 % for `oven`, `authz`, `service`, `search` normalization and the ingredient parser.

### 6.1 Unit tests
- `oven`: every cell of the `25 §4` table (required/forbidden/hard/soft bounds), normalization.
- `authz`: every cell of the `20 §7` table, for owner / other user / admin / ownerless shared recipe.
- Search normalization (final letters, niqqud, NFKC, case) and query building (short terms, quoting).
- Ingredient line parser (`40 §2.6`), including Hebrew units and decimal commas.
- Config parsing (required vars, `_FILE` handling, both-set error, CIDR parsing).
- `return_to` validation, token format/hashing, SSRF IP classification (including mapped/NAT64 forms).

### 6.2 Store and service tests
Against a fresh SQLite file in `t.TempDir()` with migrations applied. Cover: create/update/delete with children and
FTS rows; version conflict; visibility change side effects (`30 §3`); user deletion procedure; copy duplicates
image files; inbox idempotency key and duplicate `source_url`; image deletion after commit; search with every filter.

### 6.3 HTTP integration tests (required — these catch broken links and missing buttons)
Use the real router from `routes.go` with `httptest.Server`. A test helper creates users and sessions **directly in
the DB** (the binary has no login bypass).

1. **Route-table test**: for every named route, assert the mux resolves it (`mux.Handler(req)` returns the expected
   pattern) and that wrong methods give 405.
2. **Rendered-link crawl**: for each page (home, list, recipe, cook, editor, inbox list/item, settings, tokens,
   admin categories) rendered as (a) owner, (b) another user, (c) admin, parse the HTML and, for every `<a href>` and
   every `<form action method>`, send the request (forms with their own fields) and assert the response is not
   404/405 and, for forms, that the intended state change happened. *An Edit link or delete form pointing to an
   unregistered path fails this test.*
3. **Permission-flag matrix**: on `/recipes/{id}` for private/shared × owner/other/admin, assert presence or absence of
   the Edit link, Delete button, visibility switch, Copy button, favourite heart, cook-log form (expected values from
   `70 §4.3`). Assert 404 for a private recipe of another user.
4. **Story flows** (one test each): create → view → edit (title and a stage change persist) → share → second user
   edits → owner sees "Last changed by" → delete; inbox import via API → item page → edit-and-keep → appears in search;
   import → throw away with a reason → `GET /api/v1/inbox/outcomes` shows it → re-import of the same URL with
   `utm_*` added and `?allow_duplicate=1` → 409 `previously-reviewed`;
   version conflict shows the 409 page; expired session on POST redirects to `/signin` with `return_to`.
5. **Security headers**: CSP exactly as `20 §8` on every HTML page; no `'unsafe-inline'`.
6. **Assistant surfaces in step**: every `/api/v1` route in `routes.go` (except `/mcp`) has a path + method in
   `openapi.json` and vice versa; the §4.1 example validates against its schema; every MCP tool in `40 §9.2`
   is listed by `tools/list` for a token with both scopes and returns the same result as its REST twin.

### 6.4 End-to-end (`test/`, `task test:e2e`)
Go tests driving the running dev mock stack over HTTP: real OIDC login through mock-oauth2-server
(form login), API token creation, image upload of a real JPEG, backup subcommand, restart persistence (session
still valid after `docker compose restart app`).

### 6.5 Manual device checklist
Browser-only behaviour (wake lock, timers, vibration, camera capture, RTL rendering, install to home screen) is
verified with the checklist in `70 §10` before each release, on at least one Galaxy phone and the Tab S6 Lite.
