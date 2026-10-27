# 70 — UI & Front-end

Owner: UI developer. Status: **canonical** for screens, layout, interaction behaviour, client-side JS, PWA and
wording. Inputs: `10-user-stories.md` (what users need), `40-api.md` (routes; this document never invents URLs),
`20 §7` (permission predicates), `20 §8` (CSP), `20 §10` (i18n mechanics), `25` (oven fields and labels).

## 1. Principles

- Server-rendered HTML (`html/template`) + **htmx 2.x** (vendored, `/static/{v}/vendor/htmx.min.js`) + small
  vanilla-JS ES modules. No frameworks, no Hyperscript, no CDN, no build step.
- **CSP-clean** (`20 §8`): no inline `<script>` code, no `on*=` attributes, no `style=` attributes. Behaviour is attached
  by event delegation on `data-action` attributes. `task csp:check` enforces this.
- **Every user-visible string goes through `T`** (`20 §10`), including placeholders, `aria-label`s, dialog texts and
  flash messages. Wording follows `10 §4`.
- Big targets, high contrast, oven settings first, nothing lost.

## 2. Client JS architecture

| File | Loaded on | Responsibility |
|---|---|---|
| `js/app.js` | every page | Event delegation for `data-action`, dialogs, menu, theme, share-link, flash dismiss, SW registration |
| `js/editor.js` | editor pages | Stage cards (add/remove/move), field show/hide per mode, hints from embedded oven spec, tag chips, draft save/restore, JSON submit, photo queue |
| `js/upload.js` | editor | Client-side resize + sequential upload with progress (§6.3) |
| `js/cook.js` | cook mode | Stepper, swipe, wake lock, timers, ingredients panel, servings scaling, tick-off |
| `sw.js` | registered by app.js | §8 |

Pattern (no inline handlers):

```html
<button type="button" class="btn" data-action="open-dialog" data-target="delete-dialog">{{T "recipe.delete"}}</button>
```
```js
document.addEventListener('click', (e) => {
  const el = e.target.closest('[data-action]');
  if (!el) return;
  const fn = actions[el.dataset.action];
  if (fn) { e.preventDefault(); fn(el, e); }
});
```

Confirmations use native `<dialog>` elements rendered by the server (localized, big buttons), opened by
`data-action="open-dialog"`. `hx-confirm` and `window.confirm()` are not used.
JSON config for JS (oven spec, i18n strings needed client-side, user ID for draft keys) is embedded as
`<script type="application/json" id="…">` blocks.

## 3. Layout, sizing, theme

### 3.1 Breakpoints

| Name | Width | Layout |
|---|---|---|
| phone | < 600 px | Single column. **Bottom navigation bar** (Home, Search, + Add, Inbox (badge), Menu). Primary action buttons at the bottom within thumb reach |
| tablet | 600–1023 px | Two columns for lists. Recipe page: ingredients and steps side by side in landscape. Bottom nav in portrait, side rail in landscape |
| desktop | ≥ 1024 px | Top bar navigation, content max-width 1200 px, list grid 3–5 columns. Keyboard: `/` focuses search, `Ctrl+S` saves in the editor |

Device notes: Galaxy S25+/S26+ and S23 (~384–412 CSS px wide) are "phone"; Tab S9 and Tab S6 Lite are "tablet";
PCs are "desktop". The Tab S6 Lite is the performance floor: no JS on list pages beyond htmx, images lazy-loaded
with explicit `width`/`height`.

### 3.2 Sizes
- Base font 18 px (phones and tablets), 16 px desktop; line-height 1.5.
- Touch targets ≥ 48×48 px with ≥ 8 px spacing; primary actions ≥ 56 px high; cook-mode Next/Back ≥ 72 px high.
- Oven hero: mode ≥ 28 px bold uppercase; temperature and time ≥ 56 px (cook mode ≥ 72 px, `clamp()` to fit).
- No horizontal scrolling at 360 px; no pinch-zoom needed (do not disable zoom, though).

### 3.3 Theme
- Light and dark palettes as CSS custom properties; default follows `prefers-color-scheme`.
- Toggle ☀️/🌙 in the menu and in cook mode stores `theme = light|dark|auto` in `localStorage` (`nwr_theme`) and sets
  `data-theme` on `<html>` before first paint via `js/theme-init.js` loaded synchronously in `<head>` (external file,
  CSP-safe).
- Contrast: body text ≥ 7:1 in both themes (sunlight), no grey-on-white secondary text below 4.5:1. Dark theme
  uses near-black backgrounds and warm (not pure white) text so it is not blinding at night.

## 4. Screens

Each page's view model embeds `Layout` (`60 §2`). "Flags" are booleans computed from `20 §7`; templates show an
action only when its flag is true.

### 4.1 Home — `GET /`  (stories B1, B3, B4, D1, H1)
1. Search box (submits to `/recipes?q=`; live results start on the list page).
2. **Category tiles**: 2 per row on phones, emoji + localized name; order by `sort_order`; only categories that have at
   least one visible recipe. Plus a virtual **"Smoked 🔥"** tile → `/recipes?smoked=1`. Tile → `/recipes?category=<slug>`.
3. **Recently cooked / viewed** row (horizontal scroll of up to 10 cards, `30 §6`), each with `fmtAgo`. Hidden if empty.
4. **Favourites** row (hidden if empty) with "See all" → `/recipes?fav=1`.
5. Floating **"+ Add recipe"** button (phone) / button in top bar (desktop) → `/recipes/new`.
6. Inbox badge in the nav: "New recipes to review (3)", hidden when 0.

### 4.2 List & search — `GET /recipes`  (B1-AC2, B2, B5, B6, J4)
- Search input:
```html
<form id="filters" action="{{url "recipes.list"}}" method="get" role="search">
  <input type="search" name="q" value="{{.Q}}" dir="auto" autocomplete="off"
         placeholder="{{T "search.placeholder"}}" aria-label="{{T "search.label"}}"
         hx-get="{{url "recipes.list"}}" hx-trigger="input changed delay:300ms, search"
         hx-target="#results" hx-include="#filters" hx-push-url="true" hx-indicator="#search-spinner">
  … filter chips as checkboxes/radios inside the same form, each with
    hx-get="{{url "recipes.list"}}" hx-trigger="change" hx-target="#results" hx-include="#filters" hx-push-url="true"
</form>
<div id="results" aria-live="polite">{{template "results" .}}</div>
```
  (`input` rather than `keyup`, which is unreliable with Android keyboards and Hebrew input.)
- Filter chips: categories, oven mode, Smoked, scope (All / Just me / Family), Favourites; sort select.
  Active filters are visible and removable; a "Clear" button resets.
- Card: thumb (lazy, `object-fit: cover`, fixed aspect 4:3), title (`dir="auto"`), **primary mode + temperature**
  (e.g. "Max Roast · 250 °C"), badge **"Just me 🔒" / "Family 👨‍👩‍👧"**, 🔥 if woodfire, ♥ if favourite.
  Whole card is one link to `/recipes/{id}`.
- Empty state: "No recipes found for 'xyz'." + "Clear search" button.
- "Show more" button at the end (keyset cursor, `40 §2.3`).

### 4.3 Recipe page — `GET /recipes/{id}`  (C1, C4, E1, E2, F1, F2, G1, J1–J3)
Order top to bottom:
1. Title (`lang`/`dir` from `recipes.language`), visibility badge, ♥ button.
2. **Oven settings hero**: one numbered card per stage (`25 §5`), e.g.
   `① SMOKER · 120 °C · 2 h · 🔥 Woodfire ON (Robust, hickory)` then `② MAX ROAST · 250 °C · 10 min`.
   A Gourmet Roast stage shows both phases on one card: `GOURMET ROAST · 330 °C · 20 min → 140 °C · 1 h`.
   "Preheat first (≈ 25 min)" line when applicable. Recipes without stages show "No oven settings yet" (+ Edit link if allowed).
3. Big **"Start cooking"** button → `/recipes/{id}/cook`.
4. Cover photo and gallery (tap to view full size).
5. Ingredients with servings stepper (§5.4) — side by side with steps on tablet landscape.
6. Steps (numbered; timer chips shown when `timer_seconds` set).
7. Notes, categories, tags, "Found on: <host>" link (`source_url`).
8. "Cooked 4 times — last on 3 Oct · ★ 4.5 (2)" and **"Cooked it today"** form (optional note, optional rating,
   optional photo — resized in the browser like recipe photos, §6.3). "Our notes" list (newest first: author, date,
   stars, note, photo thumbnail that opens full size; the author can delete their own entry).
9. "Last changed 12 May by Lior" (E2) when `updated_by` is set.
10. Action bar: **Edit**, **Share with family** switch, **Copy to my recipes**, **Share link** (Web Share API, falls back to
    copy-to-clipboard), and a "⋯" menu containing **Delete forever** (red).

Required flags and expected values (tested in `60 §6.3`):

| Flag | Owner, private | Owner, shared | Other user, shared | Admin (not owner), shared | Ownerless shared |
|---|---|---|---|---|---|
| `CanEdit` (Edit button) | ✓ | ✓ | ✓ | ✓ | ✓ |
| `CanDelete` (Delete in ⋯ menu, and at bottom of editor) | ✓ | ✓ | ✓ | ✓ | ✓ |
| `CanChangeVisibility` (Share switch; others see the badge only) | ✓ | ✓ | ✗ | ✗ | ✗ |
| `ShowCopy` (= `CanCopy` and not owner) | ✗ | ✗ | ✓ | ✓ | ✓ |
| `CanFavourite`, `CanLogCook` | ✓ | ✓ | ✓ | ✓ | ✓ |
| Other user, private | → 404 page | | | | |

Share switch behaviour (F1): turning on shows "Everyone in the family will see this recipe." then posts
`/recipes/{id}/visibility` (htmx swaps the control); turning off shows "The family won't see this recipe any more."

### 4.4 Cook mode — `GET /recipes/{id}/cook`  (C2–C6)
- Sticky top strip: current stage summary (mode, °C, time) with stage switcher "Stage 1 of 2"; stage timer button.
- One step per screen, very large text; step counter "Step 3 of 7".
- Bottom bar: **Back** / **Next** (≥ 72 px), swipe left/right also navigates (direction flips in RTL), **Ingredients**
  button opens a full-height panel over the step (side panel on tablet landscape) with tap-to-tick items.
- Theme toggle ☀️/🌙; **"✕ Done"** button.
- Screen wake lock while this page is visible (§5.1).
- Done → `<dialog>` "Did you cook it?" with **Yes** (optional note + stars, posts the cook log, returns to the recipe
  page) and **Not now** (returns without logging).
- Works without network after load (all data is in the page).

### 4.5 Editor — `GET /recipes/new`, `GET /recipes/{id}/edit`, `GET /inbox/{id}/edit`  (D2–D5, E1, H3-AC3)
Sections in order; only **Name** is required:
1. **Name** (big input, `dir="auto"`).
2. **Photos**: "Add photo" button opening the normal Android chooser (`<input type="file" accept="image/jpeg,image/png,image/webp" multiple>`; no `capture` attribute, so both Camera and Gallery are offered). Thumbnails with upload progress, "Make cover", move ◀▶, delete ✕. §6.3.
3. **Oven settings**: stage cards with pickers (D2-AC2):
   mode `<select>` (localized name + dial name), pizza preset (pizza only; temperature hidden unless Custom),
   temperature (number, step 5, °C) with the recommended range shown as hint, time as hours + minutes (+ seconds for
   pizza); for **Gourmet Roast** two rows "Stage 1" and "Stage 2", each with temperature and time; Woodfire switch
   (forced on and disabled for Smoker; hidden for Broil and Keep Warm), pellet blend + note (when woodfire), preheat
   minutes (preheat modes), accessories checkboxes (Pizza Stone pre-selected for Pizza, Pro Heat Pan otherwise),
   core temperature, stage notes. For Broil a fixed hint "Frame on the top level" is shown (no input). "+ Add another stage", remove, move up/down. Fields not allowed for the chosen mode
   are hidden **and** not submitted. Hints and warnings come from the embedded oven spec (same table as the server).
4. **Ingredients**: one textarea, one ingredient per line, `# Section` lines for groups; pasting a block works (D2-AC3).
   Help text shows an example. Server parses per `40 §2.6`.
5. **Steps**: one textarea, one step per line; help text: "Add [10:00] at the end of a step to get a timer button."
6. **Categories**: tappable chips (multi-select, ≤ 5). **Tags**: chip input with suggestions (`/labels/suggest`),
   free text allowed (D2-AC4).
7. More (collapsed by default): description, notes, servings, prep time, language (Auto/English/Hebrew; auto-set to
   Hebrew when the name contains Hebrew letters), source link.
8. **Share with family** switch — only when creating (default off, D5); existing recipes use the switch on the recipe page.
9. Sticky **Save** button. Edit page: **Delete forever** at the very bottom (if `CanDelete`).

Validation errors appear next to fields and in a summary at the top that links to them; warnings are yellow and do not
block saving. On the inbox editor the button reads **"Save and keep"**.

### 4.6 Delete dialog  (G1)
"Delete '<title>' forever? This can't be undone. Photos will be deleted too." + for shared recipes "The whole family
will lose this recipe too." Buttons: **Keep it** (prominent, autofocus) and **Delete forever** (red), the latter a
`<form method="post" action="{{url "recipe.delete" .ID}}">`. After deletion: list page with flash "Recipe deleted".

### 4.7 Inbox — `GET /inbox`, `GET /inbox/{id}`  (H1–H4)
- List: cards "Found by your assistant" with title, photo, source host, received date; tap opens the item.
- Item page: same layout as the recipe page, plus "Found on: example.com" link and, if no stages, the banner
  "Oven settings missing — add them before cooking". Categories/tags suggested by the assistant are pre-selected.
- Bottom bar: **✓ Keep** (primary; a "Share with family too" checkbox next to it, off by default),
  **Edit, then keep**, **✕ Throw away** (opens a light `<dialog>` "Throw away this suggestion?").
- After Keep or Throw away the server redirects to the next item (H3-AC4) or to the empty inbox ("All done!").

### 4.8 Menu & settings — `GET /settings`
Language button showing the *other* language in its own script ("עברית" / "English") (I1-AC1), theme, "Assistant
access" (tokens), "Categories" (admins only), app version, **Sign out** (A4, at the bottom, posts `/auth/logout`).
The language button also appears in the bottom-nav menu sheet and on `/signin`.

### 4.9 Tokens — `GET /settings/tokens`
Table of tokens; create form (name, scopes as checkboxes with plain explanations, expiry); after creation the full
token is shown once in a read-only field with a "Copy" button and the warning "You won't see this again."

### 4.10 Sign-in and error pages
- `/signin`: app logo, one big **"Sign in" / "התחברות"** button, language button, and the small line
  "Forgot your password? Ask Lior." No technical text (A1-AC3).
- Not allowed (403 from callback): "You can't get in yet. Ask Lior to add you." + "Try another account" (logout link).
- `/auth/logged-out`: "Signed out" + Sign in button.
- 404: "We couldn't find that recipe. It may have been deleted." + Home button. 500: "Something went wrong. Try again."
  with the request ID in small print. 503 (sign-in unavailable): "Sign-in is temporarily unavailable. Try again in a minute."
- Offline (from the service worker): "No connection. Recipes you opened recently are still available." (§8).

### 4.11 Admin categories — `GET /admin/categories`
List with emoji, English name, Hebrew name, order, recipe count; inline edit; add form; delete with a dialog stating
how many recipes use it.

## 5. Cook-mode behaviour

### 5.1 Screen wake lock
```js
let lock = null;
async function acquire() {
  if (!('wakeLock' in navigator) || document.visibilityState !== 'visible') return;
  try { lock = await navigator.wakeLock.request('screen'); lock.addEventListener('release', () => { lock = null; }); }
  catch { /* unsupported or denied: show a one-time hint "Keep the screen on in phone settings" */ }
}
document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'visible' && !lock) acquire(); });
window.addEventListener('pagehide', () => { lock?.release(); });
acquire();
```
Requires a secure context: works on `https://` and `http://localhost`. For testing on a phone in development use
`adb reverse tcp:8080 tcp:8080` and open `http://localhost:8080` on the phone (a LAN IP over http will not work).

### 5.2 Timers (decision 3.7 + story C3)
- **Gourmet Roast**: one timer for the whole program (Stage 1 + Stage 2) with an extra notice "Stage 2 now: 140 °C" when
  Stage 1 ends (the oven switches by itself; the notice only informs).
- **Stage timers**: each stage with a duration has a "Start <duration>" button. Starting it creates a timer for that
  duration. If the duration is ≥ 10 min, a **pre-alert fires 5 min before the end**; every timer fires a final alert at 0.
- **Step timers**: steps with `timer_seconds` show a timer chip; tapping starts a countdown (final alert only).
- Several timers may run at once; a timer tray at the top shows all of them (label, remaining time, Stop).
  Timers keep running when changing steps or leaving and re-opening cook mode.
- State is stored in `localStorage` key `nwr_timers` as absolute end timestamps
  (`[{id, recipeId, label, endsAt, preAlertAt, firedPre, firedEnd}]`), so a reload or app switch does not lose them;
  on load, overdue alerts fire immediately. Timers older than 12 h are discarded.
- Alert: full-width flashing banner (respecting `prefers-reduced-motion` by using a colour pulse instead), chime via
  Web Audio (an `AudioContext` is created/resumed on the user's "Start" tap so it is allowed to play later), and
  `navigator.vibrate([400,200,400,200,400])` where supported. The final alert repeats the chime every 10 s until
  dismissed or for 2 min.
- Limitation (stated in the UI help): alerts are reliable while the cook-mode page is open, which the wake lock keeps
  the normal case. When the browser is in the background, JS timers are throttled; the alert fires as soon as the
  page is visible again. No push or server-side timers in v1.

### 5.3 Step navigation
Next/Back buttons, arrow keys on desktop, horizontal swipe (≥ 60 px, mostly horizontal) on touch. In RTL, swipe and
arrow directions mirror. Current step is kept in the URL hash (`#step-3`) so reload restores it.

### 5.4 Portion scaling (C6)
Servings stepper (−/+) on the recipe page and in the ingredients panel when `servings` is set. Each ingredient with an
`amount` carries `data-amount`; JS multiplies by `new/original` and formats like `fmtQty` (≤ 2 decimals, common
fractions for tsp/tbsp/pcs, convert g→kg and ml→l at ≥ 1000). Lines without an amount are unchanged. Not persisted.

### 5.5 Ingredient tick-off
Tapping an ingredient toggles a strike-through; stored in `sessionStorage` per recipe, cleared when leaving cook mode via Done.

## 6. Forms and data entry

### 6.1 htmx usage
Allowed patterns: live search and filters (§4.2), "Show more", favourite heart (`hx-post` → button fragment),
visibility switch (`hx-post` → control fragment), cook-log form (`hx-post` → cook-log section fragment). Every htmx
interaction has a working non-htmx fallback (normal form post + redirect). Navigation between pages is ordinary links
(no `hx-boost`), so wake lock and page scripts have clear lifecycles.

### 6.2 Editor submit
`editor.js` intercepts submit and posts with `fetch(form.action, {method:'POST', body:new FormData(form), headers:{Accept:'application/json'}})`:
- 200 JSON → upload queued photos (§6.3) → clear draft → navigate to `url`.
- 422 HTML → replace the form, scroll to the error summary; the draft stays.
- 409 → show the conflict message from the response; the draft stays.
- 401 → save draft, navigate to `/signin?return_to=<editor URL>`; after sign-in the draft-restore prompt appears.
- Network error → message "Not saved yet — no connection. We'll keep it here." with **Try again**; retry automatically
  on the `online` event.

### 6.3 Photos
- On pick, for each file: `createImageBitmap(file, {imageOrientation:'from-image'})`; on failure (typically HEIC) show
  the HEIC message from `20 §8` and skip the file. Draw onto a canvas with longest edge ≤ 2048 px and
  `canvas.toBlob(cb, 'image/jpeg', 0.85)`.
- Existing recipe: upload immediately, one at a time, via `XMLHttpRequest` (for `upload.onprogress`) to
  `POST /recipes/{id}/images` with field `image`. New recipe: queue the resized blobs in memory and upload after the
  first successful save (§6.2).
- A failed upload shows "Photo didn't upload — try again" with a retry button on that thumbnail; the recipe text is
  already saved (D3-AC4).
- Reorder and cover changes post `/recipes/{id}/images/order`.

### 6.4 Drafts (D4)
- Key: `nwr_draft:<userId>:<recipeId|new|inbox:<id>>`; value `{savedAt, baseVersion, fields}` (all form fields except
  files). Saved 1 s after the last input and on `visibilitychange` to hidden.
- On opening an editor with a stored draft: banner "Continue your unsaved recipe?" with **Continue** / **Discard**.
  For an existing recipe, if `baseVersion` < current version, add "This recipe was changed since — check before saving."
- Cleared on successful save and on Discard. Drafts older than 30 days are pruned on load. On sign-out, `app.js`
  removes all `nwr_draft:<userId>:*` keys (shared tablets).
- Photos are not part of drafts (the UI says so next to queued photos).
- Leaving an editor with unsaved changes triggers the browser's `beforeunload` prompt (D4-AC3).

## 7. Hebrew and RTL
Mechanics in `20 §10`. UI rules:
- `<html lang dir>` from the UI language; all CSS uses logical properties (`margin-inline-start`, `inset-inline-end`,
  `text-align: start`); directional icons use the `.icon-dir` class.
- Every user-entered text element has `dir="auto"` (or the recipe's `lang`/`dir`).
- Numbers with units are wrapped in `<bdi>` so "220 °C" and "1:30" are not reordered.
- Mode labels in Hebrew include the English dial name (`25 §3`).

## 8. PWA

`/manifest.webmanifest`:
```json
{ "name": "Ninja Woodfire Recipes", "short_name": "Recipes", "start_url": "/", "scope": "/",
  "display": "standalone", "background_color": "#1b1b1b", "theme_color": "#e85d04",
  "icons": [ {"src": "/icon-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any maskable"},
             {"src": "/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any maskable"} ] }
```
`/sw.js` (scope `/`):
- On install, pre-cache the current version's static assets and the offline page.
- HTML: network-first; on network failure serve the cached copy if this URL was cached, else the offline page.
- Recipe and cook-mode pages (`/recipes/{id}`, `/recipes/{id}/cook`) are cached after each successful load; keep the
  20 most recent (Nice-to-have "recently opened recipes readable offline").
- Never cache `/auth/*`, `/api/*`, POST responses, editor pages or `/inbox*`.
- On sign-out, `app.js` posts a message to the SW to delete the page cache (shared devices).
- In standalone mode the Authentik login opens inside the app window and returns to the app afterwards; verify in the
  device checklist.

## 9. Wording
The canonical wording table is `10 §4`; message keys use those texts. Forbidden words in UI copy: OIDC, token
(say "assistant access key"), label/tag (say "category"/"keyword"), entity, sync, 404, accept/reject (say Keep/Throw away).

## 10. Manual device checklist (before each release)

On a Galaxy phone (Chrome), Tab S6 Lite, and a desktop (Chrome + Firefox):
1. Install to home screen; opens full screen; signing in from the installed app works and lands on the requested page.
2. Recipe page readable at arm's length in sunlight (light theme) and at night (dark theme).
3. Cook mode: screen stays on for 10 min; Next/Back and swipe work in English and Hebrew; ingredients panel opens.
4. Stage timer with pre-alert and step timer run together; chime + vibration + banner fire; survive a reload.
5. Add recipe with a camera photo and a gallery photo; progress shown; airplane mode during save keeps the draft and
   retries when back online.
6. Paste a WhatsApp ingredient list (Hebrew and English); lines split and amounts recognised.
7. Hebrew UI: layout mirrored, numbers and temperatures read correctly, English recipe shows LTR inside it.
8. Delete flow wording and the "whole family" line for shared recipes.
9. Inbox: Keep / Throw away moves to the next item.
