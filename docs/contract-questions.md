# Contract questions and decisions (append-only; the architect answers)

## Q1 (Package A, 2026-10-27): ambiguous selectors Title/Lang/Dir on RecipeView, CookView, InboxItemView
Contract 4.3 embeds both `Layout` (page title, UI lang/dir) and `RecipeBody` (recipe title, recipe lang/dir) in the same struct. Go and
html/template treat `.Title/.Lang/.Dir` as ambiguous selectors there ("can't evaluate field Lang"), so every page would fail.
Chosen (spec-compliant, no field renamed): the three structs get methods Title(), Lang(), Dir() returning the RecipeBody values, so page templates keep
using `.Title` for the recipe. The layout must read the page values as `.Layout.Title/.Lang/.Dir`, valid for every view model.
Alternative if the architect prefers: rename RecipeBody.Lang/Dir/Title (would touch the whole contract).

## Q2 (Package A): additive view-model fields
- `Layout.ClientI18n map[string]string` (contract mentions `#client-i18n` filled "by Go" but gives no field).
- `ImageView.RecipeID` (image_tile needs the recipe id for delete URLs).
- JSON key names of #editor-config and #cook-config are camelCase (contract left them open); documented in docs/PHASE0_DONE.
- Extra template funcs add, sub, list.

## B (UI) notes, 2026-10-27
- B1: Taskfile `test:ui` was absent, so B appended it (builds bin/uiharness, `npm ci`, node unit tests, then `node run.mjs`). A may restyle it.
- B2: Pointer args: templates pass `*int`/`*float64` to T as Count; go-i18n plural fails on pointers, so B uses `(add . 0)`. A `deref`-style func would be cleaner.
- B3: fmtQty prints fractions for every unit (e.g. "1 ½" kg); JS scaling mirrors that format but shows decimals for g/kg/ml/l. Align if desired.
- B4: Fixtures harness does not supply InboxCount, so the inbox badge is untested until real mode; `test/ui/run.mjs --real` expects the same JSON line from `go run ./test/uiharness`.
- B5: Icons: web/static/icons/icon-192.png and icon-512.png exist; /icon-192.png and /icon-512.png routes should serve them. sw.js uses `typeof __PRECACHE__` guard and `'__ASSET_VERSION__'` string token.
- B6: Form field names used (per spec 40): visibility POST `shared=1|0`, favourite `on=1|0`, tokens `name, scopes (repeat), expires_in_days`, cook log `note, rating, photo`, inbox accept `share=1`.

---
## Architect rulings (2026-10-27)
- **Q1: accepted.** Keep field names. Methods `Title()/Lang()/Dir()` on RecipeView, CookView, InboxItemView return the RecipeBody values. Layout is always read as `.Layout.Title/.Lang/.Dir` (layout.html and any partial rendered with a page view). Partials that get a bare RecipeBody are unaffected. Fixtures must exercise this.
- **Q2: accepted.** `Layout.ClientI18n map[string]string`, `ImageView.RecipeID`, funcs `add sub list` are contract. JSON keys of `#editor-config`/`#cook-config` are **camelCase** (recipeId, userId, draftKey, signinUrl, ...); the Go struct tags must be camelCase and documented in PHASE0_DONE; the oven-spec JSON stays snake_case (it is also the public API). 
- **B1: accepted.** `test:ui` stays; A may restyle but keep the name and `bin/uiharness`.
- **B2: add `deref`.** Go func `deref` accepts `*int|*int64|*float64|*string` (nil -> zero value) and returns the value; B replaces `(add . 0)` with `deref`. `add` stays for arithmetic.
- **B3: one canonical format (70 §5.4 wins over 20 §10's generic "fractions").** Rule: g/kg/ml/l -> decimals, max 2, trailing zeros trimmed, decimal separator per locale; tsp/tbsp/pcs/pinch -> common fractions (½ ¼ ¾ ⅓ ⅔, mixed "1 ½") else <=2 decimals; at >=1000 g->kg and ml->l (value/1000) - applies to Go and JS identically, also at scale 1. Contract change: `fmtQty amount unit` returns the **number text only** (after conversion) and new func `fmtUnit amount unit` returns the **converted unit key** (g|kg|ml|l|...); `IngredientView` gains `AmountText` (number) and `UnitKey` (post-conversion) and templates render `<span class="qty">{{.AmountText}}</span> <span class="unit" data-unit="{{.Unit}}">{{T (printf "unit.%s" .UnitKey)}}</span>` with `data-amount` = raw amount, `data-unit` = raw unit on the `<li>`. JS re-renders qty and unit label (labels from `js.unit.<key>` in `#client-i18n`, B owns, same text as `unit.*`). Golden test cases shared: 250 g, 1000 g -> 1 kg, 1500 ml -> 1.5 l, 0.5 tsp -> ½, 1.25 tbsp -> 1 ¼, 2.333 pcs -> 2 ⅓ , 0.125 kg -> 0.13 kg(sic: 2 decimals).
- **B4: accepted.** Fixtures must set `Layout.InboxCount` (e.g. 3); `Layout.InboxCount` stays a plain int on every view (0 hides badge) - no extra field.
- **B5: accepted.** Icon routes serve `static/icons/icon-{192,512}.png`; A's `/sw.js` handler replaces `'__ASSET_VERSION__'` and `__PRECACHE__` (bare token inside the guarded expression) exactly as B wrote them.
- **B6: accepted as canonical** (matches 40): visibility `shared=1|0`, favourite `on=1|0`, tokens `name, scopes (repeat), expires_in_days`, cook log `note, rating, photo`, inbox accept `share=1`.

## A (backend) status notes
- Rulings applied: deref, fmtQty/fmtUnit (+IngredientView.UnitKey), fixtures InboxCount=3, icon/sw.js serving, B6 names.
- Real mode: `go run ./test/uiharness [-web ./web]` prints the same JSON line (plus `token`, `idp`); `node run.mjs --real` -> 83/88 pass.
- UI suite mismatches in real mode (not backend bugs, for B / architect):
  1. editor.spec "409 shows conflict": relies on the fixture-only magic title "conflict"; a new recipe cannot conflict in the real app. Needs a real stale-version scenario (edit page open in two contexts).
  2. recipe.spec "no-stage empty hero", "share switch wording", "delete dialog wording": run after a test that POSTs /settings/language lang=he as alice; per 20 §10 the stored `users.ui_language` beats the `lang` cookie, so later English tests see Hebrew. Tests must restore the language (POST lang=en) or use a fresh user.
  3. list.spec "card shows primary mode, temperature": first card is whatever was updated last (test-created recipe without stages). Select a known recipe card.
