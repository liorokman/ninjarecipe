# 25 — Ninja Woodfire OO101 Oven Settings Model

Owner: Architect. Status: authoritative for implementation. Referenced by `30-data-model.md` (table `oven_stages`) and `40-api.md` (`OvenStage` schema).

The app **does not control the oven**. It records *what the cook should dial in*. So validation exists to catch typos and nonsense, and to warn when a value looks wrong for the oven. It must never stop someone saving a recipe that works on their oven.

## 1. Research summary (Ninja Woodfire Outdoor Oven OO101UK, "Artisan Pizza Maker & BBQ Smoker")

Sources: sharkninja.co.uk / ninjakitchen.ie product pages, the OO101UK instruction manual (manuals.plus, device.report mirrors), and retailer spec sheets (Stoves Are Us, Bing Lee, Appliances Online). Some sources contradict each other, especially on ranges for the UK model compared with the US model (°F). The confidence column below reflects that.

| Fact | Value | Confidence |
|---|---|---|
| Functions (left dial), "8-in-1" | Pizza, Max Roast, Specialty Roast (called "Gourmet Roast" in some markets), Top Heat (called "Broil" in the US), Bake, Smoker, Dehydrate, Keep Warm | High |
| Overall temperature range | 40 °C – 370 °C (105 °F – 700 °F) | High |
| Pizza presets (right dial) | Artisan (Neapolitan), Thin, Pan, New York, Frozen, Custom. Some firmware/market variants also list Calzone | High (6 presets); Low (Calzone) |
| Pizza preset temperatures | Fixed by the oven. You can only set the temperature with the **Custom** preset | Medium-High |
| Pizza timer | Countdown in minutes:seconds. Artisan defaults to about 3 min. The oven stays hot (about 1 h) for back-to-back pizzas | Medium |
| Woodfire Flavour | Optional pellet smoke. It **cannot** be used with Top Heat or Keep Warm. It is **always on** for Smoker | High |
| Pellets | 1 level scoop (about ½ cup / about 45 g) of Ninja Woodfire pellets per cook. Blends: All-Purpose, Robust (other official blends exist) | High |
| Preheat | Pizza, Max Roast, Specialty Roast, Bake and Smoker preheat ("ADD FOOD" when ready). Top Heat, Dehydrate and Keep Warm have no preheat | Medium-High |
| Specialty Roast | The oven runs it as an automatic two-phase cycle (hot sear, then lower roast). The user still sets one temperature and one time | Medium |
| Accessories (OO101 box) | Pizza stone (about 30 cm / 12"), Pro-Heat Pan, Roast Rack (sits in the pan), Accessory Frame, Pellet Scoop, starter pellets | High |
| Accessory frame levels | The frame can sit low or high. Top Heat uses the **top** level | Medium |
| Food probe | Not built into the OO101 (it is on OO150-series models). A target core temperature is still useful for an external thermometer | Medium |

### Per-mode ranges: evidence

| Mode | US manual (°F → °C) | Other UK claims | Default/typical |
|---|---|---|---|
| Pizza (Custom) | 350–700 °F → 175–370 °C | up to 370 °C | Artisan ~370 °C, ~3 min |
| Max Roast | 450–700 °F → 230–370 °C | 120–370 °C (one source) | ~260 °C |
| Specialty Roast | 200–700 °F → 95–370 °C | 120–240 °C (one source) | ~180–200 °C |
| Top Heat | 500–700 °F → 260–370 °C | "Hi/Lo" (one source) | max |
| Bake | 300–700 °F → 150–370 °C | 40–210 °C (one source, doubtful) | ~180 °C |
| Smoker | 145–375 °F → 63–190 °C | 120–180 °C (one source) | ~120 °C |
| Dehydrate | 105–195 °F → 40–90 °C | 25–90 °C (one source) | ~65 °C |
| Keep Warm | fixed / not user-set | 60–90 °C (one source) | — |

Time ranges are the **least certain** part. Smoker and Dehydrate allow up to about **12 h**. Roast/Bake allow up to about **2–4 h**. Top Heat allows up to about **30 min**. Pizza allows minutes.

> Uncertainty policy: there are two bands per field. **Recommended** (soft) is the best evidence; a value outside it gets a non-blocking warning. **Accepted** (hard) is wide enough to cover every credible source; a value outside it is rejected. Both live in one Go table (`internal/oven/spec.go`) so they are easy to change after the owner checks the printed manual (see open questions).

## 2. Settings model (per oven stage)

A recipe has **0..N ordered oven stages**. Example: Smoker 120 °C for 3 h, then Max Roast 260 °C for 10 min. Each stage has the fields below.

| Field | Type | Required | Rules |
|---|---|---|---|
| `position` | int ≥ 1 | yes | Order of the stage. Unique per recipe. Set by the server from array order |
| `mode` | enum `OvenMode` | yes | See §3 |
| `pizza_preset` | enum `PizzaPreset` | yes if mode=`pizza`, otherwise must be null | |
| `temperature_c` | int | depends on mode | See §4. Integer °C. Not rounded by the server. UI step is 5 |
| `top_heat_level` | enum `hi`/`lo` | optional, only when mode=`top_heat` | Hedges the "Hi/Lo" uncertainty. Can be used with, or instead of, `temperature_c` |
| `duration_seconds` | int | yes, except `keep_warm` (optional) | See §4. Stored in seconds so pizza times like 1:30 work. UI shows `m:ss` below 1 h and `h:mm` from 1 h |
| `woodfire` | bool | yes (default false) | Forced `true` for `smoker`. Must be `false` for `top_heat` and `keep_warm` (rejected if true) |
| `pellet_blend` | enum `PelletBlend` or null | optional, only if `woodfire=true` | `all_purpose`, `robust`, `other` |
| `pellet_scoops` | int 1–3 or null | optional, only if `woodfire=true` | Default shown is 1. More than 1 means a refill during a long smoke (recommended max 2) |
| `accessories` | set of enum `Accessory` | optional | `pizza_stone`, `pro_heat_pan`, `roast_rack`, `accessory_frame` |
| `frame_level` | enum `low`/`high` or null | optional | Only meaningful with `accessory_frame`. UI suggests `high` for `top_heat` |
| `preheat` | bool | derived, not stored | True for pizza, max_roast, specialty_roast, bake, smoker. The UI shows "Preheat, then add food" |
| `target_core_temp_c` | int 30–100 or null | optional | For an external thermometer (smoking/roasting) |
| `notes` | text ≤ 500 chars | optional | Free text, e.g. "flip halfway", "add cheese for last 2 min" |

## 3. Enumerations (stable string keys; UI labels come from i18n `oven.mode.<key>` etc.)

```
OvenMode     = pizza | max_roast | specialty_roast | top_heat | bake | smoker | dehydrate | keep_warm
PizzaPreset  = artisan | thin | pan | new_york | frozen | custom
PelletBlend  = all_purpose | robust | other
Accessory    = pizza_stone | pro_heat_pan | roast_rack | accessory_frame
FrameLevel   = low | high
TopHeatLevel = hi | lo
```

English labels: Pizza, Max Roast, Specialty Roast, Top Heat, Bake, Smoker, Dehydrate, Keep Warm; Artisan, Thin, Pan, New York, Frozen, Custom; All-Purpose, Robust, Other; Pizza Stone, Pro-Heat Pan, Roast Rack, Accessory Frame. The Hebrew labels belong to the UI spec. Keep the English mode name next to the Hebrew one (for example "צלייה מקסימלית (Max Roast)") so it matches the oven's dial printing.

## 4. Validation table

`temperature_c` is required when the column says "required", must be null when it says "—", and may be null when it says "optional". Durations are in seconds. Hard limits are inclusive.

| Mode | temperature_c | Recommended °C | Accepted °C | Recommended duration | Accepted duration | woodfire | Preheat |
|---|---|---|---|---|---|---|---|
| `pizza` + preset ≠ custom | optional (preset is fixed; record it only if known) | 175–370 | 90–370 | 60 s – 20 min | 30 s – 60 min | allowed | yes |
| `pizza` + `custom` | **required** | 175–370 | 90–370 | 60 s – 20 min | 30 s – 60 min | allowed | yes |
| `max_roast` | **required** | 230–370 | 120–370 | 5 min – 2 h | 1 min – 4 h | allowed | yes |
| `specialty_roast` | **required** | 95–370 | 90–370 | 10 min – 4 h | 1 min – 6 h | allowed | yes |
| `top_heat` | optional (or `top_heat_level`) | 260–370 | 200–370 | 1 – 30 min | 30 s – 60 min | **forbidden** | no |
| `bake` | **required** | 150–370 | 90–370 | 5 min – 4 h | 1 min – 6 h | allowed | yes |
| `smoker` | **required** | 60–190 | 50–200 | 10 min – 12 h | 1 min – 24 h | **forced true** | yes |
| `dehydrate` | **required** | 40–90 | 25–100 | 1 h – 12 h | 10 min – 24 h | allowed | no |
| `keep_warm` | — (must be null; fixed by oven) | — | — | ≤ 2 h | 0 – 12 h (optional) | **forbidden** | no |

Cross-field rules (all hard; error code `invalid`):
- `pizza_preset` is required if and only if `mode = pizza`.
- `top_heat_level` is only allowed for `top_heat`.
- `pellet_blend` and `pellet_scoops` are only allowed when `woodfire = true`.
- `frame_level` is only allowed when `accessories` contains `accessory_frame`.
- A recipe can have at most 10 stages.
- When a client sends `woodfire=false` (or leaves it out) for `smoker`, the server stores `true` silently. This is a normalization, not an error.

Validation result shape (Go): `oven.Validate(stage) (errs []FieldError, warns []FieldWarning)`. Errors block saving: the HTML form re-renders, and the API returns 422. Warnings are shown in the editor ("Outside the usual range for Max Roast (230–370 °C)"). The API returns them in `warnings[]` and still saves.

## 5. Derived/display values
- **Total oven time** = sum of `duration_seconds` over all stages. Preheat is not included. Search can filter on it ("≤ 30 min").
- **Uses woodfire** = any stage has `woodfire = true`. This drives a recipe-level 🔥 badge and a search filter.
- **Primary mode** = mode of the stage with the longest duration (ties go to the lowest position). Recipe cards show it.
- Cook view shows each stage as a large card with the mode (with the dial name), temperature (or preset), time, a woodfire/pellet badge, accessories, frame level, core temperature, "Preheat first" when applicable, and notes.

## 6. Machine-readable spec endpoint
`GET /api/v1/meta/oven` returns this table as JSON (see `40-api.md`). The UI editor gets the same data embedded as `<script type="application/json" id="oven-spec">`, so the client-side hints and the server validation can never disagree. The Go table `internal/oven/spec.go` is the single source of truth.
