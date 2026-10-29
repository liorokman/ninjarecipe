# 40 — HTTP Routes & REST API

Owner: Architect. Status: **canonical** for every URL, method, request/response shape and status code.
Authorization *rules* are canonical in `20-architecture.md §7`; this document says which rule each route applies.
Page layout and wording are in `70-ui-frontend.md`.

## 1. Conventions

- Routing uses Go ≥ 1.22 `http.ServeMux` **method + pattern** registrations (e.g. `mux.Handle("POST /recipes/{id}/edit", …)`)
  and `r.PathValue("id")`. Do **not** dispatch on `strings.HasSuffix(path)` or `r.Method` inside a catch-all handler;
  an unregistered method/path must yield 405/404 from the mux.
- **Browser routes** are HTML. All state changes are `POST` (HTML forms cannot send other methods; no method spoofing).
  The action is in the path (`/recipes/{id}/delete`), never in a hidden `_method` field.
  Every form's `action` and every link in templates must be built from the route table below; integration tests
  enforce this (`60 §6`).
- **JSON API** lives under `/api/v1`, uses bearer tokens only (never the session cookie), and may use `GET`/`POST`/`DELETE`.
  Request and response bodies are `application/json; charset=utf-8`; errors are RFC 9457 `application/problem+json`.
- IDs in paths are the UUIDs from `30`. A malformed or unknown ID → 404.
- If the actor cannot **view** a resource → 404 (existence is not leaked). Can view but not perform the action → 403.
- htmx requests (`HX-Request: true`) to list/search routes receive a fragment instead of a full page (§2.3).
- Unauthenticated access (session routes):
  - `GET` (non-htmx) → `303 /signin?return_to=<path+query>`.
  - htmx or `fetch` requests → `401` with `HX-Redirect: /signin?return_to=<current page path from HX-Current-URL>`.
  - Other `POST` → `303 /signin?return_to=<path of the same-origin Referer, else />`. The form's content is not
    replayed; the editor's on-device draft (`70 §6.4`) restores it after sign-in.
  - `return_to` must start with `/` and not `//` or `/\`; otherwise `/`.

## 2. Browser routes

Auth column: **public** = no session needed; **session** = signed-in user; **admin** = `users.is_admin`.
Authz column refers to `20 §7` predicates.

### 2.1 Public and authentication

| Method & path | Auth | Purpose |
|---|---|---|
| `GET /healthz` | public | Liveness, `200 text/plain "ok"`, no DB access |
| `GET /readyz` | public | Readiness, see `20 §13` |
| `GET /static/{version}/{path...}` | public | Embedded assets, immutable caching |
| `GET /manifest.webmanifest` | public | PWA manifest (`70 §8`) |
| `GET /sw.js` | public | Service worker; served from root so its scope is `/`; `Cache-Control: no-cache` |
| `GET /icon-192.png`, `GET /icon-512.png` | public | App icons at stable paths (manifest, Cosmos dashboard icon); `Cache-Control: public, max-age=86400` |
| `GET /signin` | public | Friendly sign-in page: one big "Sign in" button → `/auth/login?return_to=…`, plus language switch. If already signed in → `303 return_to` |
| `GET /auth/login` | public | Starts OIDC (`20 §5.2`) |
| `GET /auth/callback` | public | OIDC callback; on success `303 return_to`; on denial renders the "not allowed" page (`70 §4.10`) with status 403 |
| `POST /auth/logout` | session | Ends session and redirects to the IdP end-session endpoint (`20 §5.4`) |
| `GET /auth/logged-out` | public | "Signed out" page with "Sign in" button |
| `POST /settings/language` | public | Body `lang=en|he`, `return_to`. Sets the `lang` cookie; if signed in also `users.ui_language`. `303 return_to` |

### 2.2 Recipes

| Method & path | Auth / authz | Behaviour |
|---|---|---|
| `GET /` | session | Home: category tiles, Smoked tile, Recently cooked/viewed, Favourites, search box (`70 §4.1`) |
| `GET /recipes` | session | List/search with the query parameters in §2.3. Full page, or results fragment for htmx |
| `GET /recipes/new` | session | Empty editor |
| `POST /recipes` | session | Create (§2.5). New recipes are `active`, `private`, owned by the actor |
| `GET /recipes/{id}` | CanView | Recipe page. Records a `recipe_views` row (upsert, at most once per 10 min per user/recipe) |
| `GET /recipes/{id}/cook` | CanView | Cook mode. Records a view |
| `GET /recipes/{id}/edit` | CanEdit | Editor pre-filled, includes hidden `version` |
| `POST /recipes/{id}/edit` | CanEdit | Update (§2.5). Sets `updated_by`, `version+1` |
| `POST /recipes/{id}/delete` | CanDelete | Hard delete; `303 /recipes` with flash "Recipe deleted" |
| `POST /recipes/{id}/visibility` | CanChangeVisibility | Body `shared=1|0`. `303 /recipes/{id}`; htmx → re-rendered visibility control fragment |
| `POST /recipes/{id}/copy` | CanView | Creates a private copy owned by the actor, **duplicating image files** under new storage keys; copies categories, tags, stages, ingredients, steps; not cook logs, favourites, views. `303 /recipes/{newID}/edit` |
| `POST /recipes/{id}/favourite` | CanView | Body `on=1|0`. Idempotent. htmx → heart button fragment; else `303` back |
| `POST /recipes/{id}/cook-logs` | CanView | `multipart/form-data`: `note` (≤ 2000, optional), `rating` (1–5, optional), `photo` (one image file, optional; same limits and pipeline as recipe photos, stored with `kind='cook_log'`, `30 §4`). Records `cooked_at = now`. If the photo fails, the log is still saved and the response says "Photo didn't upload". `303 /recipes/{id}#cook-logs`; htmx → cook-log section fragment |
| `POST /recipes/{id}/cook-logs/{logID}/delete` | log author | Deletes one log entry and its photo |
| `POST /recipes/{id}/images` | CanEdit | Multipart, **one** file field `image`. Processes synchronously (`20 §9`). 201 with the image-tile HTML fragment and header `X-Image-Id`. 413/415/422 with a localized message fragment |
| `POST /recipes/{id}/images/{imageID}/delete` | CanEdit | Deletes one image |
| `POST /recipes/{id}/images/order` | CanEdit | Body `ids` (repeated, all image IDs of the recipe in the new order; first = cover). 400 if the set differs |
| `GET /media/{key}/{variant}` | CanView on the image's recipe (session **or** bearer with `recipes:read`) | `variant` ∈ `full`, `thumb` |
| `GET /labels/suggest?q=` | session | Up to 10 tags (prefix match, then substring) used on recipes the actor can view, plus all categories matching `q`. JSON `[{type, slug, name}]` |

### 2.3 List and search parameters (`GET /recipes`)

| Param | Values | Meaning (canonical semantics in `20 §11`) |
|---|---|---|
| `q` | text ≤ 200 | Full-text query |
| `scope` | `all` (default) \| `mine` \| `family` | `mine` = recipes I own (private and shared); `family` = all shared recipes |
| `category` | slug, repeatable | OR within the group |
| `tag` | text, repeatable | AND within the group |
| `mode` | OvenMode, repeatable | OR; any stage has that mode |
| `smoked` | `1` | Any stage `smoker` or woodfire on |
| `max_time` | minutes | Total oven time ≤ value |
| `fav` | `1` | Only my favourites |
| `sort` | `relevance` (default when `q`), `updated` (default otherwise), `title`, `cooked` (most cooked), `rating`, `recent` (my recently cooked/viewed) | |
| `cursor` | opaque string | Keyset pagination; page size 24 |

Response: full page, or — for htmx — the `#results` fragment containing cards and, when more results exist,
a "Show more" button carrying the next `cursor` (`hx-get`, `hx-swap="outerHTML"`). The search box uses
`hx-push-url="true"` so Back restores the query.

### 2.4 Inbox

| Method & path | Auth / authz | Behaviour |
|---|---|---|
| `GET /inbox` | session | Own inbox items, oldest first, as cards; link to each item |
| `GET /inbox/{id}` | owner of item | Full recipe view (like `/recipes/{id}`) with "Found on: <host>" link, missing-oven-settings banner, and **Keep / Throw away / Edit, then keep** buttons |
| `GET /inbox/{id}/edit` | owner of item | Editor for the item |
| `POST /inbox/{id}/edit` | owner of item | Saves the edits **and accepts** in one transaction (§2.5) |
| `POST /inbox/{id}/accept` | owner of item | Body `share=1` optional. Sets `status='active'` (and visibility), indexes FTS, publishes `InboxItemAccepted`. Redirect: `303 /inbox/{nextID}` (oldest remaining) or `303 /inbox` with flash when empty |
| `POST /inbox/{id}/reject` | owner of item | Body `reason` (optional, one of the codes in §7) and `note` (optional, ≤ 200). Hard delete and write the `rejected` outcome (`30 §5a`); same redirect as accept. Unknown `reason` → treated as absent |

The inbox badge count (pending items of the actor) is rendered in the layout on every page.

### 2.5 Recipe form contract (create / update / inbox edit)

Form fields (`multipart/form-data` not required; images are uploaded separately):

| Field | Notes |
|---|---|
| `version` | update only; must equal the stored version |
| `title`, `description`, `notes`, `language` (`en|he|und`), `servings`, `prep_minutes`, `source_url` | |
| `category` | repeated slug |
| `tags` | comma-separated text |
| `ingredients` | textarea, one ingredient per line; lines starting with `#` are section headers. Parsed per §2.6 |
| `steps` | textarea, one step per line (blank lines ignored). A step may end with `[mm:ss]` or `[h:mm:ss]` to set `timer_seconds`; the editor's step UI writes this syntax |
| `stage[N].<field>` | N = 0..9, fields as in `25 §2`; `stage[N].accessories` repeated |
| `visibility` | create only (`private` default). Changes after creation go through `/visibility` |

Responses:
- Plain form submit: success `303 /recipes/{id}` with flash; validation failure `422` with the editor re-rendered,
  field errors inline and all input preserved.
- Editor JS submit (`fetch` with `Accept: application/json`): success `200 {"id","url","version"}`; validation failure
  `422 text/html` containing only the `<form>` element re-rendered with errors (the JS swaps it in);
  the JS then uploads queued photos (`70 §6.3`).
- Version conflict → `409` page/fragment: "Someone changed this recipe while you were editing." with a link to open
  the current version; the on-device draft is kept so nothing typed is lost.
- Inbox edit: the item's `status` becomes `active` on success.

### 2.6 Ingredient line parsing (server-side, also used by the API `text` form)

1. Trim; empty → skip. `# Header` → `section` for following lines.
2. Match `^(?P<amount>\d+(?:[.,]\d+)?|\d+/\d+|\d+\s+\d+/\d+|[½¼¾⅓⅔])\s*(?P<unit>UNIT)?\s+(?P<item>.+)$`, where UNIT is
   any alias in the table below (case-insensitive). On match store `amount` (decimal comma accepted), `unit`, `item`.
3. Otherwise store `item = line`, `amount = NULL`, `unit = NULL`.
4. Always store `raw_text = line`. Rendering uses the parsed fields when `amount` is set, else `raw_text`.

| unit | aliases |
|---|---|
| `g` | g, gr, gram, grams, גרם, ג' |
| `kg` | kg, kilo, ק"ג, קילו |
| `ml` | ml, מ"ל |
| `l` | l, liter, litre, ליטר |
| `tsp` | tsp, teaspoon(s), כפית, כפיות |
| `tbsp` | tbsp, tablespoon(s), כף, כפות |
| `pcs` | pcs, pc, piece(s), יח', יחידות |
| `pinch` | pinch, קורט |

Portion scaling (`70 §5.4`) multiplies `amount` only; lines without an amount are shown unchanged.

### 2.7 Settings and admin

| Method & path | Auth | Behaviour |
|---|---|---|
| `GET /settings` | session | Language, theme, link to tokens, sign out, app version |
| `GET /settings/tokens` | session | Own tokens (name, prefix, scopes, created, last used, expiry, status) |
| `POST /settings/tokens` | session | Body `name`, `scopes` (repeated), `expires_in_days` (`0|30|90|365`). Renders the page with the **new token shown once** |
| `POST /settings/tokens/{id}/revoke` | token owner | Sets `revoked_at` |
| `POST /settings/assistant-brief` | session | Body `text` (≤ 2000, trimmed; empty clears it). Sets `users.assistant_brief` and `assistant_brief_updated_at`. `303 /settings/tokens` with flash "Saved" |
| `GET /admin/categories` | admin | List with rename / icon / order / delete |
| `POST /admin/categories` | admin | Create (`slug`, `name`, `name_he`, `icon`, `sort_order`, optional `description`) |
| `POST /admin/categories/{id}/edit` | admin | Update the same fields except `slug` |
| `POST /admin/categories/{id}/delete` | admin | Delete; confirmation shows how many recipes use it |
| `POST /admin/categorizer` | admin | Save the auto-categorization settings (`enabled`, `url`, `model`, `api_key`, `clear_key`, `threshold` as a percentage); 422 re-renders with field errors |
| `POST /admin/categorizer/test` | admin | Send a sample recipe to the saved engine and show the outcome |

## 3. JSON API overview

| Method & path | Scope | Purpose |
|---|---|---|
| `POST /api/v1/inbox` | `inbox:write` | Submit a recipe to the token owner's inbox (§4) |
| `GET /api/v1/inbox` | `inbox:write` | List own pending inbox items (`RecipeSummary[]`) |
| `DELETE /api/v1/inbox/{id}` | `inbox:write` | Withdraw an own pending item → 204 (404 if already accepted/rejected) |
| `GET /api/v1/inbox/outcomes` | `inbox:write` | Keep / Throw away decisions on the user's inbox items (§7) |
| `GET /api/v1/recipes` | `recipes:read` | Search recipes visible to the user. Params: `q`, `source_url` (matched by source key, §4.3), `scope`, `category`, `cursor`, `limit` (1–100, default 50). Returns `{items: RecipeSummary[], next_cursor}` |
| `GET /api/v1/recipes/{id}` | `recipes:read` | Full `Recipe` |
| `GET /api/v1/categories` | any valid token | `{categories: [{slug, name, name_he, icon}], tags: [string]}` (tags in use on recipes the user can view) |
| `GET /api/v1/assistant/brief` | any valid token | What the user wants the assistant to look for (§8) |
| `GET /api/v1/meta/oven` | public | The `25 §4` table as JSON (the same structure embedded in the editor) |
| `GET /api/v1/openapi.json` | public | OpenAPI 3.1 description of this API (§9.1) |
| `POST /api/v1/mcp` | any valid token; per tool as §9.2 | MCP server for AI assistants (§9.2). `GET`/`DELETE` → 405 |

Bearer handling, scopes, rate limit: `20 §6`. A token can never accept/reject inbox items, edit, share or delete
recipes; those are browser-only by design.

## 4. `POST /api/v1/inbox`

Headers: `Authorization: Bearer nwr_…`, `Content-Type: application/json`, optional `Idempotency-Key` (1–100 printable ASCII).

Behaviour:
1. If `Idempotency-Key` is present and an item/recipe of this user already has that `import_key` → `200` with that item's `ImportResult` (no new item).
2. If `source_url` is present and any recipe of this user (any status) has the same source key (§4.3) → `409` problem
   `duplicate` with `existing_id`, unless query `?allow_duplicate=1`.
3. Otherwise, if an `inbox_outcomes` row of this user has the same source key → `409` problem `previously-reviewed`
   with `outcome` (`kept`|`rejected`), `decided_at` and, when given, `reason`. `allow_duplicate` does **not** override
   this: a recipe the household threw away (or kept and later deleted) is never offered again through the API.
4. Validate (limits `20 §8`, oven rules `25 §4`). Errors → `422`.
5. Process images **synchronously**, in order. An image that fails (fetch error, SSRF block, too large, unsupported type)
   does not fail the request; it is reported in `warnings[]`.
6. Insert as `status='inbox'`, `visibility='private'`, owner = token user, `recipe_labels.source='assistant'`.
   Publish `InboxItemReceived`. `201` with `ImportResult`.

### 4.1 `RecipeImport` (request)

```jsonc
{
  "title": "Smoked short ribs",               // required, 1–200
  "description": "…",                          // ≤ 2000
  "notes": "…",                                // ≤ 8000
  "language": "en",                            // en | he | und (default und)
  "servings": 4,                               // 1–100
  "prep_minutes": 30,                          // ≥ 0
  "source_url": "https://example.com/ribs",    // http(s) URI ≤ 2000
  "source_name": "Example Kitchen",            // ≤ 100
  "categories": ["beef"],                      // known category slugs; unknown → warning, ignored
  "tags": ["weekend", "brisket"],              // free text, normalized (30 §7)
  "ingredients": [                             // ≤ 100; each EITHER structured OR text
    {"amount": 1.5, "unit": "kg", "item": "beef short ribs", "section": null},
    {"text": "2 tbsp smoked paprika"}
  ],
  "steps": [{"text": "Rub the ribs.", "timer_seconds": null}],   // ≤ 100, text ≤ 4000
  "oven_stages": [ OvenStage ],                // ≤ 10; fields exactly as 25 §2
  "images": [                                  // ≤ MAX_IMAGES_PER_RECIPE; first = cover
    {"url": "https://example.com/ribs.jpg"},
    {"data_base64": "…", "caption": "…"}
  ]
}
```

`OvenStage` JSON: `{mode, pizza_preset, temperature_c, duration_seconds, temperature2_c, duration2_seconds,
preheat_minutes, woodfire, pellet_blend, pellet_note, accessories: [], target_core_temp_c, notes}`; omitted = null/default.
Output DTOs additionally include the derived `preheat` and `frame_level` (`25 §2`).
The JSON schema carries only types and enums; numeric ranges are enforced by `oven.Validate` (`25 §4`), so the schema
must not declare stricter min/max values. `image.url` is fetched with the SSRF guard (`20 §8`): **LAN or localhost URLs
are always rejected** — an assistant on the LAN must send `data_base64` instead.

### 4.2 `ImportResult` (response)

```json
{
  "id": "7b8e192a-3c48-4384-a140-5bbf2f0739c9",
  "status": "inbox",
  "title": "Smoked short ribs",
  "review_url": "https://recipes.example.com/inbox/7b8e192a-3c48-4384-a140-5bbf2f0739c9",
  "created_at": "2026-10-27T13:30:00.000Z",
  "warnings": [
    {"name": "oven_stages[0].temperature_c", "code": "out_of_range", "params": {"min": 60, "max": 190}},
    {"name": "images[1]", "code": "fetch_failed", "params": {"status": 404}},
    {"name": "categories[2]", "code": "unknown_category", "params": {"value": "bbq"}}
  ]
}
```

### 4.3 Source key (duplicate detection)

`source_key` is derived from `source_url` by one function (`internal/textnorm.SourceKey`), used for recipes created
in the browser, API imports, outcomes, and the `source_url` search parameter. `NULL` when `source_url` is empty or does
not parse as an absolute http(s) URL.

1. Drop the scheme (`http` and `https` are the same recipe), userinfo, the default port and the fragment.
2. Host: lower-case, IDNA to ASCII, strip one leading `www.`.
3. Path: remove dot segments, decode percent-encoded unreserved characters, remove a trailing `/` (except for `/`).
4. Query: drop `utm_*`, `fbclid`, `gclid`, `mc_cid`, `mc_eid`, `igshid`, `ref`; sort the remaining parameters by
   name, then value. Omit `?` when none remain.
5. Result: `host + path [+ "?" + query]`, e.g. `https://www.Example.com/ribs/?utm_source=x#top` → `example.com/ribs`.

Sites that serve one recipe at several paths (print pages, AMP) are not merged. Assistants should send the page's
`<link rel="canonical">` URL as `source_url`.

## 5. Errors

```json
{
  "type": "https://recipes.example.com/problems/validation",
  "title": "Validation failed",
  "status": 422,
  "detail": "1 field is invalid.",
  "instance": "/api/v1/inbox",
  "request_id": "01JB…",
  "invalid_params": [
    {"name": "oven_stages[0].woodfire", "code": "forbidden", "params": {"mode": "broil"}, "reason": "Woodfire cannot be used with Broil."}
  ]
}
```

`type` URIs: `…/problems/validation`, `unauthorized`, `forbidden`, `not-found`, `conflict`, `duplicate`
(adds `existing_id`), `previously-reviewed` (adds `outcome`, `decided_at`, `reason`), `too-large`,
`unsupported-media-type`, `rate-limited`, `internal`. `reason` is English;
clients should use `code` + `params`.

| Status | When |
|---|---|
| 200 | OK; idempotent replay of `POST /api/v1/inbox` |
| 201 | Created |
| 204 | Deleted/withdrawn |
| 400 | Malformed JSON, a field of the wrong type, an unknown field (the `detail` names the field), bad query parameter |
| 401 | Missing/invalid/expired/revoked token (`WWW-Authenticate: Bearer`) |
| 403 | Token lacks the scope; or session user may view but not modify |
| 404 | Not found or not viewable |
| 405 | Method not allowed (from the mux) |
| 409 | Duplicate `source_url`; source already kept or thrown away; recipe version conflict |
| 413 | Body or file too large |
| 415 | Unsupported image type |
| 422 | Validation failed |
| 429 | Rate limited (`Retry-After`) |
| 500 | Internal error; `request_id` logged |

## 6. Output DTOs (API)

`RecipeSummary`: `{id, title, status, visibility, owner: {id, display_name} | null, language, categories: [slug],
tags: [string], primary_mode, total_oven_seconds, uses_woodfire, cover_image: {thumb_url, full_url} | null,
source_url, updated_at}`.

`Recipe`: `RecipeSummary` + `{description, notes, servings, prep_minutes, source_name, ingredients: [{section, amount,
unit, item, raw_text}], steps: [{text, timer_seconds}], oven_stages: [OvenStage], images: [{id, thumb_url, full_url,
width, height, caption}], version, created_at, created_by, updated_by}`. Cook logs are not exposed in v1.
Image URLs are absolute, built from `APP_BASE_URL`.

## 7. Inbox outcomes (`GET /api/v1/inbox/outcomes`)

Lets the assistant learn what the household keeps and throws away. Params: `after` (cursor from a previous response;
omit for the start), `limit` (1–100, default 50). Items in decision order (oldest first).

```json
{
  "items": [
    {
      "cursor": "42",
      "item_id": "7b8e192a-3c48-4384-a140-5bbf2f0739c9",
      "recipe_id": null,
      "title": "Smoked short ribs",
      "source_url": "https://example.com/ribs",
      "outcome": "rejected",
      "reason": "too_complicated",
      "note": "Two days of prep",
      "edited": false,
      "shared": false,
      "decided_at": "2026-10-29T18:02:11.000Z"
    }
  ],
  "next_cursor": "42"
}
```

- `recipe_id` = `item_id` when the outcome is `kept` and the recipe still exists; otherwise `null`.
- `next_cursor` is the cursor of the last item, or the `after` value when `items` is empty (`null` when there is none
  yet), so the assistant stores it and asks again later. A malformed `after` or a `limit` outside 1–100 → 400.
- `source_url` is the URL the assistant submitted (30 §5a).
- `reason` is `null` or one of the codes below. `note` is free text in any language (≤ 200).

| `reason` | Shown to the user as (`10 §3 H3`) |
|---|---|
| `not_our_taste` | Not our taste |
| `too_complicated` | Too complicated |
| `not_for_our_oven` | Doesn't suit our oven |
| `already_have` | We already have one like it |
| `poor_details` | Missing or wrong details |

## 8. Assistant brief (`GET /api/v1/assistant/brief`)

```json
{ "text": "More smoked fish. Neapolitan dough variations. Nothing with pork.", "language": "he", "updated_at": "2026-10-29T17:40:00.000Z" }
```

`text` is the user's free text from `/settings/tokens` (`""` when not set; `updated_at` then `null`). `language` is
`users.ui_language`, else `APP_DEFAULT_LANG`: the language the user reads, so the assistant can prefer recipes in it.

## 9. Discovery for AI assistants

### 9.1 OpenAPI (`GET /api/v1/openapi.json`)

OpenAPI 3.1, hand-maintained as `internal/api/openapi.json` (embedded). Covers every `/api/v1` route except `/mcp`,
with the DTOs of §4, §6, §7, §8 and the problem types of §5. `servers[0].url` is set to `${APP_BASE_URL}/api/v1` when
served. `Cache-Control: no-cache`. A test keeps it in step with the route table (`60 §6.3`).

### 9.2 MCP server (`POST /api/v1/mcp`)

An [MCP](https://modelcontextprotocol.io) server so assistants such as Hermes can use the API as tools without custom
HTTP code.

- Transport: MCP **Streamable HTTP**, stateless: no `Mcp-Session-Id`, no server-initiated messages, responses are
  `application/json` (no SSE). `GET` and `DELETE` → 405, as the transport allows.
- Auth, scopes, rate limit and body limit as every `/api/v1` route (`20 §6`): `Authorization: Bearer nwr_…`. The MCP
  request counts once against the rate limit, however many tools it calls. A tool whose scope the token lacks is left
  out of `tools/list`; calling it anyway returns an `isError` result carrying the `forbidden` problem.
- Implemented in `internal/mcp` with `github.com/modelcontextprotocol/go-sdk`. Each tool runs its REST twin's handler
  in-process (`internal/api` passes `internal/mcp` a dispatcher), so results, validation and errors are identical and
  there is no MCP-only behaviour. A 204 becomes `{"ok":true}`. Tool input schemas are the REST request schemas:
  `submit_recipe` embeds `RecipeImport` from `openapi.json` (as `$defs`).
- Tool errors are results with `isError: true` whose text content is the problem JSON of §5, so the assistant sees
  `code` and `params`.

| Tool | REST twin | Scope |
|---|---|---|
| `get_assistant_brief` | `GET /assistant/brief` | any |
| `list_categories` | `GET /categories` | any |
| `get_oven_reference` | `GET /meta/oven` | any |
| `search_recipes` | `GET /recipes` | `recipes:read` |
| `get_recipe` | `GET /recipes/{id}` | `recipes:read` |
| `submit_recipe` | `POST /inbox` (args: `recipe` = `RecipeImport`, optional `idempotency_key`, `allow_duplicate`) | `inbox:write` |
| `list_inbox` | `GET /inbox` | `inbox:write` |
| `withdraw_inbox_item` | `DELETE /inbox/{id}` | `inbox:write` |
| `list_outcomes` | `GET /inbox/outcomes` | `inbox:write` |

Server `instructions` (sent on `initialize`, English, fixed text):

> You help a household that cooks on a Ninja Woodfire OO101 outdoor oven find recipes. Before searching, read
> get_assistant_brief and list_outcomes to learn what they want and what they threw away. Before submitting, check
> search_recipes with the source URL. Always fill oven_stages using get_oven_reference: map the recipe's method to
> the oven's modes and stay within the listed ranges. Send the page's canonical URL as source_url and use known
> category slugs from list_categories. Submissions go to the user's review list; you cannot keep, share, edit or
> delete recipes.
