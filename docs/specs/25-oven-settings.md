# 25 — Ninja Woodfire OO101 Oven Settings Model

Owner: Architect. Status: **canonical** for oven modes, stage fields, enums, labels, validation ranges and derived
oven values. The table columns live in `30-data-model.md` (`oven_stages`); the wire format in `40-api.md`
(`OvenStage`). Neither restates the rules below.

The app **does not control the oven**. It records *what the cook should dial in*. Validation catches typos and
nonsense and warns when a value looks unusual; it must never stop someone saving a recipe that works on their oven.

## 1. The oven and the evidence

The household's oven is the **Israeli variant of the Ninja Woodfire Outdoor Oven OO101** (the brief names OO101UK;
decision 5.1 in `open-questions.md`). Function and preset names below are the Israeli ones as supplied by the owner.

Sources:
- **Owner** (2026-10-27): Israeli function and pizza-preset list with Hebrew names (authoritative for names).
- **Ninja Woodfire OO101ANZ Owner's Guide** (the metric-market manual, same hardware): operating steps, preheat
  behaviour, woodfire rules, accessories.
- **Ninja UK product page** (OO101UK): 8 functions, 40–370 °C, pizza in under 3 minutes at 370 °C.

| Fact | Value | Source / confidence |
|---|---|---|
| Functions (left dial) | Pizza, Gourmet Roast, Max Roast, Smoker, Bake, **Broil** (UK/ANZ: "Top Heat"), Dehydrate, Keep Warm | Owner — certain |
| Pizza presets (right dial) | Artisan, Thin Crust, New York Style, **Chicago Style** (UK/ANZ: "Deep Pan"), Calzone, Custom. No "Frozen" preset on the Israeli model | Owner — certain |
| Pizza temperature | Fixed for the presets; settable only with **Custom** | ANZ manual — high |
| Pizza timer | Sets a cook timer; after it ends the oven stays hot up to 1 h for back-to-back pizzas; "READY" shows when the stone is back to temperature | ANZ manual — high |
| Gourmet Roast | Programmed as **Stage 1 temp + time, then Stage 2 temp + time**; the oven switches to Stage 2 automatically. Example from the quick-start chart: whole chicken 330 °C, then 140 °C | ANZ manual — high |
| Preheat | Pizza, Gourmet Roast, Max Roast, Bake preheat (≈ 5–25 min, "ADD FOOD" when ready). **Smoker has no preheat** (pellets ignite for 3–4 min, then cooking starts). Broil, Dehydrate, Keep Warm: no preheat | ANZ manual — high |
| Woodfire Flavour | Optional pellet smoke for every function **except Broil and Keep Warm**; always on for Smoker | ANZ manual — high |
| Pellets | Fill the smoke box to the top using the scoop, every cook. Ninja Woodfire pellets only. Blends sold: All-Purpose, Robust | ANZ manual — high |
| Accessory frame | Must always be installed: **bottom level** for every function, **top level for Broil** | ANZ manual — high |
| In the box | Accessory Frame, Pizza Stone, Pro Heat Pan, Roast Rack (nests in the pan), Pellet Scoop, (ANZ: perforated pizza peel) | ANZ manual — high |
| Pans | Pizza → Pizza Stone. Every other function → Pro Heat Pan (+ Roast Rack optional). Deep-dish/own bake pans go **on** the Pro Heat Pan (not included) | ANZ manual — high |
| Overall temperature range | 40 °C – 370 °C | UK product page — high |
| Per-function temperature/time ranges | **Not published** in the manual or product pages | — |
| Oils above 260 °C | Manufacturer warns not to use butter/olive oil/margarine above 260 °C (smoke/fire risk) | ANZ manual — high |
| Food probe | Not built in; a target core temperature is still useful with an external thermometer | Medium |

Because per-function ranges are not published, the owner chose **wide hard limits**: every temperature-bearing mode
accepts 40–370 °C. Soft "unusual for this mode" warnings use the best online evidence (US manual °F ranges converted)
and never block saving.

## 2. Settings model (per oven stage)

A recipe has **0..10 ordered oven stages**. Example: Smoker 120 °C for 3 h, then Max Roast 260 °C for 10 min.
A Gourmet Roast stage is **one** stage with two phases, because that is how it is dialled in.

| Field | Type | Required | Rules |
|---|---|---|---|
| `position` | int ≥ 1 | yes | Set by the server from array order |
| `mode` | enum `OvenMode` | yes | §3 |
| `pizza_preset` | enum `PizzaPreset` | iff mode = `pizza` | |
| `temperature_c` | int | per §4 | °C; for Gourmet Roast this is **Stage 1**. UI step 5 |
| `duration_seconds` | int | per §4 | For Gourmet Roast this is **Stage 1**. UI shows `m:ss` below 1 h, `h:mm` from 1 h |
| `temperature2_c` | int | only and required for `gourmet_roast` | Stage 2 temperature |
| `duration2_seconds` | int | only and required for `gourmet_roast` | Stage 2 time |
| `preheat_minutes` | int 0–120 or null | optional; only for modes that preheat | Expected preheat time, display only |
| `woodfire` | bool | yes (default false) | Forced `true` for `smoker`; must be `false` for `broil`, `keep_warm` |
| `pellet_blend` | enum `PelletBlend` or null | optional; only if `woodfire` | |
| `pellet_note` | text ≤ 60 or null | optional; only if `woodfire` | e.g. "hickory", "cherry" |
| `accessories` | set of enum `Accessory` | optional | Editor pre-selects `pizza_stone` for pizza, `pro_heat_pan` otherwise |
| `target_core_temp_c` | int 30–100 or null | optional | For an external thermometer |
| `notes` | text ≤ 500 | optional | e.g. "turn halfway", "cheese for the last 2 min" |
| `preheat` | bool | derived | True for `pizza`, `gourmet_roast`, `max_roast`, `bake` |
| `frame_level` | `bottom`/`top` | derived | `top` for `broil`, else `bottom` (the frame is always installed) |

## 3. Enumerations and labels

```
OvenMode    = pizza | gourmet_roast | max_roast | smoker | bake | broil | dehydrate | keep_warm
PizzaPreset = artisan | thin_crust | new_york | chicago | calzone | custom
PelletBlend = all_purpose | robust | other
Accessory   = pizza_stone | pro_heat_pan | roast_rack | own_pan
```

i18n keys `oven.mode.<key>`, `oven.preset.<key>`, `oven.pellet.<key>`, `oven.accessory.<key>`, `oven.frame.<key>`.
The Hebrew mode and preset names are the **official Israeli names**; in the Hebrew UI show them with the English
name in parentheses, e.g. "צלייה מקס (Max Roast)", so the label matches either printing. Hebrew wording for blends,
accessories and frame levels is for the owner's review (open question O4).

| Key | English | Hebrew |
|---|---|---|
| `pizza` | Pizza | פיצה |
| `gourmet_roast` | Gourmet Roast | צלייה גורמה |
| `max_roast` | Max Roast | צלייה מקס |
| `smoker` | Smoker | עישון |
| `bake` | Bake | אפייה |
| `broil` | Broil | השחמה |
| `dehydrate` | Dehydrate | ייבוש מזון |
| `keep_warm` | Keep Warm | שמירת חום |
| `artisan` | Artisan | פיצה ביתית |
| `thin_crust` | Thin Crust | בצק דק |
| `new_york` | New York Style | New York Style |
| `chicago` | Chicago Style | Chicago Style |
| `calzone` | Calzone | Calzone |
| `custom` | Custom | מותאם אישית |
| `all_purpose` / `robust` / `other` | All-Purpose / Robust / Other | כללי / עז / אחר |
| `pizza_stone` / `pro_heat_pan` / `roast_rack` / `own_pan` | Pizza Stone / Pro Heat Pan / Roast Rack / Own pan or dish | אבן פיצה / תבנית Pro Heat / רשת צלייה / תבנית משלך |
| frame `bottom` / `top` | Frame: bottom level / Frame: top level | מסגרת: מדף תחתון / מסגרת: מדף עליון |

## 4. Validation table

Hard limits (Accepted) are the only blocking limits anywhere in the system; JSON schema, HTML `min`/`max` and DB
`CHECK`s must not be stricter. Recommended bands produce non-blocking warnings. Durations in seconds; limits inclusive.
"—" = must be null.

| Mode | Temperature | Recommended °C (warn) | Accepted °C | Recommended time | Accepted time | Woodfire | Preheat |
|---|---|---|---|---|---|---|---|
| `pizza` + preset ≠ `custom` | — (fixed by preset) | — | — | 60 s – 20 min | 30 s – 60 min | allowed | yes |
| `pizza` + `custom` | required | 175–370 | 40–370 | 60 s – 20 min | 30 s – 60 min | allowed | yes |
| `gourmet_roast` | Stage 1 and Stage 2 required | Stage 1: 200–370; Stage 2: 95–250 | 40–370 each | Stage 1: 5–30 min; Stage 2: 10 min – 4 h | each 1 min – 6 h | allowed | yes |
| `max_roast` | required | 230–370 | 40–370 | 5 min – 2 h | 1 min – 4 h | allowed | yes |
| `smoker` | required | 60–190 | 40–370 | 30 min – 12 h | 1 min – 24 h | **forced on** | no (ignition 3–4 min) |
| `bake` | required | 150–370 | 40–370 | 5 min – 4 h | 1 min – 6 h | allowed | yes |
| `broil` | required | 260–370 | 40–370 | 1 – 30 min | 30 s – 60 min | **forbidden** | no |
| `dehydrate` | required | 40–90 | 40–370 | 1 h – 12 h | 10 min – 24 h | allowed | no |
| `keep_warm` | — (fixed by oven) | — | — | ≤ 2 h | 0 – 12 h, optional (null = until switched off) | **forbidden** | no |

Cross-field rules (hard; code `invalid` or `forbidden`):
- `pizza_preset` iff `mode = pizza`.
- `temperature2_c` and `duration2_seconds` iff `mode = gourmet_roast`.
- `pellet_blend`, `pellet_note` only when `woodfire = true`.
- `preheat_minutes` only for modes with Preheat "yes".
- `accessories` values unique (server sorts and de-duplicates).
- ≤ 10 stages per recipe.
- Smoker normalization: `woodfire = false` or missing for `smoker` is stored as `true` silently (not an error).

Validation contract (Go, `internal/oven`):

```go
type Issue struct {
    Field  string         // stage field name, e.g. "temperature_c"
    Code   string         // "required" | "forbidden" | "out_of_range" | "invalid"
    Params map[string]any // e.g. {"min":230,"max":370,"mode":"max_roast"} for i18n interpolation
}
func Normalize(s *domain.OvenStage)                   // smoker rule, accessory sort/dedupe
func Validate(s domain.OvenStage) (errs, warns []Issue)
```

The service prefixes `Field` with the stage path (`oven_stages[1].temperature_c`). Errors block saving (form
re-renders with inline messages; API 422 with `invalid_params`, `40 §5`). Warnings use `out_of_range` against the
Recommended band; the editor shows them ("Unusual for Max Roast (230–370 °C)") and the API returns them in
`warnings[]` while still saving. Messages come from `Code` + `Params` through i18n; the package returns no human text.
Table-driven tests cover every cell of this table.

## 5. Derived and display values
- **Stage time** = `duration_seconds` + `duration2_seconds` (Gourmet Roast). **Total oven time** = sum of stage times;
  preheat excluded. Searchable ("≤ 30 min").
- **Uses woodfire** = any stage has `woodfire = true` → 🔥 badge and the "Smoked" filter (together with any `smoker` stage).
- **Primary mode** = mode of the stage with the longest stage time (ties → lowest position). Shown on cards.
- **Cook card** per stage: mode (with dial name), preset or temperature, time; for Gourmet Roast "Stage 1: 330 °C · 20 min →
  Stage 2: 140 °C · 1 h"; woodfire badge with blend and note ("Fill the smoke box"); accessories; derived frame level
  (shown prominently only for Broil: "Frame on the top level"); core temperature; "Preheat first (≈ N min)" for
  preheat modes; "Pellets ignite ≈ 3–4 min" for Smoker; for any temperature above 260 °C the hint "Above 260 °C: no
  butter or olive oil"; notes. Timers: `70 §5.2`.

## 6. Machine-readable spec
`GET /api/v1/meta/oven` returns §3–§4 as JSON. The editor embeds the same JSON as
`<script type="application/json" id="oven-spec">`. `internal/oven/spec.go` is the single source of truth.
