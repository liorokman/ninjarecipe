package web

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/liorokman/ninjarecipe/internal/domain"
	"github.com/liorokman/ninjarecipe/internal/recipetext"
)

func itoa(p *int) string {
	if p == nil {
		return ""
	}
	return strconv.Itoa(*p)
}

// StageToForm converts a stage into its form representation.
func StageToForm(s domain.OvenStage, idx string) StageForm {
	return StageForm{Idx: idx, Mode: s.Mode, PizzaPreset: s.PizzaPreset, TemperatureC: itoa(s.TemperatureC),
		DurationSeconds: itoa(s.DurationSeconds), Temperature2C: itoa(s.Temperature2C), Duration2Seconds: itoa(s.Duration2Seconds),
		PreheatMinutes: itoa(s.PreheatMinutes), Woodfire: s.Woodfire || s.Mode == domain.ModeSmoker, PelletBlend: s.PelletBlend,
		PelletNote: s.PelletNote, Accessories: append([]string{}, s.Accessories...), TargetCoreTempC: itoa(s.TargetCoreTempC), Notes: s.Notes}
}

// FormFromRecipe prefills the editor form.
func FormFromRecipe(r *domain.Recipe) RecipeForm {
	f := RecipeForm{Version: r.Version, Title: r.Title, Description: r.Description, Notes: r.Notes, Language: r.Language,
		Servings: itoa(r.Servings), PrepMinutes: itoa(r.PrepMinutes), SourceURL: r.SourceURL, SourceName: r.SourceName,
		Ingredients: recipetext.FormatIngredients(r.Ingredients), Steps: recipetext.FormatSteps(r.Steps), Visibility: r.Visibility}
	var tags []string
	for _, l := range r.Labels {
		if l.Type == "category" {
			f.CategorySlugs = append(f.CategorySlugs, l.Slug)
		} else {
			tags = append(tags, l.Name)
		}
	}
	f.Tags = strings.Join(tags, ", ")
	for i, s := range r.Stages {
		f.Stages = append(f.Stages, StageToForm(s, strconv.Itoa(i)))
	}
	return f
}

// ParseRecipeForm reads the recipe form contract (4.3) from a parsed request.
func ParseRecipeForm(r *http.Request) RecipeForm {
	v := r.PostForm
	f := RecipeForm{Title: strings.TrimSpace(v.Get("title")), Description: strings.TrimSpace(v.Get("description")),
		Notes: strings.TrimSpace(v.Get("notes")), Language: v.Get("language"), Servings: strings.TrimSpace(v.Get("servings")),
		PrepMinutes: strings.TrimSpace(v.Get("prep_minutes")), SourceURL: strings.TrimSpace(v.Get("source_url")),
		SourceName: strings.TrimSpace(v.Get("source_name")), CategorySlugs: v["category"], Tags: v.Get("tags"),
		Ingredients: v.Get("ingredients"), Steps: v.Get("steps"), Visibility: v.Get("visibility")}
	f.Version, _ = strconv.Atoi(v.Get("version"))
	if f.Language == "" {
		f.Language = domain.LangUnd
	}
	// stage[N].field -> contiguous, re-sorted by N
	idx := map[int]bool{}
	for k := range v {
		if strings.HasPrefix(k, "stage[") {
			if e := strings.Index(k, "]"); e > 6 {
				if n, err := strconv.Atoi(k[6:e]); err == nil && n >= 0 && n < 1000 {
					idx[n] = true
				}
			}
		}
	}
	var ns []int
	for n := range idx {
		ns = append(ns, n)
	}
	sort.Ints(ns)
	for _, n := range ns {
		p := fmt.Sprintf("stage[%d].", n)
		g := func(s string) string { return strings.TrimSpace(v.Get(p + s)) }
		f.Stages = append(f.Stages, StageForm{Idx: strconv.Itoa(len(f.Stages)), Mode: g("mode"), PizzaPreset: g("pizza_preset"),
			TemperatureC: g("temperature_c"), DurationSeconds: g("duration_seconds"), Temperature2C: g("temperature2_c"),
			Duration2Seconds: g("duration2_seconds"), PreheatMinutes: g("preheat_minutes"), Woodfire: g("woodfire") == "1",
			PelletBlend: g("pellet_blend"), PelletNote: g("pellet_note"), Accessories: v[p+"accessories"],
			TargetCoreTempC: g("target_core_temp_c"), Notes: g("notes")})
	}
	return f
}

type numParser struct{ issues []domain.Issue }

func (p *numParser) opt(field, s string) *int {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		p.issues = append(p.issues, domain.Issue{Field: field, Code: "invalid"})
		return nil
	}
	return &n
}

// ToInput converts the form into the service write model. Numeric parse failures are returned as
// FormIssues with form field names (stage[N].x); service validation issues use oven_stages[N].x and
// are mapped back with FormFieldName.
func (f RecipeForm) ToInput() domain.RecipeInput {
	var p numParser
	in := domain.RecipeInput{Title: f.Title, Description: f.Description, Notes: f.Notes, Language: f.Language,
		SourceURL: f.SourceURL, SourceName: f.SourceName, Visibility: f.Visibility, LabelSource: "user"}
	in.Servings = p.opt("servings", f.Servings)
	in.PrepMinutes = p.opt("prep_minutes", f.PrepMinutes)
	in.Categories = append(in.Categories, f.CategorySlugs...)
	tags, bad := recipetext.SplitTags(f.Tags)
	if bad {
		p.issues = append(p.issues, domain.Issue{Field: "tags", Code: "too_long", Params: map[string]any{"max": domain.MaxTag}})
	}
	in.Tags = tags
	in.Ingredients = recipetext.ParseIngredients(f.Ingredients)
	in.Steps = recipetext.ParseSteps(f.Steps)
	for i, s := range f.Stages {
		fn := func(n string) string { return fmt.Sprintf("stage[%d].%s", i, n) }
		st := domain.OvenStage{Position: i + 1, Mode: s.Mode, PizzaPreset: s.PizzaPreset, Woodfire: s.Woodfire,
			PelletBlend: s.PelletBlend, PelletNote: s.PelletNote, Accessories: s.Accessories, Notes: s.Notes}
		st.TemperatureC = p.opt(fn("temperature_c"), s.TemperatureC)
		st.DurationSeconds = p.opt(fn("duration_seconds"), s.DurationSeconds)
		st.Temperature2C = p.opt(fn("temperature2_c"), s.Temperature2C)
		st.Duration2Seconds = p.opt(fn("duration2_seconds"), s.Duration2Seconds)
		st.PreheatMinutes = p.opt(fn("preheat_minutes"), s.PreheatMinutes)
		st.TargetCoreTempC = p.opt(fn("target_core_temp_c"), s.TargetCoreTempC)
		in.Stages = append(in.Stages, st)
	}
	in.FormIssues = p.issues
	return in
}

// FormFieldName maps a service/API issue field to the form field name.
func FormFieldName(f string) string {
	switch {
	case strings.HasPrefix(f, "oven_stages["):
		return "stage[" + f[len("oven_stages["):]
	case f == "categories":
		return "category"
	case f == "oven_stages":
		return "stage"
	}
	return f
}
