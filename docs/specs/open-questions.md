# Decisions Log & Open Questions

`00-brief.md` is never edited; changes to its requirements are recorded here. Each decision names the spec section
that implements it. "Owner" = decided by the project owner; "Spec" = decided during spec review, may be overridden
by the owner.

## Decisions

| # | Topic | Decision | By | Implemented in |
|---|---|---|---|---|
| 1.1 | Shared recipe permissions | Any household user can view, edit and delete a shared recipe. Private recipes: owner only. | Owner | `20 §7` |
| 1.1a | Who can unshare | Only the owner can change visibility (share/unshare). Ownerless shared recipes stay shared. Prevents one user from making a family recipe private to someone else. | Owner (2026-10-27) | `20 §7` |
| 1.2 | Session duration | 30 days sliding idle timeout; absolute maximum 365 days. Server restarts never log users out. | Owner | `20 §5.3` |
| 1.3 | Cook log, notes, ratings | Favourites are personal. Cook logs, post-cook notes and ratings on shared recipes are visible to everyone. Ratings exist only on cook logs (no per-recipe rating field). | Owner | `30 §4, §6` |
| 1.4 | Inbox scope | Per-user private inbox; a token submits to its owner's inbox. On Keep the user may share immediately. | Owner | `40 §2.4` |
| 1.5 | Admin role | Admins (Authentik group) manage the category list only; no extra recipe powers. | Owner (confirmed 2026-10-27) | `20 §7`, `40 §2.7` |
| 2.5 | Translation scope | UI translated (EN/HE); recipe content shown in its own language with correct direction. | Owner | `20 §10` |
| 2.6 | Categories | Shared, seeded, translated categories (0–5 per recipe, admin-managed) plus free tags. "Smoked" is derived from oven settings. | Owner (+ Spec for details) | `30 §5, §7`, `20 §11` |
| 3.7 | In-app timers | Stage timers with a 5-minute pre-alert (stages ≥ 10 min), step timers, several at once; alerts while the page is open. | Owner | `70 §5.2` |
| 3.8 | Deletion | Immediate hard delete with a strong two-button confirmation; no trash. | Owner | `70 §4.6`, `30 §3` |
| 3.9 | Image formats | **Revised:** JPEG, PNG, WebP only; HEIC is rejected with instructions (Samsung default is JPEG). The browser resizes photos before upload. Reason: the only Go HEIC decoder requires cgo, which conflicts with the static distroless build. | Owner (2026-10-27) | `20 §8, §9`, `70 §6.3` |
| 4.1 | Spec structure | One canonical document per concept (see `01-index.md`); other documents reference instead of restating. | Owner (2026-10-27) | `01-index.md` |
| 4.2 | Access gate | The app's `OIDC_ALLOWED_GROUPS` check is the primary gate so denied users get the friendly message; an Authentik binding is optional. | Spec | `20 §5.2`, `50 §6.5` |
| 4.3 | Sign-in screen | Unauthenticated users see an app page with one "Sign in" button and a language switch, not an immediate redirect to Authentik. | Owner (2026-10-27) | `40 §2.1`, `70 §4.10` |
| 4.4 | Concurrent edits | Optimistic concurrency (`recipes.version`); a conflicting save shows a message and keeps the draft. | Spec | `30 §4`, `40 §2.5` |
| 4.5 | Step timers | Steps carry an explicit optional `timer_seconds` (entered as `[mm:ss]`); no free-text time parsing. | Spec | `30 §4`, `40 §2.5` |
| 4.6 | Stage fields | Added `preheat_minutes` and `pellet_note` (e.g. "hickory"). | Spec | `25 §2` |
| 4.7 | Unshare side effects | Other users' favourites and "recently viewed" entries for that recipe are removed; cook logs are kept. | Owner (confirmed 2026-10-27) | `30 §3` |
| 4.8 | Copy to my recipes | Creates an independent private copy including duplicated photo files. | Owner (confirmed 2026-10-27) | `40 §2.2` |
| 4.9 | Assistant duplicates | `Idempotency-Key` header and 409 on an existing `source_url`. LAN image URLs are rejected; the assistant sends image bytes instead. | Spec | `40 §4` |
| 4.10 | Cook-log photos | **In v1**: one optional photo per cook-log entry, shown under "Our notes", not in the recipe gallery. | Owner (2026-10-27) | `30 §4`, `40 §2.2`, `70 §4.3` |
| 4.11 | User deletion | Only via `recipes user delete`; private recipes and inbox items are deleted, shared recipes are kept without an owner. | Spec | `30 §3`, `20 §17` |
| 4.12 | Pre-release migrations | Until the first production deployment, migrations `0001`/`0002` may be edited in place. | Spec | `30 §1` |
| 4.13 | Assistant memory of decisions | Every Keep / Throw away is recorded in `inbox_outcomes` and survives the hard delete. A source already kept or thrown away is refused with 409 `previously-reviewed`; `allow_duplicate` does not override it. Fixes thrown-away recipes being resubmitted. | Owner (2026-10-29) | `30 §5a`, `40 §4` |
| 4.14 | Source URL matching | Duplicates are matched on a normalized `source_key` (scheme, `www.`, tracking parameters, trailing slash, fragment ignored), not the raw URL. | Spec | `40 §4.3` |
| 4.15 | Assistant feedback | Optional throw-away reasons (5 fixed codes + short note); the assistant reads decisions from `GET /api/v1/inbox/outcomes` and a per-user "what to look for" text from `GET /api/v1/assistant/brief`. | Owner (2026-10-29) | `40 §7, §8`, `70 §4.7, §4.9`, `10 H3, H5, H6` |
| 4.16 | Assistant discovery | The app serves `openapi.json` and an in-process, stateless MCP server at `/api/v1/mcp` (same tokens, scopes and service methods as REST). Assistants such as Hermes connect without custom code. | Owner (2026-10-29) | `40 §9`, `60 §1` |
| 4.17 | Recipe search stays outside the app | The app does not run inference or crawl the web; an external assistant finds recipes and submits them through the API. In-app ML is limited to the optional categorizer (`20 §15`). | Owner (2026-10-29) | `00` req. 10, `20 §15` |
| 5.1 | Oven model | The household oven is the **Israeli OO101 variant**. Its function and preset names (incl. official Hebrew) are authoritative: Broil (not Top Heat), Gourmet Roast (not Specialty Roast), Chicago Style (not Deep Pan/Pan), Calzone, no Frozen preset. | Owner (2026-10-27) | `25 §1, §3` |
| 5.2 | Gourmet Roast | Stored as one stage with Stage 1 and Stage 2 temperature/time, as programmed on the oven. | Owner (2026-10-27) | `25 §2, §4` |
| 5.3 | Pellets | Record blend + free note only; a full smoke box per cook is assumed (no scoop count). | Owner (2026-10-27) | `25 §2` |
| 5.4 | Accessory frame | Always installed; level derived (top for Broil, bottom otherwise); not a selectable accessory. | Owner (2026-10-27) | `25 §2` |
| 5.5 | Smoker preheat | Per the manual, Smoker has no preheat (pellet ignition 3–4 min). | Spec (from manual) | `25 §4` |
| 5.6 | Oven ranges (was O1) | Per-function ranges are not published. Every temperature-bearing mode accepts 40–370 °C; soft warnings from online evidence only. | Owner (2026-10-27) | `25 §4` |
| 5.7 | Off-host backups (was O2) | Host cron + restic of `backups/` and `media/`; production uses a bind mount. | Owner (2026-10-27) | `50 §8.3` |
| 5.8 | Forgotten passwords (was O3) | Lior resets them manually in Authentik; no SMTP/recovery flow. | Owner (2026-10-27) | `50 §6.5a`, `70 §4.10` |

## Open questions / owner actions

| # | Question | Default until answered |
|---|---|---|
| O4 | Review the Hebrew wording in `10 §4`, `10 H3`/`H6`, `25 §3`, `30 §5` (and later `locales/he.toml`). | **Owner action** (Lior reviews); use as written until then |
| O5 | Categorizer engine (`20 §15`, story H4). Jev (jevai.net) is a hosted API only and cannot run on Cosmos/LocalAI. Candidates: keyword rules in Go; a multilingual embedding model behind an OpenAI-compatible `/v1/embeddings` endpoint (LocalAI, Ollama, TEI) with nearest-neighbour voting; or both. | `noop` |
