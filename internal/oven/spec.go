// Package oven is the single source of truth for the Ninja Woodfire OO101 settings model (spec 25).
package oven

import "cooking/internal/domain"

// Issue is the validation message type (code + params, no human text).
type Issue = domain.Issue

// Hard limits (25 §4). The only blocking numeric limits anywhere.
const (
	TempMin           = 40
	TempMax           = 370
	PreheatMinutesMax = 120
	CoreTempMin       = 30
	CoreTempMax       = 100
	OilWarningC       = 260
)

// Field kinds.
const (
	Required  = "required"
	Forbidden = "forbidden"
	ByPreset  = "by_preset"
	Optional  = "optional"
)

// Woodfire rules.
const (
	WoodAllowed   = "allowed"
	WoodForced    = "forced"
	WoodForbidden = "forbidden"
)

// Field describes the rule for one numeric field of a mode.
type Field struct {
	Kind        string  `json:"kind"`
	Accepted    *[2]int `json:"accepted,omitempty"`    // hard bounds, inclusive
	Recommended *[2]int `json:"recommended,omitempty"` // warn outside
}

// Mode is one row of the 25 §4 table.
type Mode struct {
	Key              string `json:"key"`
	Dial             string `json:"dial"`
	Preheat          bool   `json:"preheat"`
	FrameLevel       string `json:"frame_level"`
	Woodfire         string `json:"woodfire"`
	DefaultAccessory string `json:"default_accessory"`
	Temperature      Field  `json:"temperature"`
	Duration         Field  `json:"duration"`
	Temperature2     *Field `json:"temperature2"`
	Duration2        *Field `json:"duration2"`
}

// Limits is the "limits" object of the machine-readable spec.
type Limits struct {
	TempMin           int `json:"temp_min"`
	TempMax           int `json:"temp_max"`
	MaxStages         int `json:"max_stages"`
	PreheatMinutesMax int `json:"preheat_minutes_max"`
	CoreTempMin       int `json:"core_temp_min"`
	CoreTempMax       int `json:"core_temp_max"`
	PelletNoteMax     int `json:"pellet_note_max"`
	StageNotesMax     int `json:"stage_notes_max"`
	OilWarningC       int `json:"oil_warning_c"`
}

// Spec is the full machine-readable spec (GET /api/v1/meta/oven and #oven-spec).
type Spec struct {
	Limits       Limits   `json:"limits"`
	Modes        []Mode   `json:"modes"`
	PizzaPresets []string `json:"pizza_presets"`
	PelletBlends []string `json:"pellet_blends"`
	Accessories  []string `json:"accessories"`
}

func b(lo, hi int) *[2]int { return &[2]int{lo, hi} }

const (
	min = 60
	hr  = 3600
)

var tempAny = b(TempMin, TempMax)

var modes = []Mode{
	{Key: "pizza", Dial: "PIZZA", Preheat: true, FrameLevel: "bottom", Woodfire: WoodAllowed, DefaultAccessory: "pizza_stone",
		Temperature: Field{Kind: ByPreset, Accepted: tempAny, Recommended: b(175, 370)},
		Duration:    Field{Kind: Required, Accepted: b(30, 60*min), Recommended: b(60, 20*min)}},
	{Key: "gourmet_roast", Dial: "GOURMET ROAST", Preheat: true, FrameLevel: "bottom", Woodfire: WoodAllowed, DefaultAccessory: "pro_heat_pan",
		Temperature:  Field{Kind: Required, Accepted: tempAny, Recommended: b(200, 370)},
		Duration:     Field{Kind: Required, Accepted: b(1*min, 6*hr), Recommended: b(5*min, 30*min)},
		Temperature2: &Field{Kind: Required, Accepted: tempAny, Recommended: b(95, 250)},
		Duration2:    &Field{Kind: Required, Accepted: b(1*min, 6*hr), Recommended: b(10*min, 4*hr)}},
	{Key: "max_roast", Dial: "MAX ROAST", Preheat: true, FrameLevel: "bottom", Woodfire: WoodAllowed, DefaultAccessory: "pro_heat_pan",
		Temperature: Field{Kind: Required, Accepted: tempAny, Recommended: b(230, 370)},
		Duration:    Field{Kind: Required, Accepted: b(1*min, 4*hr), Recommended: b(5*min, 2*hr)}},
	{Key: "smoker", Dial: "SMOKER", Preheat: false, FrameLevel: "bottom", Woodfire: WoodForced, DefaultAccessory: "pro_heat_pan",
		Temperature: Field{Kind: Required, Accepted: tempAny, Recommended: b(60, 190)},
		Duration:    Field{Kind: Required, Accepted: b(1*min, 24*hr), Recommended: b(30*min, 12*hr)}},
	{Key: "bake", Dial: "BAKE", Preheat: true, FrameLevel: "bottom", Woodfire: WoodAllowed, DefaultAccessory: "pro_heat_pan",
		Temperature: Field{Kind: Required, Accepted: tempAny, Recommended: b(150, 370)},
		Duration:    Field{Kind: Required, Accepted: b(1*min, 6*hr), Recommended: b(5*min, 4*hr)}},
	{Key: "broil", Dial: "BROIL", Preheat: false, FrameLevel: "top", Woodfire: WoodForbidden, DefaultAccessory: "pro_heat_pan",
		Temperature: Field{Kind: Required, Accepted: tempAny, Recommended: b(260, 370)},
		Duration:    Field{Kind: Required, Accepted: b(30, 60*min), Recommended: b(1*min, 30*min)}},
	{Key: "dehydrate", Dial: "DEHYDRATE", Preheat: false, FrameLevel: "bottom", Woodfire: WoodAllowed, DefaultAccessory: "pro_heat_pan",
		Temperature: Field{Kind: Required, Accepted: tempAny, Recommended: b(40, 90)},
		Duration:    Field{Kind: Required, Accepted: b(10*min, 24*hr), Recommended: b(1*hr, 12*hr)}},
	{Key: "keep_warm", Dial: "KEEP WARM", Preheat: false, FrameLevel: "bottom", Woodfire: WoodForbidden, DefaultAccessory: "pro_heat_pan",
		Temperature: Field{Kind: Forbidden},
		Duration:    Field{Kind: Optional, Accepted: b(0, 12*hr), Recommended: b(0, 2*hr)}},
}

// Default returns the machine-readable spec built from the table.
func Default() Spec {
	ms := make([]Mode, len(modes))
	copy(ms, modes)
	return Spec{
		Limits: Limits{TempMin: TempMin, TempMax: TempMax, MaxStages: domain.MaxStages, PreheatMinutesMax: PreheatMinutesMax,
			CoreTempMin: CoreTempMin, CoreTempMax: CoreTempMax, PelletNoteMax: domain.MaxPelletNote,
			StageNotesMax: domain.MaxStageNotes, OilWarningC: OilWarningC},
		Modes:        ms,
		PizzaPresets: append([]string(nil), domain.PizzaPresets...),
		PelletBlends: append([]string(nil), domain.PelletBlends...),
		Accessories:  append([]string(nil), domain.Accessories...),
	}
}

// Lookup returns the table row of a mode.
func Lookup(mode string) (Mode, bool) {
	for _, m := range modes {
		if m.Key == mode {
			return m, true
		}
	}
	return Mode{}, false
}
