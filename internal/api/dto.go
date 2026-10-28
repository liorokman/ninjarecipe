package api

import (
	"github.com/liorokman/ninjarecipe/internal/domain"
	"github.com/liorokman/ninjarecipe/internal/oven"
	"github.com/liorokman/ninjarecipe/internal/store"
	"github.com/liorokman/ninjarecipe/internal/web"
)

type UserRef struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}
type CoverImage struct {
	ThumbURL string `json:"thumb_url"`
	FullURL  string `json:"full_url"`
}

// RecipeSummary is the list DTO (40 §6).
type RecipeSummary struct {
	ID               string      `json:"id"`
	Title            string      `json:"title"`
	Status           string      `json:"status"`
	Visibility       string      `json:"visibility"`
	Owner            *UserRef    `json:"owner"`
	Language         string      `json:"language"`
	Categories       []string    `json:"categories"`
	Tags             []string    `json:"tags"`
	PrimaryMode      *string     `json:"primary_mode"`
	TotalOvenSeconds int         `json:"total_oven_seconds"`
	UsesWoodfire     bool        `json:"uses_woodfire"`
	CoverImage       *CoverImage `json:"cover_image"`
	SourceURL        *string     `json:"source_url"`
	UpdatedAt        string      `json:"updated_at"`
}

type IngredientDTO struct {
	Section *string  `json:"section"`
	Amount  *float64 `json:"amount"`
	Unit    *string  `json:"unit"`
	Item    string   `json:"item"`
	RawText string   `json:"raw_text"`
}
type StepDTO struct {
	Text         string `json:"text"`
	TimerSeconds *int   `json:"timer_seconds"`
}
type ImageDTO struct {
	ID       string  `json:"id"`
	ThumbURL string  `json:"thumb_url"`
	FullURL  string  `json:"full_url"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	Caption  *string `json:"caption"`
}

// OvenStageDTO is the wire OvenStage with derived preheat and frame_level.
type OvenStageDTO struct {
	Mode             string   `json:"mode"`
	PizzaPreset      *string  `json:"pizza_preset"`
	TemperatureC     *int     `json:"temperature_c"`
	DurationSeconds  *int     `json:"duration_seconds"`
	Temperature2C    *int     `json:"temperature2_c"`
	Duration2Seconds *int     `json:"duration2_seconds"`
	PreheatMinutes   *int     `json:"preheat_minutes"`
	Woodfire         bool     `json:"woodfire"`
	PelletBlend      *string  `json:"pellet_blend"`
	PelletNote       *string  `json:"pellet_note"`
	Accessories      []string `json:"accessories"`
	TargetCoreTempC  *int     `json:"target_core_temp_c"`
	Notes            string   `json:"notes"`
	Preheat          bool     `json:"preheat"`
	FrameLevel       string   `json:"frame_level"`
}

// Recipe is the full DTO.
type Recipe struct {
	RecipeSummary
	Description string          `json:"description"`
	Notes       string          `json:"notes"`
	Servings    *int            `json:"servings"`
	PrepMinutes *int            `json:"prep_minutes"`
	SourceName  *string         `json:"source_name"`
	Ingredients []IngredientDTO `json:"ingredients"`
	Steps       []StepDTO       `json:"steps"`
	OvenStages  []OvenStageDTO  `json:"oven_stages"`
	Images      []ImageDTO      `json:"images"`
	Version     int             `json:"version"`
	CreatedAt   string          `json:"created_at"`
	CreatedBy   *string         `json:"created_by"`
	UpdatedBy   *string         `json:"updated_by"`
}

func sp(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (a *API) abs(key, variant string) string { return a.BaseURL + web.MediaURL(key, variant) }

func (a *API) summary(s domain.RecipeSummary) RecipeSummary {
	out := RecipeSummary{ID: s.ID, Title: s.Title, Status: s.Status, Visibility: s.Visibility, Language: s.Language,
		Categories: nonEmpty(s.Categories), Tags: nonEmpty(s.Tags), PrimaryMode: sp(s.PrimaryMode), TotalOvenSeconds: s.TotalSeconds,
		UsesWoodfire: s.UsesWoodfire, SourceURL: sp(s.SourceURL), UpdatedAt: store.FormatTime(s.UpdatedAt)}
	if s.OwnerID != "" {
		out.Owner = &UserRef{ID: s.OwnerID, DisplayName: s.OwnerName}
	}
	if s.CoverKey != "" {
		out.CoverImage = &CoverImage{ThumbURL: a.abs(s.CoverKey, "thumb"), FullURL: a.abs(s.CoverKey, "full")}
	}
	return out
}

func nonEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (a *API) recipe(r *domain.Recipe) Recipe {
	sum := web.Summarize(r)
	for _, l := range r.Categories() {
		sum.Categories = append(sum.Categories, l.Slug)
	}
	for _, l := range r.Tags() {
		sum.Tags = append(sum.Tags, l.Name)
	}
	out := Recipe{RecipeSummary: a.summary(sum), Description: r.Description, Notes: r.Notes, Servings: r.Servings, PrepMinutes: r.PrepMinutes,
		SourceName: sp(r.SourceName), Version: r.Version, CreatedAt: store.FormatTime(r.CreatedAt), CreatedBy: sp(r.CreatedBy), UpdatedBy: sp(r.UpdatedBy),
		Ingredients: []IngredientDTO{}, Steps: []StepDTO{}, OvenStages: []OvenStageDTO{}, Images: []ImageDTO{}}
	for _, i := range r.Ingredients {
		out.Ingredients = append(out.Ingredients, IngredientDTO{Section: sp(i.Section), Amount: i.Amount, Unit: sp(i.Unit), Item: i.Item, RawText: i.RawText})
	}
	for _, s := range r.Steps {
		out.Steps = append(out.Steps, StepDTO{Text: s.Text, TimerSeconds: s.TimerSeconds})
	}
	for _, s := range r.Stages {
		out.OvenStages = append(out.OvenStages, OvenStageDTO{Mode: s.Mode, PizzaPreset: sp(s.PizzaPreset), TemperatureC: s.TemperatureC,
			DurationSeconds: s.DurationSeconds, Temperature2C: s.Temperature2C, Duration2Seconds: s.Duration2Seconds, PreheatMinutes: s.PreheatMinutes,
			Woodfire: s.Woodfire, PelletBlend: sp(s.PelletBlend), PelletNote: sp(s.PelletNote), Accessories: nonEmpty(s.Accessories),
			TargetCoreTempC: s.TargetCoreTempC, Notes: s.Notes, Preheat: oven.Preheat(s.Mode), FrameLevel: oven.FrameLevel(s.Mode)})
	}
	for _, im := range r.Images {
		out.Images = append(out.Images, ImageDTO{ID: im.ID, ThumbURL: a.abs(im.Key, "thumb"), FullURL: a.abs(im.Key, "full"), Width: im.Width, Height: im.Height, Caption: sp(im.Caption)})
	}
	return out
}
