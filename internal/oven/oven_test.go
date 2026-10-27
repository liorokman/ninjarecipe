package oven

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"

	"cooking/internal/domain"
)

func ip(i int) *int { return &i }

func codes(is []Issue) map[string]string {
	m := map[string]string{}
	for _, x := range is {
		m[x.Field] = x.Code
	}
	return m
}

func has(is []Issue, field, code string) bool { return codes(is)[field] == code }

// row of 25 §4: every mode with its recommended / accepted bands.
type row struct {
	mode        string
	preset      string
	tempKind    string // required | forbidden
	recT        [2]int
	accT        [2]int
	recD, accD  [2]int
	durOptional bool
	woodfire    string
	preheat     bool
}

var table = []row{
	{"pizza", "custom", "required", [2]int{175, 370}, [2]int{40, 370}, [2]int{60, 1200}, [2]int{30, 3600}, false, "allowed", true},
	{"pizza", "artisan", "forbidden", [2]int{}, [2]int{}, [2]int{60, 1200}, [2]int{30, 3600}, false, "allowed", true},
	{"max_roast", "", "required", [2]int{230, 370}, [2]int{40, 370}, [2]int{300, 7200}, [2]int{60, 14400}, false, "allowed", true},
	{"smoker", "", "required", [2]int{60, 190}, [2]int{40, 370}, [2]int{1800, 43200}, [2]int{60, 86400}, false, "forced", false},
	{"bake", "", "required", [2]int{150, 370}, [2]int{40, 370}, [2]int{300, 14400}, [2]int{60, 21600}, false, "allowed", true},
	{"broil", "", "required", [2]int{260, 370}, [2]int{40, 370}, [2]int{60, 1800}, [2]int{30, 3600}, false, "forbidden", false},
	{"dehydrate", "", "required", [2]int{40, 90}, [2]int{40, 370}, [2]int{3600, 43200}, [2]int{600, 86400}, false, "allowed", false},
	{"keep_warm", "", "forbidden", [2]int{}, [2]int{}, [2]int{0, 7200}, [2]int{0, 43200}, true, "forbidden", false},
}

func stageFor(r row, t, d *int) domain.OvenStage {
	return domain.OvenStage{Mode: r.mode, PizzaPreset: r.preset, TemperatureC: t, DurationSeconds: d, Woodfire: r.woodfire == "forced"}
}

func TestEveryCellOfTheTable(t *testing.T) {
	for _, r := range table {
		r := r
		t.Run(r.mode+"/"+r.preset, func(t *testing.T) {
			okT, okD := ip(r.recT[0]), ip(r.recD[0])
			if r.tempKind == "forbidden" {
				okT = nil
			}
			// valid baseline
			e, w := Validate(stageFor(r, okT, okD))
			if len(e) != 0 || len(w) != 0 {
				t.Fatalf("baseline: errs=%v warns=%v", e, w)
			}
			if r.tempKind == "required" {
				// missing -> required
				e, _ := Validate(stageFor(r, nil, okD))
				if !has(e, "temperature_c", "required") {
					t.Fatalf("temp required: %v", e)
				}
				// hard bounds inclusive; just outside -> out_of_range error
				for _, v := range []int{r.accT[0], r.accT[1]} {
					e, _ := Validate(stageFor(r, ip(v), okD))
					if has(e, "temperature_c", "out_of_range") {
						t.Fatalf("temp %d must be accepted", v)
					}
				}
				for _, v := range []int{r.accT[0] - 1, r.accT[1] + 1} {
					e, _ := Validate(stageFor(r, ip(v), okD))
					if !has(e, "temperature_c", "out_of_range") {
						t.Fatalf("temp %d must be rejected: %v", v, e)
					}
				}
				// soft bounds: inclusive edges quiet, outside warns without error
				for _, v := range []int{r.recT[0], r.recT[1]} {
					e, w := Validate(stageFor(r, ip(v), okD))
					if len(e) != 0 || has(w, "temperature_c", "out_of_range") {
						t.Fatalf("temp %d edge of recommended: %v %v", v, e, w)
					}
				}
				for _, v := range []int{r.recT[0] - 1, r.recT[1] + 1} {
					if v < r.accT[0] || v > r.accT[1] {
						continue
					}
					e, w := Validate(stageFor(r, ip(v), okD))
					if len(e) != 0 || !has(w, "temperature_c", "out_of_range") {
						t.Fatalf("temp %d outside recommended must warn only: %v %v", v, e, w)
					}
					p := w[0].Params
					if p["min"] != r.recT[0] || p["max"] != r.recT[1] || p["mode"] != r.mode || p["unit"] != "c" {
						t.Fatalf("warning params: %v", p)
					}
				}
			} else {
				e, _ := Validate(stageFor(r, ip(200), okD))
				if !has(e, "temperature_c", "forbidden") {
					t.Fatalf("temp must be forbidden: %v", e)
				}
			}
			// durations
			if !r.durOptional {
				e, _ := Validate(stageFor(r, okT, nil))
				if !has(e, "duration_seconds", "required") {
					t.Fatalf("duration required: %v", e)
				}
			} else {
				if e, w := Validate(stageFor(r, okT, nil)); len(e) != 0 || len(w) != 0 {
					t.Fatalf("keep_warm duration optional: %v %v", e, w)
				}
			}
			for _, v := range []int{r.accD[0], r.accD[1]} {
				e, _ := Validate(stageFor(r, okT, ip(v)))
				if has(e, "duration_seconds", "out_of_range") {
					t.Fatalf("duration %d must be accepted", v)
				}
			}
			for _, v := range []int{r.accD[0] - 1, r.accD[1] + 1} {
				if v < 0 {
					continue
				}
				e, _ := Validate(stageFor(r, okT, ip(v)))
				if !has(e, "duration_seconds", "out_of_range") {
					t.Fatalf("duration %d must be rejected: %v", v, e)
				}
			}
			for _, v := range []int{r.recD[0] - 1, r.recD[1] + 1} {
				if v < r.accD[0] || v > r.accD[1] {
					continue
				}
				e, w := Validate(stageFor(r, okT, ip(v)))
				if len(e) != 0 || !has(w, "duration_seconds", "out_of_range") {
					t.Fatalf("duration %d outside recommended must warn only: %v %v", v, e, w)
				}
			}
			// woodfire
			s := stageFor(r, okT, okD)
			switch r.woodfire {
			case "forbidden":
				s.Woodfire = true
				if e, _ := Validate(s); !has(e, "woodfire", "forbidden") {
					t.Fatalf("woodfire must be forbidden: %v", e)
				}
			case "forced":
				s.Woodfire = false
				Normalize(&s)
				if !s.Woodfire {
					t.Fatal("smoker must be normalized to woodfire")
				}
			default:
				s.Woodfire = true
				if e, _ := Validate(s); len(e) != 0 {
					t.Fatalf("woodfire allowed: %v", e)
				}
			}
			// preheat_minutes only for preheat modes
			s = stageFor(r, okT, okD)
			s.PreheatMinutes = ip(15)
			e, _ = Validate(s)
			if r.preheat == has(e, "preheat_minutes", "forbidden") {
				t.Fatalf("preheat rule (preheat=%v): %v", r.preheat, e)
			}
			if Preheat(r.mode) != r.preheat {
				t.Fatalf("Preheat(%s)", r.mode)
			}
			// frame level
			want := "bottom"
			if r.mode == "broil" {
				want = "top"
			}
			if FrameLevel(r.mode) != want {
				t.Fatalf("frame level of %s", r.mode)
			}
		})
	}
}

func TestPizzaPresetRules(t *testing.T) {
	d := ip(150)
	if e, _ := Validate(domain.OvenStage{Mode: "pizza", DurationSeconds: d}); !has(e, "pizza_preset", "required") {
		t.Fatalf("preset required: %v", e)
	}
	if e, _ := Validate(domain.OvenStage{Mode: "pizza", PizzaPreset: "frozen", DurationSeconds: d}); !has(e, "pizza_preset", "invalid") {
		t.Fatalf("frozen is gone: %v", e)
	}
	if e, _ := Validate(domain.OvenStage{Mode: "pizza", PizzaPreset: "custom", DurationSeconds: d}); !has(e, "temperature_c", "required") {
		t.Fatalf("custom needs temp: %v", e)
	}
	for _, p := range []string{"artisan", "thin_crust", "new_york", "chicago", "calzone"} {
		if e, w := Validate(domain.OvenStage{Mode: "pizza", PizzaPreset: p, DurationSeconds: d}); len(e)+len(w) != 0 {
			t.Fatalf("%s: %v %v", p, e, w)
		}
	}
	if e, _ := Validate(domain.OvenStage{Mode: "bake", PizzaPreset: "artisan", TemperatureC: ip(200), DurationSeconds: d}); !has(e, "pizza_preset", "forbidden") {
		t.Fatalf("preset only for pizza: %v", e)
	}
}

func TestGourmetRoastTwoPhase(t *testing.T) {
	s := domain.OvenStage{Mode: "gourmet_roast", TemperatureC: ip(330), DurationSeconds: ip(1200), Temperature2C: ip(140), Duration2Seconds: ip(3600)}
	if e, w := Validate(s); len(e)+len(w) != 0 {
		t.Fatalf("valid example: %v %v", e, w)
	}
	if StageSeconds(s) != 4800 {
		t.Fatal("stage time includes phase 2")
	}
	for _, f := range []string{"temperature2_c", "duration2_seconds"} {
		x := s
		if f == "temperature2_c" {
			x.Temperature2C = nil
		} else {
			x.Duration2Seconds = nil
		}
		if e, _ := Validate(x); !has(e, f, "required") {
			t.Fatalf("%s required: %v", f, e)
		}
	}
	x := s
	x.Temperature2C = ip(260) // above stage-2 recommended 95-250 -> warn
	if e, w := Validate(x); len(e) != 0 || !has(w, "temperature2_c", "out_of_range") {
		t.Fatalf("stage 2 band: %v %v", e, w)
	}
	x = s
	x.Temperature2C, x.Duration2Seconds = ip(371), ip(59)
	e, _ := Validate(x)
	if !has(e, "temperature2_c", "out_of_range") || !has(e, "duration2_seconds", "out_of_range") {
		t.Fatalf("stage 2 hard: %v", e)
	}
	// phase 2 fields on other modes are forbidden
	y := domain.OvenStage{Mode: "bake", TemperatureC: ip(200), DurationSeconds: ip(600), Temperature2C: ip(100), Duration2Seconds: ip(60)}
	if e, _ := Validate(y); !has(e, "temperature2_c", "forbidden") || !has(e, "duration2_seconds", "forbidden") {
		t.Fatalf("phase 2 forbidden elsewhere: %v", e)
	}
}

func TestCrossFieldRules(t *testing.T) {
	base := domain.OvenStage{Mode: "bake", TemperatureC: ip(200), DurationSeconds: ip(600)}
	s := base
	s.PelletBlend, s.PelletNote = "robust", "hickory"
	e, _ := Validate(s)
	if !has(e, "pellet_blend", "forbidden") || !has(e, "pellet_note", "forbidden") {
		t.Fatalf("pellets need woodfire: %v", e)
	}
	s.Woodfire = true
	if e, _ := Validate(s); len(e) != 0 {
		t.Fatalf("pellets with woodfire: %v", e)
	}
	s.PelletBlend = "mesquite"
	if e, _ := Validate(s); !has(e, "pellet_blend", "invalid") {
		t.Fatalf("blend enum: %v", e)
	}
	s = base
	s.Woodfire, s.PelletNote = true, string(make([]rune, 61))
	for i := range []rune(s.PelletNote) {
		_ = i
	}
	s.PelletNote = ""
	for i := 0; i < 61; i++ {
		s.PelletNote += "x"
	}
	if e, _ := Validate(s); !has(e, "pellet_note", "invalid") {
		t.Fatalf("pellet note 60: %v", e)
	}
	// smoker normalization
	sm := domain.OvenStage{Mode: "smoker", TemperatureC: ip(120), DurationSeconds: ip(3600), PelletBlend: "robust"}
	Normalize(&sm)
	if e, _ := Validate(sm); len(e) != 0 || !sm.Woodfire {
		t.Fatalf("smoker pellets: %v", e)
	}
	// preheat range, core temp, accessories, notes
	s = base
	s.PreheatMinutes = ip(121)
	if e, _ := Validate(s); !has(e, "preheat_minutes", "out_of_range") {
		t.Fatalf("preheat max: %v", e)
	}
	s.PreheatMinutes = ip(120)
	if e, _ := Validate(s); len(e) != 0 {
		t.Fatalf("preheat 120: %v", e)
	}
	for _, c := range []int{29, 101} {
		s = base
		s.TargetCoreTempC = ip(c)
		if e, _ := Validate(s); !has(e, "target_core_temp_c", "out_of_range") {
			t.Fatalf("core %d: %v", c, e)
		}
	}
	s = base
	s.Accessories = []string{"toaster"}
	if e, _ := Validate(s); !has(e, "accessories", "invalid") {
		t.Fatalf("accessory enum: %v", e)
	}
	s = base
	s.Accessories = []string{"roast_rack", "pro_heat_pan", "roast_rack"}
	Normalize(&s)
	if !reflect.DeepEqual(s.Accessories, []string{"pro_heat_pan", "roast_rack"}) {
		t.Fatalf("accessories sorted/deduped: %v", s.Accessories)
	}
	if e, _ := Validate(domain.OvenStage{Mode: "toaster"}); !has(e, "mode", "invalid") {
		t.Fatal("mode enum")
	}
	if e, _ := Validate(domain.OvenStage{}); !has(e, "mode", "required") {
		t.Fatal("mode required")
	}
	// keep warm: duration optional, 0..12 h
	kw := domain.OvenStage{Mode: "keep_warm", DurationSeconds: ip(12*3600 + 1)}
	if e, _ := Validate(kw); !has(e, "duration_seconds", "out_of_range") {
		t.Fatalf("keep warm hard max: %v", e)
	}
}

func TestStagesLimitAndPrefix(t *testing.T) {
	var ss []domain.OvenStage
	for i := 0; i < 11; i++ {
		ss = append(ss, domain.OvenStage{Mode: "bake", TemperatureC: ip(200), DurationSeconds: ip(600)})
	}
	e, _ := ValidateStages(ss, "oven_stages")
	if !has(e, "oven_stages", "too_many") {
		t.Fatalf("limit 10: %v", e)
	}
	ss = ss[:2]
	ss[1].TemperatureC = ip(5)
	e, _ = ValidateStages(ss, "oven_stages")
	if !has(e, "oven_stages[1].temperature_c", "out_of_range") {
		t.Fatalf("prefix: %v", e)
	}
}

func TestDerivedValues(t *testing.T) {
	stages := []domain.OvenStage{
		{Position: 1, Mode: "smoker", TemperatureC: ip(120), DurationSeconds: ip(3 * 3600), Woodfire: true},
		{Position: 2, Mode: "max_roast", TemperatureC: ip(250), DurationSeconds: ip(600)},
		{Position: 3, Mode: "gourmet_roast", TemperatureC: ip(330), DurationSeconds: ip(1200), Temperature2C: ip(140), Duration2Seconds: ip(3600)},
	}
	if TotalSeconds(stages) != 3*3600+600+4800 {
		t.Fatalf("total %d", TotalSeconds(stages))
	}
	if PrimaryMode(stages) != "smoker" {
		t.Fatal("primary")
	}
	// tie -> lowest position
	tie := []domain.OvenStage{{Mode: "bake", DurationSeconds: ip(600)}, {Mode: "broil", DurationSeconds: ip(600)}}
	if PrimaryMode(tie) != "bake" {
		t.Fatal("tie-break")
	}
	if PrimaryMode(nil) != "" || PrimaryStage(nil) != nil {
		t.Fatal("empty")
	}
	if !UsesWoodfire(stages) || UsesWoodfire(stages[1:2]) {
		t.Fatal("woodfire")
	}
	if !UsesWoodfire([]domain.OvenStage{{Mode: "smoker"}}) {
		t.Fatal("smoker stage counts as woodfire even if flag unset")
	}
	if MaxTemp(stages[2]) != 330 {
		t.Fatal("max temp")
	}
}

func TestSpecJSONMatchesTable(t *testing.T) {
	b, err := json.Marshal(Default())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	lim := got["limits"].(map[string]any)
	if lim["temp_min"] != 40.0 || lim["temp_max"] != 370.0 || lim["max_stages"] != 10.0 || lim["oil_warning_c"] != 260.0 || lim["pellet_note_max"] != 60.0 {
		t.Fatalf("limits: %v", lim)
	}
	modes := got["modes"].([]any)
	if len(modes) != 8 {
		t.Fatalf("modes: %d", len(modes))
	}
	var keys []string
	for _, m := range modes {
		mm := m.(map[string]any)
		keys = append(keys, mm["key"].(string))
		tk := mm["temperature"].(map[string]any)["kind"]
		switch mm["key"] {
		case "pizza":
			if tk != "by_preset" {
				t.Fatal("pizza temperature kind")
			}
		case "keep_warm":
			if tk != "forbidden" || mm["duration"].(map[string]any)["kind"] != "optional" {
				t.Fatal("keep warm kinds")
			}
		case "gourmet_roast":
			if mm["temperature2"] == nil || mm["duration2"] == nil {
				t.Fatal("gourmet phase 2")
			}
		default:
			if mm["temperature2"] != nil {
				t.Fatal("phase 2 only for gourmet")
			}
		}
		if mm["key"] != "keep_warm" {
			acc := mm["temperature"].(map[string]any)["accepted"].([]any)
			if acc[0] != 40.0 || acc[1] != 370.0 {
				t.Fatalf("%v accepted %v", mm["key"], acc)
			}
		}
	}
	want := []string{"pizza", "gourmet_roast", "max_roast", "smoker", "bake", "broil", "dehydrate", "keep_warm"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("mode order %v", keys)
	}
	if !reflect.DeepEqual(got["pizza_presets"], []any{"artisan", "thin_crust", "new_york", "chicago", "calzone", "custom"}) {
		t.Fatal("presets")
	}
	if !reflect.DeepEqual(got["accessories"], []any{"pizza_stone", "pro_heat_pan", "roast_rack", "own_pan"}) {
		t.Fatal("accessories")
	}
	// golden file: any change to the table must be deliberate
	golden, err := os.ReadFile("testdata/spec.golden.json")
	if err != nil {
		if os.Getenv("UPDATE_GOLDEN") == "1" {
			pretty, _ := json.MarshalIndent(Default(), "", "  ")
			_ = os.MkdirAll("testdata", 0o755)
			_ = os.WriteFile("testdata/spec.golden.json", append(pretty, '\n'), 0o644)
			return
		}
		t.Fatal(err)
	}
	pretty, _ := json.MarshalIndent(Default(), "", "  ")
	if string(golden) != string(pretty)+"\n" {
		t.Fatal("spec JSON differs from testdata/spec.golden.json (UPDATE_GOLDEN=1 to refresh)")
	}
	_ = sort.Strings
}
