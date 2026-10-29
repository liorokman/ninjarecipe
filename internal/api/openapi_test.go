package api

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/liorokman/ninjarecipe/internal/i18n"
	"github.com/liorokman/ninjarecipe/internal/oven"
	"github.com/liorokman/ninjarecipe/locales"
)

func testAPI(t *testing.T) *API {
	t.Helper()
	cat, err := i18n.Load(locales.FS)
	if err != nil {
		t.Fatal(err)
	}
	return New(nil, cat, "https://r.example.com", 1, 60)
}

func loadDoc(t *testing.T) (paths map[string]map[string]json.RawMessage, schemas map[string]json.RawMessage) {
	t.Helper()
	var doc struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(openapiJSON, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Paths, doc.Components.Schemas
}

// Every authenticated route, plus the public meta/oven, is documented, and nothing else is (/mcp and openapi.json excluded, 40 §9.1).
func TestOpenAPICoversEveryRoute(t *testing.T) {
	a := testAPI(t)
	paths, _ := loadDoc(t)
	var have, want []string
	for p, ms := range paths {
		for m := range ms {
			have = append(have, strings.ToUpper(m)+" /api/v1"+p)
		}
	}
	for _, rt := range a.routes() {
		want = append(want, rt.Pattern)
	}
	want = append(want, "GET /api/v1/meta/oven")
	sort.Strings(have)
	sort.Strings(want)
	if strings.Join(have, "\n") != strings.Join(want, "\n") {
		t.Fatalf("openapi.json out of step with routes\n documented:\n  %s\n registered:\n  %s", strings.Join(have, "\n  "), strings.Join(want, "\n  "))
	}
}

// The enums in the schema are the oven model's (25 §2), so an assistant that follows the schema cannot send an unknown mode.
func TestOpenAPIEnumsMatchOvenModel(t *testing.T) {
	_, schemas := loadDoc(t)
	var st struct {
		Properties map[string]struct {
			Enum  []any `json:"enum"`
			Items struct {
				Enum []string `json:"enum"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schemas["OvenStage"], &st); err != nil {
		t.Fatal(err)
	}
	strs := func(in []any) (out []string) {
		for _, x := range in {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return
	}
	spec := oven.Default()
	var modes []string
	for _, m := range spec.Modes {
		modes = append(modes, m.Key)
	}
	for name, c := range map[string]struct{ got, want []string }{
		"mode":         {strs(st.Properties["mode"].Enum), modes},
		"pizza_preset": {strs(st.Properties["pizza_preset"].Enum), spec.PizzaPresets},
		"pellet_blend": {strs(st.Properties["pellet_blend"].Enum), spec.PelletBlends},
		"accessories":  {st.Properties["accessories"].Items.Enum, spec.Accessories},
	} {
		if strings.Join(c.got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: schema %v, oven model %v", name, c.got, c.want)
		}
	}
}

func compile(t *testing.T, name string) *jsonschema.Resolved {
	t.Helper()
	_, schemas := loadDoc(t)
	defs := map[string]json.RawMessage{}
	for k, v := range schemas {
		defs[k] = json.RawMessage(strings.ReplaceAll(string(v), "#/components/schemas/", "#/$defs/"))
	}
	root, _ := json.Marshal(map[string]any{"$ref": "#/$defs/" + name, "$defs": defs})
	var s jsonschema.Schema
	if err := json.Unmarshal(root, &s); err != nil {
		t.Fatal(err)
	}
	r, err := s.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve %s: %v", name, err)
	}
	return r
}

func validate(t *testing.T, r *jsonschema.Resolved, doc string) error {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		t.Fatalf("bad test JSON: %v", err)
	}
	return r.Validate(v)
}

// The request example of 40 §4.1 (comments removed) validates; typical mistakes do not.
func TestRecipeImportSchema(t *testing.T) {
	r := compile(t, "RecipeImport")
	good := `{
	  "title": "Smoked short ribs", "description": "…", "notes": "…", "language": "en", "servings": 4, "prep_minutes": 30,
	  "source_url": "https://example.com/ribs", "source_name": "Example Kitchen",
	  "categories": ["beef"], "tags": ["weekend", "brisket"],
	  "ingredients": [{"amount": 1.5, "unit": "kg", "item": "beef short ribs", "section": null}, {"text": "2 tbsp smoked paprika"}],
	  "steps": [{"text": "Rub the ribs.", "timer_seconds": null}],
	  "oven_stages": [{"mode": "smoker", "temperature_c": 120, "duration_seconds": 10800, "woodfire": true, "pellet_blend": "robust",
	                   "pellet_note": "hickory", "accessories": ["pro_heat_pan"], "target_core_temp_c": 93, "notes": "wrap at 75"},
	                  {"mode": "gourmet_roast", "temperature_c": 230, "duration_seconds": 600, "temperature2_c": 180, "duration2_seconds": 1200, "preheat_minutes": 10},
	                  {"mode": "pizza", "pizza_preset": "artisan", "temperature_c": 370, "duration_seconds": 90}],
	  "images": [{"url": "https://example.com/ribs.jpg"}, {"data_base64": "AAAA", "caption": "cover"}]
	}`
	if err := validate(t, r, good); err != nil {
		t.Fatalf("§4.1 example must validate: %v", err)
	}
	if err := validate(t, r, `{"title":"Only a title"}`); err != nil {
		t.Fatalf("title alone is a valid import: %v", err)
	}
	for name, bad := range map[string]string{
		"no title":         `{"description":"x"}`,
		"empty title":      `{"title":""}`,
		"unknown mode":     `{"title":"x","oven_stages":[{"mode":"top_heat"}]}`,
		"stage w/o mode":   `{"title":"x","oven_stages":[{"temperature_c":200}]}`,
		"unknown preset":   `{"title":"x","oven_stages":[{"mode":"pizza","pizza_preset":"deep_pan"}]}`,
		"unknown blend":    `{"title":"x","oven_stages":[{"mode":"smoker","pellet_blend":"mesquite"}]}`,
		"unknown language": `{"title":"x","language":"fr"}`,
		"step w/o text":    `{"title":"x","steps":[{"timer_seconds":5}]}`,
		"string temp":      `{"title":"x","oven_stages":[{"mode":"bake","temperature_c":"hot"}]}`,
		"long title":       `{"title":"` + strings.Repeat("x", 201) + `"}`,
	} {
		if err := validate(t, r, bad); err == nil {
			t.Errorf("%s must not validate", name)
		}
	}
	// numeric ranges are the server's business (25 §4), so the schema must not declare stricter limits than the oven model
	if err := validate(t, r, `{"title":"x","oven_stages":[{"mode":"bake","temperature_c":999,"duration_seconds":1}]}`); err != nil {
		t.Errorf("range violations are warnings/errors from oven.Validate, not from the schema: %v", err)
	}
}

// The 40 §4.2 response example validates against ImportResult and the problem examples against Problem.
func TestResponseSchemas(t *testing.T) {
	res := compile(t, "ImportResult")
	if err := validate(t, res, `{"id":"7b8e192a-3c48-4384-a140-5bbf2f0739c9","status":"inbox","title":"Smoked short ribs",
		"review_url":"https://recipes.example.com/inbox/7b8e192a-3c48-4384-a140-5bbf2f0739c9","created_at":"2026-10-27T13:30:00.000Z",
		"warnings":[{"name":"oven_stages[0].temperature_c","code":"out_of_range","params":{"min":60,"max":190}},{"name":"images[1]","code":"fetch_failed","params":{"status":404}}]}`); err != nil {
		t.Fatalf("ImportResult: %v", err)
	}
	prob := compile(t, "Problem")
	for _, p := range []string{
		`{"type":"https://r/problems/previously-reviewed","title":"Previously reviewed","status":409,"outcome":"rejected","decided_at":"2026-10-29T18:02:11.000Z","reason":"too_complicated"}`,
		`{"type":"https://r/problems/previously-reviewed","title":"Previously reviewed","status":409,"outcome":"kept","decided_at":"2026-10-29T18:02:11.000Z","reason":null}`,
		`{"type":"https://r/problems/duplicate","title":"Duplicate","status":409,"existing_id":"x"}`,
	} {
		if err := validate(t, prob, p); err != nil {
			t.Errorf("Problem %s: %v", p, err)
		}
	}
	out := compile(t, "OutcomePage")
	if err := validate(t, out, `{"items":[{"cursor":"42","item_id":"7b8e192a-3c48-4384-a140-5bbf2f0739c9","recipe_id":null,"title":"Smoked short ribs",
		"source_url":"https://example.com/ribs","outcome":"rejected","reason":"too_complicated","note":"Two days of prep","edited":false,"shared":false,
		"decided_at":"2026-10-29T18:02:11.000Z"}],"next_cursor":"42"}`); err != nil {
		t.Fatalf("OutcomePage (40 §7 example): %v", err)
	}
	if err := validate(t, compile(t, "AssistantBrief"), `{"text":"More smoked fish.","language":"he","updated_at":"2026-10-29T17:40:00.000Z"}`); err != nil {
		t.Fatalf("AssistantBrief (40 §8 example): %v", err)
	}
}
