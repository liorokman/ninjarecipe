# Implementation Plan: align code with the spec

Status of this plan: authored 2026-10-27 after auditing `cmd/ internal/ web/ locales/ test/ Dockerfile deploy/ Taskfile.yml docker-compose.yml` against `docs/specs/*` (all of it). Spec wins everywhere; where the spec is inconsistent the decision is in section 3 (D-numbers).

Verdict: the code builds and its own tests pass, but it implements an older model. Roughly 85 % of domain, oven, schema, store, service, web, api, templates and JS must be rewritten. Reusable (adapt, not rewrite): `internal/images/{ssrf,storage,pipeline}` (fix formats), `internal/httpx/{ip,ratelimit}`, `internal/events`, `internal/categorize`, `internal/auth/{session,bearer}` (fix details), `internal/i18n` helpers, `internal/config` (rename vars). Delete: `internal/web/handlers_dev.go` (auth bypass, forbidden by 20 §5.5), root `docker-compose.yml`, `deploy/docker-compose*.yml`, all old tests asserting old behaviour.

Module path is `github.com/liorokman/ninjarecipe`. Installed: Go 1.27.2, node 24.14, google-chrome 156 (`/usr/bin/google-chrome`), docker, task. No playwright browsers, no golangci-lint, no goose binary. Network works (npm/go proxy).

---------------------------------------------------------------------------------------------------

## 1. Prioritized gap list

Legend: P0 = blocks everything / security; P1 = required feature; P2 = ops/polish. Files are current paths.

### P0 foundation

| # | Spec | Wrong / missing | Files |
|---|---|---|---|
| G1 | 25 §1-3 | Wrong oven vocabulary. Has `specialty_roast`, `top_heat`, presets `thin/pan/frozen`, accessory `accessory_frame`, `top_heat_level`, `pellet_scoops`, `frame_level low/high` as input. Missing `gourmet_roast`, `broil`, presets `thin_crust/chicago/calzone`, accessory `own_pan`, enum `chicago`. | `internal/domain/enums.go`, `internal/oven/*`, locales, templates, JS |
| G2 | 25 §2 | OvenStage lacks `temperature2_c`, `duration2_seconds`, `preheat_minutes`, `pellet_note`; has stray fields; `frame_level`/`preheat` must be derived output only. | `internal/domain/recipe.go`, store, api DTOs |
| G3 | 25 §4, 60 §4 | Validation uses hard-coded per-mode ranges (50-200 etc.), no 40-370 uniform hard limit, no recommended-vs-accepted warnings, no `Issue{Field,Code,Params}`/`Normalize`/`Validate(s)(errs,warns)` contract, no Gourmet two-phase rules, no cross-field rules (preset iff pizza, woodfire forced/forbidden, preheat_minutes only for preheat modes, pellet fields only if woodfire, <=10 stages). Spec must be data-driven table. | `internal/oven/{spec,validate,derive}.go` |
| G4 | 25 §5 | Derived values: stage time includes stage 2, primary mode tie-break, `uses woodfire` also true for smoker, frame level, preheat. | `internal/oven/derive.go` |
| G5 | 25 §6 | `/api/v1/meta/oven` and `#oven-spec` must come from one serializer of the table. | `internal/api/oven.go`, `internal/oven/spec.go` |
| G6 | 30 §1,4,5 | Schema is wrong: DATETIME instead of ms RFC3339 TEXT; no `recipes.version/import_key/categorized_at/created_by/updated_by`, `rating`+`difficulty`+`cook_time` columns shouldn't exist, `status archived` shouldn't exist; `users.email` UNIQUE index must not exist, `ui_language` nullable; ingredient columns (`section,raw_text`, unit set `pcs` not `unit`), steps (`text`, `timer_seconds`), `oven_stages` new columns+CHECKs, `recipe_images.kind/bytes/UNIQUE(recipe_id,position)`, `labels.name_he/sort_order/created_by/UNIQUE(type,slug)`, `recipe_labels.source` values `user/assistant/llm`, cook logs `note`+`image_id`, tables `recipe_favourites`, `recipe_views` missing, FTS table name `recipe_fts` + `remove_diacritics 1`, partial indexes, import_key unique index. 0002 must seed the 12 emoji categories with Hebrew names and NO starter tags. | `internal/db/migrations/*.sql` (rewrite in place per 30 §1 + decision 4.12) |
| G7 | 30 §1, 20 §14, 60 §1 | Custom hand-rolled migrator; spec requires `pressly/goose/v3` (embedded, `goose_db_version`), refuse-to-start (exit 3) when DB newer than binary, `migrate status/up` only. Missing deps: goose, go-oidc, goexif. | `internal/db/migrate.go`, `internal/db/db.go`, `go.mod` |
| G8 | 60 §2.5-7, 20 §2 | No transaction helper `store.WithTx`, no post-commit file deletion queue, no Clock, handlers call store directly in places, services have wrong signatures (no `actor` first, no version, no `ImportToInbox`, `EditAndAcceptInbox`, `CopyRecipe`, `SetVisibility`, `SetFavourite`, `ReorderImages`, ...). Errors not typed (`ErrConflict`, `ErrDuplicate{ExistingID}`, `*ValidationError`, `ErrTooLarge`, `ErrUnsupportedMedia`). | `internal/service/*`, `internal/store/*`, `internal/domain/errors.go` (new) |
| G9 | 20 §7, 60 §1 | authz lives in `internal/auth/authz.go`, wrong rules: missing `CanChangeVisibility` (owner-only, ownerless never), `CanCopy`, `CanFavourite`, `CanLogCook`, cook-log-author delete, inbox rules; "archived" status. Must be package `internal/authz` with pure predicates + table tests. | `internal/auth/authz.go` -> `internal/authz/` |
| G10 | 40 §1, 60 §2.1-2 | Routing is catch-all with `strings.HasSuffix` and `r.Method` dispatch; no method patterns; no named route table / `url` template func; web+api split via a root dispatcher. | `internal/web/router.go`, `internal/api/router.go`, `cmd/recipes/main.go` |
| G11 | 60 §2.4, 70 §4 | Page data are `map[string]any`; no typed view models, no `Layout`, no permission flags. | `internal/web/*` |

### P0 security

| # | Spec | Wrong / missing | Files |
|---|---|---|---|
| S1 | 20 §8 | CSP wrong: has `'unsafe-inline'`, cdn.jsdelivr, fonts.googleapis. Must be exactly the 20 §8 string (incl. `img-src 'self' data: blob:`, `worker-src`, `manifest-src`, `object-src 'none'`, `base-uri 'none'`, `form-action 'self' <issuer origin>`). Also Permissions-Policy, COOP, Referrer-Policy `same-origin`, HSTS value. | `internal/httpx/middleware.go` |
| S2 | 20 §9, 40 §2.2 | `/media/` has NO authorization (anyone with the key reads any image), serves `public` cache, serves ServeFile on unvalidated key, no placeholder, accepts session or bearer? none. Must look up by key, `CanView`, `private, max-age=31536000, immutable`, ETag, placeholder, bearer `recipes:read` or session. | `internal/web/media.go` |
| S3 | 20 §5.2 | OIDC ID token is never signature-verified (hand-rolled; no JWKS). Must use `go-oidc/v3` verifier (iss/aud/exp/sig), nonce, lazy discovery with retry + friendly 503 page, group gate -> 403 "not allowed", `access_denied` handling, `locale` claim, admin group refresh, end-session redirect with `id_token_hint`. | `internal/auth/oidc.go` |
| S4 | 20 §5.5 | `/auth/dev-login` bypass exists. Remove. | `internal/web/handlers_dev.go`, router |
| S5 | 20 §4, 40 §1 | Middleware order/contents wrong: global rate-limiter (spec: per-token bucket, burst 20, in the bearer path), `CrossOriginProtection` custom (spec: stdlib `http.CrossOriginProtection`, non-`/api/`, trust `APP_BASE_URL` origin), no Auth/Locale in the chain, no unauthenticated handling rules (303 `/signin?return_to`, htmx/fetch 401 + `HX-Redirect`, POST 303), `return_to` validation (`/`, not `//`, `/\`). Timeouts 120 s. Shutdown 8 s + checkpoint. Startup must not need IdP. | `internal/httpx/*`, `internal/auth/session.go`, `cmd/recipes/main.go` |
| S6 | 20 §5.3, 6 | Cookie `__Host-nwr_session` (https) / `nwr_session` (http), last_seen update >10 min, expires=min(now+idle, created+max); token scopes are space-separated (code splits on comma), token cap 10 active, format `nwr_`+base62 43 chars, `last_used_at` <=1/min. | `internal/auth/*`, `internal/service/tokens.go`, `internal/store/api_tokens.go` |
| S7 | 20 §8 | Image types: GIF is decoded today (must be 415), HEIC message text, pixel cap `IMAGE_MAX_PIXELS` via DecodeConfig first, WebP RIFF check, `IMAGE_WORKERS` semaphore with 60 s wait -> 503, EXIF orientation via goexif, alpha flatten to white, output `full` <=1600 q82, `thumb` <=480 q78, layout `media/{key[0:2]}/{key}/{full|thumb}.jpg` (current `VariantPath` check), tmp+fsync+rename, post-commit deletion, nightly sweeper. SSRF: https only unless `IMAGE_FETCH_ALLOW_HTTP`, mapped/NAT64, <=3 redirects re-checked, `Dialer.Control`. Keep and verify what exists. | `internal/images/*` |
| S8 | 20 §8 | Input limits (title 200 ... tag 40, cook-log note 2000), 1 MB form limit, `API_MAX_BODY_MB`, `MaxBytesReader` everywhere, `Cache-Control` on HTML. Secrets never logged; config dump redacts; query strings of `/auth/*` not logged. | service validation, httpx |

### P1 features / routes

| # | Spec | Missing / wrong | Files |
|---|---|---|---|
| F1 | 40 §2.1 | Missing routes: `/signin`, `/auth/logged-out` (currently redirects to login), `/manifest.webmanifest`, `/sw.js`, `/icon-192.png`, `/icon-512.png`, `/static/{version}/{path...}` (static is unversioned `/static/`), `/readyz` JSON shape, `/healthz` text `ok`; `POST /auth/logout` must be POST + session-checked. | `internal/web`, `internal/api/health.go` |
| F2 | 40 §2.2 | Missing: `GET /` home (currently redirects to /recipes), `POST /recipes/{id}/visibility`, `/copy`, `/favourite`, `/cook-logs` (path is `/cook-log`), `/cook-logs/{logID}/delete`, `/images`, `/images/{id}/delete`, `/images/order`, `GET /labels/suggest`, view throttling (10 min upsert), version conflict 409, 303 flash flows, JSON-submit contract (200 `{id,url,version}` / 422 form HTML). | `internal/web/handlers_recipes.go` + new files |
| F3 | 40 §2.3 | Search params wrong/missing: `scope=all|mine|family` (code: `shared`), `category` repeatable OR, `tag` repeatable AND (code: comma-split), `mode` repeatable, `smoked`, `max_time`, `fav`, `sort` (relevance/updated/title/cooked/rating/recent), keyset `cursor`, page 24 (code: limit 50/offset), htmx fragment. | `internal/store/recipes.go`, `internal/service/search.go`, `internal/domain` |
| F4 | 20 §11 | Search normalization missing (NFKC, lowercase, niqqud, Hebrew final letters, whitespace), query building (>=3 rune phrases, 1-2 rune LIKE), bm25 weights, FTS columns `notes` incl. cook-log notes, index maintenance on accept/cook-log/category rename, inbox excluded, `recipes reindex`. Visibility filter in SQL. | `internal/service/search.go`, `internal/store/fts.go` |
| F5 | 40 §2.4 | Inbox: missing `GET /inbox/{id}`, `/inbox/{id}/edit` (GET/POST: edit and accept in one tx), accept with `share=1`, redirect to next oldest item or flash, reject = hard delete, badge count in Layout. Accept uses `FTS` indexing and `InboxItemAccepted`. | `internal/web/handlers_inbox.go`, service |
| F6 | 40 §2.5-2.6 | Recipe form contract: `stage[N].field` parsing, `ingredients` textarea parser (sections `#`, amount regex incl. fractions/unicode/Hebrew units, `raw_text`), `steps` textarea with `[mm:ss]`/`[h:mm:ss]`, `category` repeated, `tags` comma list, `version`, `language`, create-only `visibility`. Currently per-row inputs and wrong names. | `internal/web/forms.go` (new), `internal/service/ingredients.go` (parser) |
| F7 | 40 §2.7 | Settings page `GET /settings` missing; tokens page needs create flow showing token once, `expires_in_days`, revoke; admin categories CRUD (4 routes) missing; `admin` guard. | `internal/web/handlers_settings.go` + new |
| F8 | 40 §3-6 | JSON API: only `POST /api/v1/inbox`, `GET /api/v1/recipes` (partial) exist. Missing `GET /api/v1/inbox`, `DELETE /api/v1/inbox/{id}`, `GET /api/v1/recipes/{id}`, filters `source_url/scope/category/cursor/limit`, `Idempotency-Key` replay 200, duplicate `source_url` 409 + `existing_id` + `?allow_duplicate=1`, `RecipeImport` schema (structured vs `text` ingredients, `timer_seconds`, `data_base64`, per-image warnings, unknown category warning), `ImportResult`, RFC 9457 `problem+json` with `type` URIs, `request_id`, `invalid_params[{name,code,params,reason}]`, `WWW-Authenticate: Bearer`, 413/415/429 `Retry-After`, DTOs `RecipeSummary`/`Recipe` with absolute URLs from `APP_BASE_URL`, derived `preheat`/`frame_level`. | `internal/api/*`, `internal/service/import.go` |
| F9 | 20 §7, 30 §3 | Lifecycle rules: unshare deletes others' favourites/views; user-deletion procedure; copy duplicates image files; cook-log delete deletes photo; recipe delete queues file deletion after commit; ownerless shared stays shared. | service/store |
| F10 | 30 §4/6, decisions 1.3, 4.10 | Favourites, recently cooked/viewed (union), cook count/avg rating/last cooked from visible logs, cook-log photo (`kind='cook_log'`), "Last changed by". Recipe `rating` column and "archived" removed. | store/service |
| F11 | 20 §10 | i18n: key coverage, he parity, plural forms (CLDR one/two/many/other), helpers `T fmtDuration fmtTemp fmtQty fmtDate fmtAgo`, language resolution order (user, cookie, Accept-Language matcher, default), `POST /settings/language` public + `return_to`, tz via `TZ`, `i18ncheck` tool (`internal/i18n/cmd/i18ncheck`), Hebrew oven labels from 25 §3 (with English in parentheses), validation messages by Code+Params. Existing keys are for the old model. | `internal/i18n/*`, `locales/*` |
| F12 | 20 §15-16 | Events after commit (5 types), `recipes.categorized_at` marker, Categorizer interface shape (`Suggest(ctx, snapshot, knownCats, knownTags)`), backup (`VACUUM INTO`, integrity_check, `.media.txt`, prune, `BACKUP_AT`), jobs package (hourly purge sessions/login states/orphan tags; nightly sweeper; daily backup) all missing. | `internal/events`, `internal/categorize`, `internal/jobs` (new), `cmd` |
| F13 | 20 §17 | CLI: missing `user list|revoke-sessions|delete [--yes]`, `backup --out`, `migrate status/up` only (remove `down-to`), `healthcheck` needs only `APP_LISTEN_ADDR`, `version` via `main.version/commit/date`. | `cmd/recipes/main.go` (split per command) |
| F14 | 20 §12 | Config: wrong names (`APP_TRUST_PROXY`, `RECIPES_*` aliases), missing `APP_TRUSTED_PROXIES` (CIDR), `BACKUP_AT`, `IMAGE_MAX_PIXELS`, `IMAGE_WORKERS`, `TZ`, exit code 2 on invalid, unknown `APP_*`/`OIDC_*` warnings, `_FILE` both-set error, `OIDC_SCOPES` default `openid email profile`, defaults table. | `internal/config/config.go` |
| F15 | 20 §13 | Logging fields, access-log skips, `/readyz` JSON `{db,media,oidc}`; request-id ULID; HostCheck exempts health; client IP from right-most untrusted XFF. | `internal/httpx/*` |

### P2 deployment

| # | Spec | Wrong / missing | Files |
|---|---|---|---|
| D1 | 50 §2 | Dockerfile: Go 1.24 -> `GO_VERSION=1.25`, `--platform=$BUILDPLATFORM`, multi-arch args, ldflags use `main.version/commit/date` (current `main.Version/GitCommit/BuildTime`), `/data` skeleton with `media backups tmp` owned 65532, `ENV TMPDIR=/data/tmp APP_LISTEN_ADDR APP_DATA_DIR`, OCI labels, `.dockerignore`, no root-owned tzdata copy needed (distroless has it). | `Dockerfile`, `.dockerignore` (new) |
| D2 | 50 §4 | Layout must be `deploy/dev/{docker-compose.yml,.env.example,authentik/blueprints/recipes-dev.yaml,mock-oidc/config.json}`, `deploy/cosmos/{cosmos-compose.json,README.md}`. Current files are in wrong places/shape. Use spec blocks 50 §4.4-4.6, §5.3 verbatim as starting point. | `deploy/**`, delete root `docker-compose.yml` |
| D3 | 20 §18, 50 §9 | Taskfile has none of the canonical targets except a few: need `default dev:init dev:up dev:up:mock dev:run dev:logs dev:down dev:reset build(test ldflags) test (CGO_ENABLED=1 -race) test:e2e test:cover lint fmt i18n:check csp:check migrate:new vendor:htmx docker:build docker:release backup ci`. Add `test:ui`. | `Taskfile.yml` |
| D4 | 60 §1 | `.golangci.yml` missing; `htmx.min.js` pinned version + SHA-256 in Taskfile (vendored file exists, 51 KB; record version/hash). | repo root |

### UI gaps (package B)

| # | Spec | Wrong / missing | Files |
|---|---|---|---|
| U1 | 20 §8, 70 §1-2 | CSP-hostile templates: dozens of inline `onclick/onchange/...`, hundreds of `style=` (134 in `recipe_edit.html` alone), inline `<script>` in layout/cook_mode/recipe_edit, `/static/` hard-coded hrefs (39 `href="/"`/`action="/"` literals). All must become `data-action` delegation, CSS classes, `<script type=application/json>` blocks, and `url`/`asset` funcs. | all `web/templates/**` |
| U2 | 70 §2 | JS architecture: only `app.js` (487 lines, monolith, row-based editor), `timer.js`, `wakelock.js`. Need `app.js`, `editor.js`, `upload.js`, `cook.js`, `theme-init.js` (sync in `<head>`), `sw.js`. Cook mode, pre-alert, localStorage timers (`nwr_timers`), wake lock, swipe/RTL, `#step-N` hash, ingredient tick-off (sessionStorage), servings scaling, drafts (`nwr_draft:*`), photo resize/upload queue, JSON submit with 422/409/401/offline handling, `beforeunload`. | `web/static/js/*` |
| U3 | 70 §3,7 | CSS: breakpoints (600/1024), bottom nav (phone) / side rail (tablet landscape) / top bar (desktop), 48/56/72 px targets, light/dark tokens with `data-theme`, logical properties only, `.icon-dir` flip, `<bdi>` handling, contrast >=7:1, `prefers-reduced-motion` alert pulse. | `web/static/css/app.css` (1405 lines; audit for physical properties, rewrite as needed) |
| U4 | 70 §4 | Screens: Home (tiles, Smoked tile, recent, favourites, FAB), List/search (htmx, chips, sort, show-more, empty state), Recipe page (10-part order, share switch, copy, share link, delete in menu), Cook mode, Editor (7+2 sections, stage cards per mode, chips, collapsed "More"), Delete dialog, Inbox list/item (Keep / Edit,then keep / Throw away), Settings, Tokens, Sign-in, errors, Admin categories. Existing 8 templates are for old flows. | `web/templates/**` |
| U5 | 70 §8 | PWA: manifest at `/manifest.webmanifest` with PNG icons `icon-192.png` `icon-512.png` (only SVG exist), `sw.js` strategy, offline page, SW cache cleanup on sign-out. | `web/static/**` |
| U6 | 70 §9 | Forbidden words in UI copy (token, label, accept/reject, OIDC ...); wording table from `10 §4`. | templates + `ui.*.toml` |
| U7 | 70 §4.4 | Gourmet Roast display, 260 C oil hint, smoker ignition hint, preheat hint, frame level for Broil. | `oven_hero` partial, cook mode |

### Tests gaps (60 §6)
Missing: route-table test, rendered-link crawl, permission-flag matrix, story-flow tests, CSP/header test, `authz` table tests, oven table tests (every cell), ingredient parser tests, search normalization tests, store/service tests for lifecycle rules, e2e through real OIDC, UI browser tests. Existing tests assert the old model and will be replaced.

---------------------------------------------------------------------------------------------------

## 2. Work packages and ownership (no overlap)

### Package A: Go backend (agent A)
Owns exclusively:
- `cmd/**`, `internal/**` (including `internal/web/**` Go handlers, `views.go`, `funcs.go`, `routes.go`, renderer, `internal/web/fixtures/**`; `internal/api/**`; migrations `internal/db/migrations/*.sql`)
- `locales/**` (`en.toml`, `he.toml`, plus UI namespace files, see D14; and `locales/embed.go`)
- `web/embed.go`, `web/embed_test.go` (Go files in `web/` only)
- `go.mod`, `go.sum`, `.gitignore`, `.dockerignore`, `.golangci.yml`
- `Dockerfile`, `deploy/**`, `Taskfile.yml`, root `docker-compose.yml` (delete)
- `test/*.go`, `test/harness/**`, `test/uiharness/**` (Go e2e + the seeded-server harness)

### Package B: UI (agent B)
Owns exclusively:
- `web/templates/**` (layout, pages, partials)
- `web/static/**` (css, js, icons incl. PNGs, `manifest.webmanifest`, `sw.js`, `offline.html`, `vendor/htmx.min.js` content is produced by `task vendor:htmx`; B does not edit it)
- `test/ui/**` (Playwright e2e, `package.json`, config)
- `locales/ui.en.toml`, `locales/ui.he.toml` (UI-copy namespaces only, see D14). B is the only writer of these two files; A only reads them (loader + i18ncheck).

Neither package edits the other's files. Needed changes to the other side's files go through the contract (section 4); if the contract is insufficient, the discoverer appends a request under "Change requests" at the end of this file and continues with a workaround.

### Order and dependencies

```
Phase 0  (A, blocking for B; target: first, small)
  A0.1  internal/domain types final, internal/web/views.go (sec 4.3), funcs.go (4.2), routes.go route-name table (4.1),
        renderer with template-set layout (4.4), i18n loader that reads en/he + ui.en/ui.he (D14)
  A0.2  internal/web/fixtures + `go run ./test/uiharness -fixtures` : serves every page/fragment with canned view models
        for EN and HE, any session, no DB. (Lets B build and screenshot UI with zero backend.)
  A0.3  Final oven spec JSON (4.6) emitted by internal/oven (can be stubbed with the table values from 25 §4)
Phase 1  (parallel)
  A: migrations -> domain/oven -> store -> authz -> service -> web handlers -> api -> auth/oidc -> images -> jobs/cli/config -> deploy/Taskfile -> tests
  B: layout/css -> partials -> pages -> app.js -> editor.js/upload.js -> cook.js -> sw.js/PWA -> test/ui
     (develops against the fixture server; templates only reference fields in sec 4.3)
Phase 2  (integration, A then B, sequential handoffs)
  A: real server harness `go run ./test/uiharness` (DB-seeded users, fake OIDC) ; run Go crawl/flag tests against B's templates
  B: run test/ui against the real harness; report mismatches as Change requests
  A+B: i18ncheck green (union of catalogs), csp:check green, `task ci` + `task test:ui` green
Phase 3  A: merge ui.*.toml into en/he.toml if owner wants spec-exact filenames (D14), final docs touch-ups (none to specs).
```
Hard rule: A must not change a name in sections 4.1-4.8 after Phase 0 without a Change request that B acknowledges.

---------------------------------------------------------------------------------------------------

## 3. Decisions on ambiguities (spec wins; inconsistencies resolved here)

- D1 Index base: spec 25 shows `oven_stages[1]` and 40 shows `oven_stages[0]`; form says `stage[N]` N=0..9. Decision: 0-based everywhere (`oven_stages[0].temperature_c`, `stage[0].temperature_c`). Service prefix uses 0-based index.
- D2 Pizza non-custom with a temperature supplied: table says "must be null" -> error `forbidden` (field `temperature_c`). `keep_warm` temperature likewise `forbidden`. UI hides/doesn't submit them. Gourmet Roast `duration_seconds`/`duration2_seconds` required; `keep_warm` duration optional (0..43200, null = until switched off); smoker `preheat_minutes` is `forbidden` (Preheat "no").
- D3 Smoker `woodfire=false` silently normalized to true; `woodfire=true` for `broil`/`keep_warm` is `forbidden`.
- D4 Hard limits are only: temperatures 40-370 inclusive (each phase), durations per table, `preheat_minutes` 0-120, `target_core_temp_c` 30-100, strings per limits. DB CHECKs use the coarse 20-400 for temperatures and `>=0` for durations (30 §4 verbatim); JSON schema carries no min/max.
- D5 Warnings: `out_of_range` against the Recommended band, key `field` = `temperature_c|temperature2_c|duration_seconds|duration2_seconds`, Params `{min,max,mode,unit}` (`unit` = `c` or `s`; min/max in the field's unit). Pizza non-custom has no temperature warning. Warnings never block; API returns `warnings[]` (same shape as `invalid_params` plus `reason` English) in `ImportResult` and in 200 JSON of editor submit as `warnings` (extra key, optional for client).
- D6 Spec `LogCook(ctx, actor, id, note, rating)` (60 §2.5) lacks the photo of 40 §2.2/decision 4.10. Decision: `LogCook(ctx, actor, id string, in CookLogInput{Note string; Rating *int; Photo io.Reader}) (photoErr error, err error)`; photo failure never fails the log.
- D7 Package for authz: `internal/authz` (20 §3), not `internal/auth`.
- D8 Version injection: `-X main.version/commit/date` (20 §18, 50 §2.2); 50 §2.1 mention of `internal/version` is superseded. `cmd/recipes` exposes them to `web` via `config`/constructor arg.
- D9 Static URLs: `/static/{version}/...` where version = `main.commit` short SHA or `dev`; `asset "js/app.js"` builds it. Unknown version segment is accepted (any value) and serves the same file (so cached SW pages keep working after upgrades).
- D10 Offline page: spec gives only a sentence. Decision: static bilingual file `web/static/offline.html` (EN + HE text both visible), precached by the SW; no new route.
- D11 `sw.js` is `web/static/sw.js` containing tokens `__ASSET_VERSION__` and `__PRECACHE__` (JSON array literal) that Go replaces when serving `GET /sw.js` (precache list = every embedded file under `static/` plus the offline page). Served `Cache-Control: no-cache`, `Content-Type: text/javascript`.
- D12 Flash: cookie `nwr_flash` (HttpOnly, SameSite=Lax, Path=/, 60 s) carrying base64url JSON `{kind: "success"|"error"|"warning", key: "<i18n flash.* key>", params: {...}}`. Localized when rendered; templates only see `Layout.Flash{Kind,Text}`.
- D13 Dev/test login: none in the binary. Go and browser tests use `test/harness`: in-process app + an in-test fake OIDC IdP (stdlib RSA/JWT) and sessions inserted directly into the DB (60 §6.3). Browser tests receive a session cookie from the harness JSON. Docker `mock-oauth2-server` stays for `task test:e2e` (60 §6.4) but is not required for `test:ui`.
- D14 i18n files: spec says `locales/en.toml`, `he.toml`. To allow two agents without sharing a file, UI copy lives in `locales/ui.en.toml`/`ui.he.toml` (B), everything else in `en.toml`/`he.toml` (A). The loader loads all four per language. `i18ncheck` validates the union. At the end A may concatenate into the two spec files (mechanical).
- D15 Namespaces. A owns: `app.*`, `err.*` (HTTP/problem titles), `issue.*` (validation messages), `oven.*` (labels from 25 §3), `cat.*` (none; categories use DB names), `flash.*`, `api.*`, `mail.*` (none), `time.*` (units for fmtAgo/fmtDuration), `unit.*`. B owns: `nav.* common.* home.* list.* search.* recipe.* cook.* editor.* inbox.* settings.* tokens.* admin.* signin.* error.* offline.* js.* help.*`. Templates may use A's `oven.*` and `time.*`, `unit.*` keys and nothing else of A's. Rule: client JS gets (by Go, in `#client-i18n`) every key with prefix `js.` and `oven.mode.`; `js.*` values use `{name}` placeholders substituted by JS (not go-i18n templates).
- D16 Error pages: all errors render `error.html` with `ErrorView.Code` in {`not_found, forbidden, bad_request, conflict, too_large, unsupported_media, internal, unavailable, not_allowed, signin_failed, signin_expired, busy`}. B owns text keys `error.<code>.title`, `error.<code>.body`. A never emits free text; `renderError` maps domain errors to Code+status.
- D17 JSON in templates: Go passes Go values (not pre-encoded strings); templates write `<script type="application/json" id="x">{{.Value}}</script>`; html/template emits valid JSON in that context. Never `template.JS`.
- D18 Dynamic styling under CSP: no `style=` attributes. Use classes and `data-*`; JS may set CSSOM properties (`el.style.setProperty`) which CSP allows. Progress bars: JS sets `--p`.
- D19 Cook-log submit has two clients (htmx on recipe page, plain POST from cook-mode dialog): same route. htmx -> `cooklog_section` fragment, otherwise `303 /recipes/{id}#cook-logs`. Browser resizes the photo by hooking `htmx:configRequest` (replace the `photo` param) or, for the plain form, `submit` interception.
- D20 Server errors for fetch/htmx are classified by `Accept: application/json` or `HX-Request: true` (40 §1).
- D21 Tag-suggest JSON `name` for categories is the name in the UI language; tags verbatim.
- D22 Inbox item Keep/Edit-then-keep: "Share with family too" is a checkbox `share=1` inside the accept form. `inbox.update` (edit+accept) also reads `visibility` hidden/checkbox `share=1`.
- D23 ID format in templates: DOM ids for form fields via func `fieldID "stage[0].temperature_c"` => `stage-0-temperature_c`; top-level `title` => `f-title`. Error summary anchors use the same ids.
- D24 Dates: Go formats via `fmtDate`/`fmtAgo`; machine values in `datetime=` attributes use RFC 3339.
- D25 Duration input: UI shows h/m/(s) inputs *without `name`*; `editor.js` writes the total into a hidden `stage[N].duration_seconds` (and `duration2_seconds`). The server parses only `*_seconds`, and re-renders display parts via funcs `secH secM secS`.
- D26 Fixture server (`-fixtures`) is dev tooling only, in `test/uiharness`, never compiled into `cmd/recipes`.
- D27 `go.mod`: keep `go 1.25.0`, add `toolchain` line; add `goose/v3`, `go-oidc/v3`, `goexif`, `x/net` (tests only); drop `go-humanize` etc. if unused (tidy).

---------------------------------------------------------------------------------------------------

## 4. Backend <-> UI contract (fixed in Phase 0)

### 4.1 Route names (Go table in `internal/web/routes.go`; templates call `{{url "name" args...}}`)

`url` substitutes `{param}`s in order. `urlq "name" args... "k" "v" ...` is not needed; use `urlq` only as `{{urlq "recipes.list" "category" .Slug}}` (no path params) = path + `?k=v`.

| name | method path | name | method path |
|---|---|---|---|
| healthz / readyz | GET /healthz /readyz | static | GET /static/{version}/{path...} |
| manifest | GET /manifest.webmanifest | sw | GET /sw.js |
| icon192 / icon512 | GET /icon-192.png /icon-512.png | signin | GET /signin |
| auth.login | GET /auth/login | auth.callback | GET /auth/callback |
| auth.logout | POST /auth/logout | auth.loggedout | GET /auth/logged-out |
| lang | POST /settings/language | home | GET / |
| recipes.list | GET /recipes | recipe.new | GET /recipes/new |
| recipe.create | POST /recipes | recipe.view | GET /recipes/{id} |
| recipe.cook | GET /recipes/{id}/cook | recipe.edit | GET /recipes/{id}/edit |
| recipe.update | POST /recipes/{id}/edit | recipe.delete | POST /recipes/{id}/delete |
| recipe.visibility | POST /recipes/{id}/visibility | recipe.copy | POST /recipes/{id}/copy |
| recipe.favourite | POST /recipes/{id}/favourite | cooklog.add | POST /recipes/{id}/cook-logs |
| cooklog.delete | POST /recipes/{id}/cook-logs/{logID}/delete | image.add | POST /recipes/{id}/images |
| image.delete | POST /recipes/{id}/images/{imageID}/delete | image.order | POST /recipes/{id}/images/order |
| media | GET /media/{key}/{variant} | labels.suggest | GET /labels/suggest |
| inbox.list | GET /inbox | inbox.view | GET /inbox/{id} |
| inbox.edit | GET /inbox/{id}/edit | inbox.update | POST /inbox/{id}/edit |
| inbox.accept | POST /inbox/{id}/accept | inbox.reject | POST /inbox/{id}/reject |
| settings | GET /settings | tokens.list | GET /settings/tokens |
| tokens.create | POST /settings/tokens | tokens.revoke | POST /settings/tokens/{id}/revoke |
| admin.categories | GET+POST /admin/categories | admin.category.update | POST /admin/categories/{id}/edit |
| admin.category.delete | POST /admin/categories/{id}/delete | | |

Also `/api/v1/*` routes (40 §3) in `internal/api/routes.go`, not exposed to templates.

### 4.2 Template funcs (Go, `internal/web/funcs.go`; B may rely on exactly these)

| func | signature / result |
|---|---|
| `T` | `T "key" ["Name" val ...]` -> string (go-i18n, request localizer). Missing key renders key and logs |
| `url`, `urlq`, `asset` | `asset "css/app.css"` -> `/static/<ver>/css/app.css` |
| `fmtDuration` | seconds -> `m:ss` (<1 h) / `h:mm` (>=1 h); `fmtDurationWords` -> "1 h 30 min" localized |
| `fmtTemp` | int -> "220 °C"; templates wrap in `<bdi>` themselves |
| `fmtQty` / `fmtUnit` | `fmtQty amount unit` -> number text after g->kg, ml->l conversion at >=1000; decimals (<=2) for g/kg/ml/l, fractions for tsp/tbsp/pcs/pinch; `fmtUnit amount unit` -> converted unit key (see contract-questions B3) |
| `deref` | `*int/*int64/*float64/*string` -> value (nil -> zero); also `add sub list` |
| `fmtDate`, `fmtAgo`, `fmtDateTime` | time.Time -> localized string in `TZ` |
| `fieldID` | form field name -> DOM id (D23) |
| `secH secM secS` | seconds -> int parts |
| `dirFor` | recipe language `en|he|und` -> `ltr|rtl|auto` ; `langFor` -> `en|he|""` |
| `seq` | `seq 0 9` []int |
| `dict` | `dict "k" v ...` |
| `join`, `contains`, `hasKey` | `contains []string string` bool; `hasKey map string` bool |
| `host` | URL string -> host for "Found on" |
| `stars` | int rating -> []bool of length 5 |
| `pct` | not provided (use JS) |

### 4.3 View models (Go, `internal/web/views.go`; exact field names)

```go
type Layout struct {
    Lang, Dir        string // "en"|"he", "ltr"|"rtl"
    ClientI18n       map[string]string // -> #client-i18n (js.* and oven.mode.*)
    Title            string // localized page title ("" = app name only)
    User             *UserView // nil when signed out
    InboxCount       int       // pending inbox of actor
    Flash            *FlashView
    CSRFOrigin       string // APP_BASE_URL origin; informational (spec 60 §2.4), forms need no token
    AssetVersion     string
    ActiveNav        string // "home"|"search"|"add"|"inbox"|"menu"|""
    Path             string // current path+query, used as return_to by the language form
    OtherLang        string // "he" when Lang=en
    OtherLangNative  string // "עברית" / "English"
    DevMode          bool
}
type UserView  struct{ ID, DisplayName string; IsAdmin bool }
type FlashView struct{ Kind, Text string }

type PermFlags struct {
    IsOwner, CanEdit, CanDelete, CanChangeVisibility, CanCopy, ShowCopy bool // ShowCopy = CanCopy && !IsOwner
    CanFavourite, CanLogCook, IsFavourite bool
}

type ImageView struct{ ID, ThumbURL, FullURL, Caption string; Width, Height, Position int; IsCover bool }
type IngredientView struct{ Section string; Amount *float64; Unit, Item, RawText string; AmountText, UnitKey string /*fmtQty, fmtUnit*/ }
type StepView struct{ Position int; Text string; TimerSeconds int /*0 = none*/ }
type CategoryRef struct{ Slug, Name, Icon string }          // Name already in UI language
type CategoryOption struct{ Slug, Name, Icon string; Selected bool }

type OvenStageView struct {                                  // display only (derived values included)
    Position int
    Mode, Preset, PelletBlend string                         // enum keys; "" when n/a
    PelletNote string
    TemperatureC, Temperature2C, DurationSeconds, Duration2Seconds, PreheatMinutes, TargetCoreTempC *int
    TotalSeconds int                                         // duration + duration2
    IsGourmet, Woodfire, Preheat bool
    FrameLevel string                                        // "bottom"|"top"
    ShowFrameProminent bool                                  // broil
    Accessories []string                                     // keys
    Notes string
    OilWarning bool                                          // any temp > 260
    SmokerIgnition bool                                      // mode = smoker
    PreAlert bool                                            // TotalSeconds >= 600
}

type RecipeBody struct {                                     // shared by recipe page, inbox item, cook mode
    ID, Title, Description, Notes string
    Language, Lang, Dir string                               // raw 'en|he|und', and langFor/dirFor results
    Servings *int; PrepMinutes *int
    SourceURL, SourceName, SourceHost string
    Visibility string                                        // "private"|"shared"
    Stages []OvenStageView
    Ingredients []IngredientView                             // flat, Section changes mark headers
    Steps []StepView
    Images []ImageView
    Categories []CategoryRef; Tags []string
    OwnerName string                                         // "" if ownerless
}
type RecipeCardView struct {
    ID, Title, Lang, Dir, ThumbURL string
    PrimaryMode string; PrimaryTempC *int; Visibility string; UsesWoodfire, IsFavourite bool
    TotalSeconds int; At *time.Time                           // At = recently cooked/viewed time (home row)
}
type CookSummary struct{ Count int; LastAt *time.Time; AvgRating *float64; RatingCount int }
type CookLogView struct {
    ID int64; AuthorName string; At time.Time; Rating *int; Note, PhotoThumbURL, PhotoFullURL string
    CanDelete bool                                           // author only
}
type LastChanged struct{ At time.Time; ByName string }       // nil unless updated_by differs per 30 §6
type VisibilityView struct{ RecipeID string; Shared, CanChange bool; OwnerName string }
type FavouriteView struct{ RecipeID string; IsFavourite bool }
type DeleteDialogView struct{ ID, Title, Action string; Shared bool }  // Action = url "recipe.delete"

// Pages
type HomeView struct{ Layout; Tiles []CategoryRef; Recent, Favourites []RecipeCardView }   // tiles: only categories with >=1 visible recipe
type RecipesListView struct {
    Layout
    Q, Scope, Sort string; Smoked, Fav bool; MaxTime int
    Categories []CategoryOption; Modes []ModeOption{Key string; Selected bool}; SelectedTags []string
    SortOptions []SortOption{Key string; Selected bool}
    Active []ActiveFilter{Label, RemoveURL string}; HasFilters bool
    Results ResultsView
}
type ResultsView struct{ Cards []RecipeCardView; NextURL string /*"" = no more*/; Q string; HasFilters bool }
type RecipeView struct {
    Layout; RecipeBody; PermFlags
    Visibility VisibilityView; FavouriteState FavouriteView
    Cook CookSummary; Logs CookLogSectionView
    LastChanged *LastChanged; ShareURL string /*absolute*/; Delete DeleteDialogView
    Editable bool; MaxNote int // 2000
}
type CookLogSectionView struct{ RecipeID string; Summary CookSummary; Logs []CookLogView; CanLogCook bool; PhotoFailed bool; MaxNote int }
type CookView struct{ Layout; RecipeBody; Config CookConfig }
type CookConfig struct {                                     // -> <script id="cook-config">
    RecipeID, UserID, Title string; Servings *int
    Stages []struct{ Position int; Label string; TotalSeconds, Stage1Seconds int; IsGourmet, PreAlert bool; Stage2Notice string }
}
type InboxListView struct{ Layout; Items []InboxCardView{ID, Title, ThumbURL, SourceHost, Lang, Dir string; ReceivedAt time.Time} }
type InboxItemView struct{ Layout; RecipeBody; MissingOven bool; Next *struct{ID string} }
type SettingsView struct{ Layout; Version, Commit, BuildDate string }
type TokensView struct {
    Layout; Tokens []TokenView{ID, Name, Prefix string; Scopes []string; CreatedAt time.Time; LastUsedAt, ExpiresAt *time.Time; Status string /*active|revoked|expired*/}
    NewToken string /*shown once, else ""*/; ScopeOptions []string{"inbox:write","recipes:read"}; ExpiryOptions []int{0,30,90,365}; FormErrors map[string]string
}
type AdminCategoriesView struct{ Layout; Items []AdminCategory{ID int64; Slug, Name, NameHe, Icon string; SortOrder, RecipeCount int}; FormErrors map[string]string }
type SigninView struct{ Layout; LoginURL string /*"/auth/login?return_to=..."*/ }
type LoggedOutView struct{ Layout }
type ErrorView struct{ Layout; Status int; Code string; RequestID string; BackURL string }
```

Editor:

```go
type EditorView struct {
    Layout
    Mode string                      // "new" | "edit" | "inbox"
    Action string                    // resolved form action path
    SubmitKey string                 // i18n key: editor.save | editor.save_and_keep
    Form RecipeForm
    AllCategories []CategoryOption   // Selected from Form
    Images []ImageView               // existing recipe photos (edit/inbox)
    Errors   map[string][]string     // key = form field name ("title","stage[0].temperature_c"), values localized
    Summary  []ErrorItem{FieldID, Message string}
    Warnings map[string][]string
    Config   EditorConfig            // -> <script id="editor-config">
    OvenSpec any                     // -> <script id="oven-spec"> (4.6)
    Spec     OvenSpecLabels          // option lists for selects: Modes[{Key,Label,Dial}], Presets, Blends, Accessories (labels localized)
    CanDelete bool; Delete DeleteDialogView
    StageTemplate StageForm          // blank stage rendered inside <template id="stage-template"> with Idx "__N__"
}
type RecipeForm struct {
    Version int; Title, Description, Notes, Language string; Servings, PrepMinutes, SourceURL, SourceName string // strings: preserve input
    CategorySlugs []string; Tags string /*comma-separated*/; Ingredients, Steps string /*textareas*/
    Visibility string; Stages []StageForm
}
type StageForm struct {
    Idx string                       // "0".."9" or "__N__"
    Mode, PizzaPreset, TemperatureC, DurationSeconds, Temperature2C, Duration2Seconds, PreheatMinutes string
    Woodfire bool; PelletBlend, PelletNote string; Accessories []string; TargetCoreTempC, Notes string
}
type EditorConfig struct {
    Mode, RecipeID, UserID, DraftKey string; Version int
    Action, SigninURL, SuggestURL, UploadURL, OrderURL, DeleteImageURLTemplate string // "" when n/a (new)
    MaxImages, MaxImageBytes, MaxStages int
}
```

Field-name contract of the recipe form (POST body, urlencoded or multipart): `version, title, description, notes, language, servings, prep_minutes, source_url, source_name, category (repeat), tags, ingredients, steps, visibility (create only: "private"|"shared"), stage[N].mode|pizza_preset|temperature_c|duration_seconds|temperature2_c|duration2_seconds|preheat_minutes|woodfire ("1" when on; absent = false)|pellet_blend|pellet_note|accessories (repeat)|target_core_temp_c|notes`. Stage indexes must be contiguous from 0 in DOM order; the server re-sorts by N. Hidden/disallowed fields must not be submitted (disabled inputs).

HTML ids/anchors required: `#recipe-form` (the `<form>`), `#error-summary`, `#results`, `#visibility-control`, `#fav-{recipeID}`, `#cook-logs`, `#delete-dialog`, `#reject-dialog`, `#draft-banner`, `#photo-queue`, `#stage-list`, `#stage-template`, `#flash`, `#client-i18n`, `#oven-spec`, `#editor-config`, `#cook-config`.

### 4.4 Template files and fragments

Page files in `web/templates/pages/` each define `content` (+ optional `head`, `scripts`); `layout.html` defines the shell (`<html lang dir>`, htmx-config meta, theme-init.js in head, nav, `#flash`, `#client-i18n`, `app.js`). Renderer = layout + all `partials/*.html` + that page.

| page file | view | route(s) |
|---|---|---|
| `home.html` | HomeView | home |
| `recipes_list.html` | RecipesListView (uses partial `results`) | recipes.list |
| `recipe_view.html` | RecipeView | recipe.view |
| `cook.html` | CookView | recipe.cook |
| `recipe_edit.html` | EditorView (includes partial `recipe_form`) | recipe.new/edit, inbox.edit |
| `inbox_list.html` / `inbox_item.html` | InboxListView / InboxItemView | inbox.list / inbox.view |
| `settings.html`, `tokens.html`, `admin_categories.html` | SettingsView, TokensView, AdminCategoriesView | |
| `signin.html`, `logged_out.html`, `error.html` | SigninView, LoggedOutView, ErrorView | signin, auth.loggedout, errors |

Partials (`partials/*.html`) / fragment names and the endpoints that return them directly:

| template name | data | returned by |
|---|---|---|
| `results` | ResultsView | GET /recipes with `HX-Request` (first page replaces `#results` innerHTML; with `cursor` replaces the show-more button via `hx-swap=outerHTML`) |
| `favourite_button` (`<button id="fav-{id}" hx-post ... hx-swap="outerHTML">`) | FavouriteView | POST favourite (htmx) |
| `visibility_control` (`#visibility-control`) | VisibilityView | POST visibility (htmx) |
| `cooklog_section` (`#cook-logs`) | CookLogSectionView | POST cook-logs (htmx) |
| `image_tile` | ImageView (+`RecipeID`) | POST images -> 201 + header `X-Image-Id` |
| `upload_error` (`<p class="upload-error" role="alert">`) | `struct{Message string}` | 413/415/422/503 on POST images |
| `recipe_form` (`<form id="recipe-form">...`) | EditorView | 422 on JSON/fetch submit |
| `conflict` (`<div id="conflict" role="alert">` + link) | `struct{RecipeURL string}` | 409 on fetch submit (full `error.html` Code=conflict when not fetch) |
| `recipe_card`, `oven_hero`, `stage_card` (editor), `delete_dialog`, `flash`, `language_button`, `category_chips`, `cooklog_entry`, `stars` | as above | includes only |

Editor submit protocol (JS `fetch`, header `Accept: application/json`): 200 `{"id","url","version","warnings":[...]?}`; 422 `text/html` = partial `recipe_form`; 409 `text/html` = partial `conflict`; 401 with `HX-Redirect`; 413 on huge body. Non-JS fall-backs per 40 §2.5.
Status handling for htmx: favourite/visibility/cook-log/list return 200 fragments; errors return full pages for non-htmx, and for htmx 4xx a minimal `error` fragment is not needed (htmx ignores non-2xx).

### 4.5 Cookies, storage, headers JS depends on
- Cookies set by server: session (`nwr_session`/`__Host-nwr_session`), `lang`, `nwr_flash`. JS never reads them (HttpOnly except `lang`).
- localStorage: `nwr_theme` (`light|dark|auto`), `nwr_timers`, `nwr_draft:<userId>:<recipeId|new|inbox:<id>>`. sessionStorage: `nwr_ticks:<recipeId>`.
- Server response headers JS reads: `X-Image-Id`, `HX-Redirect`.
- `Layout.User.ID` is exposed to JS via `#editor-config.UserID` / `#cook-config.UserID` and `<body data-user-id>` (for sign-out cleanup).
- Sign-out: `<form method="post" action="{{url "auth.logout"}}" data-action="sign-out">`; `app.js` clears `nwr_draft:<userId>:*` and posts `{type:"clear-cache"}` to the SW, then lets the form submit.

### 4.6 Oven spec JSON (`GET /api/v1/meta/oven` and `#oven-spec`), from `internal/oven/spec.go`
```json
{"limits":{"temp_min":40,"temp_max":370,"max_stages":10,"preheat_minutes_max":120,"core_temp_min":30,"core_temp_max":100,"pellet_note_max":60,"stage_notes_max":500,"oil_warning_c":260},
 "modes":[{"key":"max_roast","dial":"MAX ROAST","preheat":true,"frame_level":"bottom","woodfire":"allowed|forced|forbidden",
   "default_accessory":"pro_heat_pan",
   "temperature":{"kind":"required|forbidden|by_preset","accepted":[40,370],"recommended":[230,370]},
   "duration":{"kind":"required|optional","accepted":[60,14400],"recommended":[300,7200]},
   "temperature2":null,"duration2":null}],
 "pizza_presets":["artisan","thin_crust","new_york","chicago","calzone","custom"],
 "pellet_blends":["all_purpose","robust","other"],
 "accessories":["pizza_stone","pro_heat_pan","roast_rack","own_pan"]}
```
`gourmet_roast` fills `temperature2/duration2` (`kind:"required"`). `pizza` temperature kind is `by_preset` (required only for `custom`). Mode `keep_warm` temperature `forbidden`, duration `optional`. Values are exactly the 25 §4 table (no per-mode "Accepted" for temperature other than 40-370). Labels are never in JSON; the client uses `oven.mode.*` from `#client-i18n`.

### 4.7 i18n keys the cross-package parts depend on
- Backend-emitted (A, in `en.toml/he.toml`): `oven.mode.<8>`, `oven.preset.<6>`, `oven.pellet.<3>`, `oven.accessory.<4>`, `oven.frame.<bottom|top>` (values per 25 §3 incl. Hebrew with English in parentheses for modes/presets; `new_york`, `chicago`, `calzone` Hebrew = English per table), `issue.required|forbidden|invalid` (`{{.Field}}`), `issue.out_of_range.temperature|duration` (hard: "Between {{.Min}} and {{.Max}}..."), `issue.warn.temperature|duration` ("Unusual for {{.Mode}} ({{.Min}}-{{.Max}} °C)"), `issue.too_long`, `issue.too_many`, `issue.version_conflict`, `flash.*` (`recipe_saved, recipe_deleted, recipe_copied, kept, thrown_away, inbox_empty, token_created, token_revoked, category_saved, category_deleted, language_saved, photo_failed, signed_out`), `err.*` (problem titles), `unit.<g|kg|ml|l|tsp|tbsp|pcs|pinch>`, `time.*` (ago units with CLDR plurals), `app.name`, `app.version`.
- UI-emitted (B, in `ui.*.toml`): everything else including `js.*`; B must provide `js.` keys for: draft banner texts, offline/not-saved messages, HEIC message, photo failed/retry, timers (labels, pre-alert, stage-2 notice, stop), wake-lock hint, share/copy feedback, validation hints used live (`js.hint.recommended` "{min}-{max}"; `js.warn.unusual` "Unusual for {mode} ({min}-{max} {unit})"), servings scaler, tick-off. Wording per `10 §4`; forbidden words per `70 §9`.

### 4.8 JSON endpoints the UI JS calls
- `GET /labels/suggest?q=` -> `[{"type":"category|tag","slug","name"}]` (D21).
- `POST /recipes/{id}/images` (XHR, field `image`): 201 + `image_tile` HTML + `X-Image-Id`; `POST .../images/order` body `ids` x N -> 204 (400 on set mismatch); `POST .../images/{imageID}/delete` -> 204 for XHR (`Accept: application/json` or `X-Requested-With`), 303 otherwise.
- Editor submit (4.4). Everything else is plain forms/links/htmx.

### 4.9 Test hooks (stable selectors, `data-test` attribute; used by Go HTML tests via x/net/html and by Playwright)
`edit-link, delete-open (button in menu), delete-form, visibility-switch, visibility-badge, copy-form, favourite-button, cooklog-form, cooklog-delete-form, start-cooking, share-link, last-changed, stage-card, stage-mode, add-stage, remove-stage, save, error-summary, draft-banner, draft-continue, draft-discard, timer-tray, timer-start, timer-banner, step-next, step-back, ingredients-open, servings-plus, servings-minus, inbox-keep, inbox-edit, inbox-reject, inbox-badge, lang-switch, theme-toggle, signin-button, signout, token-new, results-card, show-more, search-input`.
Permission-flag matrix expectations are those in `70 §4.3`; they map to: `edit-link`=CanEdit, `delete-open`+`delete-form`=CanDelete, `visibility-switch`=CanChangeVisibility (else only `visibility-badge`), `copy-form`=ShowCopy, `favourite-button`=CanFavourite, `cooklog-form`=CanLogCook.

---------------------------------------------------------------------------------------------------

## 5. Package A task list (ordered)

1. Phase 0 items (section 2). Rename module-internal packages; move authz.
2. `go.mod`: add goose, go-oidc, oauth2 (present), goexif, x/net (test); `go mod tidy`; toolchain.
3. Migrations: rewrite `0001_initial_schema.sql` and `0002_seed_categories.sql` verbatim from 30 §4-5 (delete old 0002 starter tags). Replace `db/migrate.go` by goose (embedded FS, `goose.SetBaseFS`, `goose_db_version`); add `db.CheckVersion` (exit 3). Timestamp helpers `store.formatTime/parseTime` (ms Z).
4. `domain`: enums per 25 §3, `OvenStage` per 25 §2, `Recipe` (version, import_key, categorized_at, created_by, updated_by, no rating/difficulty), `RecipeSummary`, `CookLog` (+image), `Image` (kind), `Label` (name_he, sort_order), errors (`ErrNotFound, ErrForbidden, ErrConflict, ErrDuplicate{ExistingID}, *ValidationError{Errors,Warnings []oven.Issue}, ErrTooLarge, ErrUnsupportedMedia, ErrBusy`), `Clock`.
5. `oven`: `spec.go` data table (25 §4, one struct per mode), `validate.go` generic interpreter incl. `Issue`, `Normalize`, `Validate`; `derive.go` (stage time, total, uses woodfire, primary mode, frame level, preheat); JSON serializer for 4.6.
6. `authz` package + table tests.
7. `store`: `WithTx` + post-commit hooks; repositories per aggregate (users, sessions, oidc states, tokens, labels, recipes(+children replace-wholesale), images, cook logs, favourites, views, fts); keyset pagination; search query builder; reindex; user deletion procedure; stats queries (cook count/avg/last, recent union).
8. `images`: harden per S7; `Process`, `Fetch`, `Store/Remove`, semaphore, sweeper; placeholder asset (embed a small JPEG in `internal/images`).
9. `service`: all signatures from 60 §2.5 + D6; ingredient parser (`service/ingredients.go`, 40 §2.6), step `[mm:ss]` parser, tag normalization (NFKC etc.), import (`ImportToInbox`, idempotency, duplicate), inbox accept/edit+accept/reject returning next ID, copy (duplicate files), visibility side effects, cook-log with photo, tokens (cap 10, base62), admin categories (+reindex on rename), events post-commit.
10. `auth`: go-oidc client (lazy discovery with backoff), session manager (cookie names, sliding expiry, absolute max), bearer + scopes (space separated) + per-token bucket, `Auth` middleware with 40 §1 unauthenticated rules, locale middleware.
11. `httpx`: exact middleware chain + order (20 §4), CSP and headers exactly per 20 §8, `http.CrossOriginProtection` (trusted origin), HostCheck, request-id ULID, IP parser with `APP_TRUSTED_PROXIES`, access log rules.
12. `web`: `routes.go` (method patterns + route-name table), `views.go`, `funcs.go`, renderer (embedded or `APP_TEMPLATES_DIR` reload), `forms.go` (recipe form parsing/printing; errors back to form field names), handlers per file (home, recipes, editor, images, cooklogs, inbox, settings/tokens, admin, auth pages, media, static/pwa), `renderError`.
13. `api`: `routes.go`, handlers, DTOs, problem+json, import schema validation, `Idempotency-Key`, `meta/oven`.
14. `jobs` (hourly purge, nightly sweeper, daily backup), `config`, `cmd/recipes` subcommands incl. `user *`, `backup --out`, `reindex`, `migrate status/up`, graceful shutdown (8 s, checkpoint).
15. `internal/i18n`: loader for 4 files, resolution order, funcs, plural rules (he one/two/many/other), `cmd/i18ncheck`, `csp:check` implementation (Go program `internal/tools/cspcheck` or shell in Taskfile).
16. `locales/en.toml`,`he.toml` (A namespaces) per 4.7 and 25 §3; Hebrew per spec tables.
17. Deploy: `Dockerfile`, `.dockerignore`, `deploy/dev/**`, `deploy/cosmos/**`, `Taskfile.yml` (all 20 §18 targets + `test:ui`), `.golangci.yml`, remove obsolete compose files. `vendor:htmx` records version+SHA-256 (verify existing file hash first).
18. Tests (section 6). Delete old tests that assert removed behaviour.

## 6. Package B task list (ordered)

1. `layout.html`: skeleton, `<meta name="htmx-config" content='{"includeIndicatorStyles":false,"allowEval":false,"allowScriptTags":false}'>`, `theme-init.js` sync, nav (bottom bar / side rail / top bar), language button, flash, `#client-i18n` JSON, `<body data-user-id>`; no inline script/style/handlers; all links via `url`/`asset`.
2. `css/app.css`: tokens, light/dark (`:root[data-theme]` + `prefers-color-scheme`), logical properties only, breakpoints 600/1024, sizes (18/16 px base; 48/56/72 targets), RTL (`.icon-dir`), reduced motion, oven hero sizes, print-free. Add `grep` guard: no `margin-left|right`, `padding-left|right`, `left:|right:`, `text-align: left|right`, `float`.
3. Partials then pages in the order of 4.4, built against the fixture server in EN and HE.
4. `js/app.js` (delegation, dialogs `<dialog>`, menu, theme toggle, share/copy, flash dismiss, SW registration + clear-cache, sign-out cleanup, desktop `/` focus and `Ctrl+S`), `js/editor.js` (stages, mode-driven show/hide + `disabled` for non-submitted fields, spec hints/warnings, chips + suggest, drafts, JSON submit protocol 4.4, language auto-set to Hebrew, ingredient/step help), `js/upload.js` (createImageBitmap -> canvas 2048 -> JPEG .85, HEIC message, sequential XHR with progress, queue for new recipe, retry, reorder/cover/delete), `js/cook.js` (stepper, swipe/arrow/RTL, hash, wake lock, timers + tray + alerts + `nwr_timers`, Web Audio unlock on tap, vibrate, ingredients panel, scaling, tick-off, Done dialog).
5. `sw.js` with tokens `__ASSET_VERSION__`/`__PRECACHE__`, strategy per 70 §8; `offline.html`; `manifest.webmanifest` content per 70 §8; PNG icons 192/512 (render the SVG with headless Chrome: `google-chrome --headless --screenshot --window-size=512,512`).
6. `locales/ui.en.toml`, `ui.he.toml` with every key B uses (wording from `10 §4`); keep EN/HE parity; no forbidden words.
7. `test/ui/**` (section 6 test plan).
8. Self-checks before handoff: `task csp:check`, `task i18n:check`, Lighthouse-style checks not required.

---------------------------------------------------------------------------------------------------

## 7. Test plan

### 7.1 Unit (A), `CGO_ENABLED=1 go test -race ./...` via `task test`; coverage >=80 % for `oven authz service search ingredient parser`
- `oven`: table-driven, every cell of 25 §4 (required/forbidden, hard bounds inclusive lo/hi and just outside, soft bounds -> warning only), cross-field rules, smoker normalization, accessory sort/dedupe, derived values, JSON spec equals table (golden file).
- `authz`: every cell of 20 §7 for owner / other / admin / ownerless shared / inbox.
- `service` search normalization (final letters ("עוף" matches "עופות"), niqqud, NFKC, case), query builder (1-2 rune terms, quoting), ingredient parser (fractions, unicode, decimal comma, Hebrew units, sections, no-amount), step timer parser, tag normalization, import validation, `return_to` validator, token format/hash, SSRF classifier (mapped/NAT64), config parsing (`_FILE`, both-set error, CIDR, unknown var warning), i18n (key parity, plural he, `fmtDuration`, `fmtQty`, `fmtAgo`), httpx (IP parser, per-token bucket, headers: CSP equals expected string with no `unsafe-inline`).
- `db`: fresh migrate, goose table, newer-DB guard (exit 3 path), schema sanity (CHECKs for 25 fields, `status inbox => private`), seed has 12 categories and no tags.

### 7.2 Store/service (A): real SQLite in `t.TempDir()`
create/update/delete with children + FTS; version conflict; unshare side effects (others' favourites/views removed, logs kept); user deletion procedure; copy duplicates image files with new keys; idempotency key replay; duplicate `source_url` 409; image files removed after commit only; cook-log delete removes photo; search with every filter/sort/cursor; recent union; category rename reindexes; hourly purge of orphan tags.

### 7.3 HTTP integration (A, `internal/web` + `internal/api`, `httptest.Server`, sessions inserted in DB), 60 §6.3
1. Route-table test: every name in 4.1 resolves to the expected mux pattern; wrong methods -> 405.
2. Rendered-link crawl as owner / other / admin for every page; each `<a href>` and `<form action method>` must not be 404/405; forms submitted with their own fields assert intended state change (uses B's templates, hence a Phase 2 test).
3. Permission-flag matrix on `/recipes/{id}` via `data-test` hooks (4.9) for the 70 §4.3 table; 404 for other user's private recipe and for inbox of others.
4. Story flows: create -> view -> edit (title and stage persist) -> share -> second user edits -> owner sees "Last changed by" -> delete; API inbox import -> inbox item -> edit-and-keep -> in search; version conflict 409 with fetch (fragment) and non-fetch (page); expired session on POST -> 303 `/signin?return_to=`; htmx 401 + `HX-Redirect`; media authz (other user's private image 404, bearer `recipes:read` ok); image upload JPEG/PNG/WebP ok, HEIC/GIF 415, >MAX 413; JSON API matrix for 40 §3-6 (scopes 403, rate limit 429 + Retry-After, problem+json shape, idempotency, duplicates, allow_duplicate, warnings for bad image URL/unknown category/out_of_range).
5. Headers: CSP identical to 20 §8 on every HTML page (crawl), `X-Content-Type-Options` etc.; templates contain no inline script/handler (also `task csp:check`).
6. No dev-login: `/auth/dev-login` -> 404.

### 7.4 Go end-to-end (A), `test/` (`go test ./test/...`, no docker): `test/harness` = real `cmd/recipes` wiring in-process on 127.0.0.1:0, fake OIDC IdP (discovery, jwks RSA, authorize auto-approve for chosen user, token, end-session), real SQLite, real image pipeline.
Cases: real OIDC login (PKCE/state/nonce, group gate -> 403 page, admin group), session persists across harness restart (same DB), logout hits end-session, token create via HTML then API call with the bearer, upload a real JPEG then fetch `/media/..`, backup subcommand + integrity check, graceful shutdown. `task test:e2e` (60 §6.4) additionally runs the same suite against `dev:up:mock` (mock-oauth2-server in docker) when `E2E_BASE_URL` is set; skipped otherwise.

### 7.5 Browser/DOM e2e (B): Playwright driving the system Chrome
Tooling check: node 24.14 and `/usr/bin/google-chrome` 156 exist; Playwright browsers are not installed, so use `playwright-core` with `channel: "chrome"` (no download). Installed in `test/ui/package.json` only (devDependency, pinned to an exact version >=2 weeks old; this is test tooling, not a runtime/build dependency of the app, consistent with 60 §1 "no JS build tooling" for the product).
Runner: node's built-in `node --test` with `playwright-core` (no extra runner). Server: `go run ./test/uiharness` prints one JSON line `{baseURL, users:{alice,bob,admin}:{cookieName,cookieValue,id}, recipes:{...}, token}` then serves until killed; seeds recipes (private/shared/ownerless/inbox, a gourmet roast, smoker+max roast, Hebrew recipe, one with timers). `test/ui/run.mjs` starts it, waits for the JSON, injects cookies with `context.addCookies`, runs the specs, kills it.
Command: `task test:ui` = `go build -o bin/uiharness ./test/uiharness && cd test/ui && npm ci && node run.mjs` (flags `--headed`, `--grep`). Falls back with a clear message if Chrome is missing (`CHROME_PATH` override).
Specs (each in EN and HE where marked *): 
- signin page and OIDC round trip via fake IdP*, language switch*, theme toggle persists
- home tiles/recent/favourites; list live search (htmx, push-url, Back restores), filter chips, show-more, empty state*
- recipe page flags as alice/bob/admin (same matrix as 7.3), share switch both directions + wording, copy, favourite heart, cook-log with photo, delete dialog (Keep it autofocus, Delete forever)
- editor: stage cards per mode (field visibility, disabled-not-submitted, Smoker woodfire forced, Broil hint, Gourmet two rows, pizza preset hides temp), warnings vs errors from oven spec, ingredient paste, step `[10:00]`, tags suggest, JSON submit 200 / 422 swap / 409 message, draft save-restore-discard, `beforeunload`, offline retry (context.setOffline), photo queue upload with real JPEG fixture and HEIC rejection message, cover/reorder/delete
- cook mode: step nav + swipe + arrow keys (RTL mirrored)*, hash restore, ingredients panel + tick-off, scaling, stage timer with 5-minute pre-alert (fake clock via `page.clock`), step timer, timers survive reload (`nwr_timers`), wake-lock requested (stub `navigator.wakeLock`), alert banner, Done dialog logs cook
- inbox: keep, keep+share, edit-then-keep, throw away, next-item redirect, badge
- settings/tokens: create shows token once with copy, revoke; admin categories CRUD
- PWA: manifest valid + icons 200, SW registers, offline navigation shows cached recipe, offline.html otherwise
- CSP: console has zero CSP violations on every page (listen to `securitypolicyviolation` and console errors)
- viewports: 360x740, 820x1180, 1366x768 with no horizontal scroll; RTL smoke screenshots (saved, not diffed)
- a11y smoke: every input labelled, focus visible, no `alert/confirm` use.
Not automatable (manual, 70 §10): real wake lock duration, vibration, camera capture, install-to-home-screen, Samsung HEIC setting.

### 7.6 Gates (A owns Taskfile)
`task ci` = fmt check, lint (golangci-lint + i18n:check + csp:check), test, build, test:e2e. `task test:ui` separate (needs Chrome). Done = both green and the Phase 2 checklist below.

## 8. Acceptance checklist (done means)
- [ ] Every cell of 25 §4 covered by a test; all enums/labels per 25 §3 (EN + HE).
- [ ] Migrations equal 30 §4-5; goose; newer-DB refusal.
- [ ] Every route in 40 §2-3 registered with a method pattern and exercised by a test; problem+json shape.
- [ ] CSP header exact; no inline script/style/handlers; `task csp:check` green; `/media` authz tested; OIDC verified signatures; no dev login.
- [ ] Config/CLI/Taskfile/Docker/deploy files per 20 and 50; image builds `CGO_ENABLED=0`, healthcheck works.
- [ ] i18n parity EN/HE (union), no forbidden words, RTL checks pass.
- [ ] `task ci` and `task test:ui` green on this machine.

## 9. Change requests
- **CR-1 (2026-10-29), assistant feedback loop**: decisions 4.13–4.16 in `docs/specs/open-questions.md`. New migration
  `0003` (`30 §5a`), source-key duplicate rule and `previously-reviewed` 409 (`40 §4`), `GET /api/v1/inbox/outcomes`,
  `GET /api/v1/assistant/brief`, `openapi.json`, `/api/v1/mcp` (`40 §7–§9`), throw-away reasons and brief textarea
  (`70 §4.7, §4.9`), tests `60 §6.3` items 4 and 6. **Implemented 2026-10-29.** Notes: the reason list reaches the inbox template through the
  `rejectReasons` template func (no view field to forget); MCP tools run the REST handlers through an in-process dispatcher
  (`internal/api/openapi.go`), with the SDK's localhost check disabled because `HostCheck` already pins the host; `test/harness`
  gained `Options.Env`; `test/ui/specs/inbox.spec.mjs` covers the dialog, the no-JS fallback and the brief box.
See `docs/contract-questions.md` (rulings 2026-10-27: Title()/Lang()/Dir() methods on page views, camelCase config JSON, `deref`, fmtQty/fmtUnit canonical format, ImageView.RecipeID).
