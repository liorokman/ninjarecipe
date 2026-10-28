package web

import (
	"fmt"
	"strings"
	"time"

	"github.com/liorokman/ninjarecipe/internal/authz"
	"github.com/liorokman/ninjarecipe/internal/domain"
	"github.com/liorokman/ninjarecipe/internal/i18n"
	"github.com/liorokman/ninjarecipe/internal/oven"
)

// VB builds view-model parts from domain objects. It is shared by the real handlers and the fixtures.
type VB struct {
	Loc     *i18n.Loc
	Now     time.Time
	BaseURL string
}

func iptr(i int) *int { return &i }

// MediaURL builds a media URL.
func MediaURL(key, variant string) string { return URL("media", key, variant) }

// Image converts a domain image.
func (vb VB) Image(im domain.Image) ImageView {
	return ImageView{ID: im.ID, RecipeID: im.RecipeID, ThumbURL: MediaURL(im.Key, "thumb"), FullURL: MediaURL(im.Key, "full"),
		Caption: im.Caption, Width: im.Width, Height: im.Height, Position: im.Position, IsCover: im.Position == 1}
}

// Stage derives the display model of an oven stage.
func (vb VB) Stage(s domain.OvenStage) OvenStageView {
	total := oven.StageSeconds(s)
	acc := s.Accessories
	if acc == nil {
		acc = []string{}
	}
	return OvenStageView{
		Position: s.Position, Mode: s.Mode, Preset: s.PizzaPreset, PelletBlend: s.PelletBlend, PelletNote: s.PelletNote,
		TemperatureC: s.TemperatureC, Temperature2C: s.Temperature2C, DurationSeconds: s.DurationSeconds,
		Duration2Seconds: s.Duration2Seconds, PreheatMinutes: s.PreheatMinutes, TargetCoreTempC: s.TargetCoreTempC,
		TotalSeconds: total, IsGourmet: s.Mode == domain.ModeGourmetRoast,
		Woodfire: s.Woodfire || s.Mode == domain.ModeSmoker, Preheat: oven.Preheat(s.Mode),
		FrameLevel: oven.FrameLevel(s.Mode), ShowFrameProminent: s.Mode == domain.ModeBroil,
		Accessories: acc, Notes: s.Notes, OilWarning: oven.MaxTemp(s) > oven.OilWarningC,
		SmokerIgnition: s.Mode == domain.ModeSmoker, PreAlert: total >= 600,
	}
}

// Ingredient converts an ingredient.
func (vb VB) Ingredient(i domain.Ingredient) IngredientView {
	v := IngredientView{Section: i.Section, Amount: i.Amount, Unit: i.Unit, Item: i.Item, RawText: i.RawText}
	v.UnitKey = i.Unit
	if i.Amount != nil {
		v.AmountText = vb.Loc.FmtQtyUnit(*i.Amount, i.Unit)
		_, v.UnitKey = i18n.ConvertUnit(*i.Amount, i.Unit)
	}
	return v
}

// Body builds the shared recipe body.
func (vb VB) Body(r *domain.Recipe) RecipeBody {
	b := RecipeBody{ID: r.ID, Title: r.Title, Description: r.Description, Notes: r.Notes, Language: r.Language,
		Lang: LangFor(r.Language), Dir: DirFor(r.Language), Servings: r.Servings, PrepMinutes: r.PrepMinutes,
		SourceURL: r.SourceURL, SourceName: r.SourceName, SourceHost: HostOf(r.SourceURL), Visibility: r.Visibility,
		OwnerName: r.OwnerName, Tags: []string{}}
	for _, s := range r.Stages {
		b.Stages = append(b.Stages, vb.Stage(s))
	}
	for _, i := range r.Ingredients {
		b.Ingredients = append(b.Ingredients, vb.Ingredient(i))
	}
	for _, s := range r.Steps {
		sv := StepView{Position: s.Position, Text: s.Text}
		if s.TimerSeconds != nil {
			sv.TimerSeconds = *s.TimerSeconds
		}
		b.Steps = append(b.Steps, sv)
	}
	for _, im := range r.Images {
		b.Images = append(b.Images, vb.Image(im))
	}
	for _, l := range r.Categories() {
		b.Categories = append(b.Categories, CategoryRef{Slug: l.Slug, Name: l.LocalName(vb.Loc.Lang), Icon: l.Icon})
	}
	for _, l := range r.Tags() {
		b.Tags = append(b.Tags, l.Name)
	}
	return b
}

// Card builds a recipe card from a summary.
func (vb VB) Card(s domain.RecipeSummary) RecipeCardView {
	c := RecipeCardView{ID: s.ID, Title: s.Title, Lang: LangFor(s.Language), Dir: DirFor(s.Language),
		PrimaryMode: s.PrimaryMode, PrimaryTempC: s.PrimaryTempC, Visibility: s.Visibility,
		UsesWoodfire: s.UsesWoodfire, IsFavourite: s.IsFavourite, TotalSeconds: s.TotalSeconds, At: s.At}
	if s.CoverKey != "" {
		c.ThumbURL = MediaURL(s.CoverKey, "thumb")
	}
	return c
}

// Summarize derives the summary fields that depend on stages.
func Summarize(r *domain.Recipe) domain.RecipeSummary {
	s := domain.RecipeSummary{ID: r.ID, OwnerID: r.OwnerID, OwnerName: r.OwnerName, Status: r.Status, Visibility: r.Visibility,
		Title: r.Title, Language: r.Language, SourceURL: r.SourceURL, UpdatedAt: r.UpdatedAt, CreatedAt: r.CreatedAt}
	if p := oven.PrimaryStage(r.Stages); p != nil {
		s.PrimaryMode = p.Mode
		s.PrimaryTempC = p.TemperatureC
	}
	s.TotalSeconds = oven.TotalSeconds(r.Stages)
	s.UsesWoodfire = oven.UsesWoodfire(r.Stages)
	if len(r.Images) > 0 {
		s.CoverKey = r.Images[0].Key
	}
	return s
}

// Perms computes the permission flags for the actor.
func Perms(u *domain.User, r *domain.Recipe, fav bool) PermFlags {
	own := authz.IsOwner(u, r)
	return PermFlags{
		IsOwner: own, CanEdit: authz.CanEdit(u, r), CanDelete: authz.CanDelete(u, r),
		CanChangeVisibility: authz.CanChangeVisibility(u, r), CanCopy: authz.CanCopy(u, r),
		ShowCopy: authz.CanCopy(u, r) && !own, CanFavourite: authz.CanFavourite(u, r),
		CanLogCook: authz.CanLogCook(u, r), IsFavourite: fav,
	}
}

// CookSummary converts stats.
func CookSummaryOf(st domain.CookStats) CookSummary {
	return CookSummary{Count: st.Count, LastAt: st.LastAt, AvgRating: st.AvgRating, RatingCount: st.RatingCount}
}

// RecipePageIn is the input of RecipeView.
type RecipePageIn struct {
	Actor       *domain.User
	Recipe      *domain.Recipe
	IsFavourite bool
	Logs        []domain.CookLog
	Stats       domain.CookStats
	PhotoFailed bool
}

// RecipePage builds the recipe page view model.
func (vb VB) RecipePage(in RecipePageIn) *RecipeView {
	r := in.Recipe
	pf := Perms(in.Actor, r, in.IsFavourite)
	v := &RecipeView{
		Layout:     Layout{Title: r.Title, ActiveNav: ""},
		RecipeBody: vb.Body(r), PermFlags: pf,
		Visibility:     VisibilityView{RecipeID: r.ID, Shared: r.Visibility == domain.VisShared, CanChange: pf.CanChangeVisibility, OwnerName: r.OwnerName},
		FavouriteState: FavouriteView{RecipeID: r.ID, IsFavourite: in.IsFavourite},
		Cook:           CookSummaryOf(in.Stats),
		ShareURL:       vb.BaseURL + URL("recipe.view", r.ID),
		Delete:         DeleteDialogView{ID: r.ID, Title: r.Title, Action: URL("recipe.delete", r.ID), Shared: r.Visibility == domain.VisShared},
		Editable:       pf.CanEdit, MaxNote: domain.MaxCookLogNote,
	}
	v.Logs = vb.LogSection(in)
	if r.UpdatedBy != "" && (in.Actor == nil || r.UpdatedBy != in.Actor.ID || r.UpdatedBy != r.CreatedBy) && r.UpdatedByName != "" && !r.UpdatedAt.Equal(r.CreatedAt) {
		v.LastChanged = &LastChanged{At: r.UpdatedAt, ByName: r.UpdatedByName}
	}
	return v
}

// LogSection builds the cook-log section fragment model.
func (vb VB) LogSection(in RecipePageIn) CookLogSectionView {
	s := CookLogSectionView{RecipeID: in.Recipe.ID, Summary: CookSummaryOf(in.Stats), CanLogCook: authz.CanLogCook(in.Actor, in.Recipe),
		PhotoFailed: in.PhotoFailed, MaxNote: domain.MaxCookLogNote}
	for i := range in.Logs {
		l := in.Logs[i]
		lv := CookLogView{ID: l.ID, AuthorName: l.AuthorName, At: l.CookedAt, Rating: l.Rating, Note: l.Note,
			CanDelete: authz.CanDeleteCookLog(in.Actor, &l)}
		if l.Image != nil {
			lv.PhotoThumbURL, lv.PhotoFullURL = MediaURL(l.Image.Key, "thumb"), MediaURL(l.Image.Key, "full")
		}
		s.Logs = append(s.Logs, lv)
	}
	return s
}

// CookPage builds the cook mode view model.
func (vb VB) CookPage(actor *domain.User, r *domain.Recipe) *CookView {
	body := vb.Body(r)
	cfg := CookConfig{RecipeID: r.ID, Title: r.Title, Servings: r.Servings, Stages: []CookStageConfig{}}
	if actor != nil {
		cfg.UserID = actor.ID
	}
	for _, s := range body.Stages {
		sc := CookStageConfig{Position: s.Position, TotalSeconds: s.TotalSeconds, IsGourmet: s.IsGourmet, PreAlert: s.PreAlert}
		sc.Label = vb.Loc.T("oven.mode." + s.Mode)
		if s.DurationSeconds != nil {
			sc.Stage1Seconds = *s.DurationSeconds
		}
		if s.IsGourmet && s.Temperature2C != nil {
			sc.Stage2Notice = fmt.Sprintf("%d °C", *s.Temperature2C)
		}
		cfg.Stages = append(cfg.Stages, sc)
	}
	return &CookView{Layout: Layout{Title: r.Title}, RecipeBody: body, Config: cfg}
}

// SpecLabels returns the localized option lists for the editor.
func (vb VB) SpecLabels() OvenSpecLabels {
	var o OvenSpecLabels
	for _, m := range oven.Default().Modes {
		o.Modes = append(o.Modes, Option{Key: m.Key, Label: vb.Loc.T("oven.mode." + m.Key), Dial: m.Dial})
	}
	for _, k := range domain.PizzaPresets {
		o.Presets = append(o.Presets, Option{Key: k, Label: vb.Loc.T("oven.preset." + k)})
	}
	for _, k := range domain.PelletBlends {
		o.Blends = append(o.Blends, Option{Key: k, Label: vb.Loc.T("oven.pellet." + k)})
	}
	for _, k := range domain.Accessories {
		o.Accessories = append(o.Accessories, Option{Key: k, Label: vb.Loc.T("oven.accessory." + k)})
	}
	return o
}

// Issue localizes a validation issue (warn selects the "unusual" wording).
func (vb VB) Issue(is domain.Issue, warn bool) string {
	field := is.Field
	if i := strings.LastIndex(field, "."); i >= 0 {
		field = field[i+1:]
	}
	if j := strings.Index(field, "["); j >= 0 {
		field = field[:j]
	}
	label := vb.Loc.T("issue.field." + field)
	p := is.Params
	num := func(k string) any { return p[k] }
	unit, _ := p["unit"].(string)
	switch is.Code {
	case "out_of_range":
		min, max := num("min"), num("max")
		if warn {
			mode := ""
			if m, ok := p["mode"].(string); ok {
				mode = vb.Loc.T("oven.mode." + m)
			}
			if unit == "s" {
				return vb.Loc.T("issue.warn.duration", "Mode", mode, "Min", vb.Loc.DurationWords(toInt(min)), "Max", vb.Loc.DurationWords(toInt(max)))
			}
			return vb.Loc.T("issue.warn.temperature", "Mode", mode, "Min", min, "Max", max)
		}
		switch unit {
		case "s":
			return vb.Loc.T("issue.out_of_range.duration", "Min", vb.Loc.DurationWords(toInt(min)), "Max", vb.Loc.DurationWords(toInt(max)))
		case "c":
			return vb.Loc.T("issue.out_of_range.temperature", "Min", min, "Max", max)
		}
		return vb.Loc.T("issue.out_of_range.number", "Min", min, "Max", max)
	case "too_long":
		return vb.Loc.T("issue.too_long", "Field", label, "Max", num("max"))
	case "too_many":
		return vb.Loc.T("issue.too_many", "Field", label, "Max", num("max"))
	case "duplicate":
		return vb.Loc.T("issue.duplicate")
	case "required", "forbidden", "invalid":
		return vb.Loc.T("issue."+is.Code, "Field", label)
	}
	return vb.Loc.T("issue.invalid", "Field", label)
}
