package web

import (
	"net/url"
	"sort"
	"strconv"

	"github.com/liorokman/ninjarecipe/internal/domain"
	"github.com/liorokman/ninjarecipe/internal/oven"
)

// Editor limits exposed to JS.
const (
	MaxImageBytesDefault = 15 << 20
	MaxImagesDefault     = 10
)

// EditorIn is the input of VB.Editor.
type EditorIn struct {
	Actor         *domain.User
	Mode          string // new|edit|inbox
	Recipe        *domain.Recipe
	Form          RecipeForm
	Categories    []domain.Label
	Errors        []domain.Issue
	Warnings      []domain.Issue
	MaxImages     int
	MaxImageBytes int
	CanDelete     bool
}

// Editor builds the editor view model.
func (vb VB) Editor(in EditorIn) *EditorView {
	v := &EditorView{Mode: in.Mode, Form: in.Form, Errors: map[string][]string{}, Warnings: map[string][]string{},
		OvenSpec: oven.Default(), Spec: vb.SpecLabels(), CanDelete: in.CanDelete}
	if len(v.Form.Stages) == 0 {
		v.Form.Stages = []StageForm{}
	}
	for i := range v.Form.Stages {
		v.Form.Stages[i].Idx = strconv.Itoa(i)
	}
	if v.Form.Language == "" {
		v.Form.Language = domain.LangUnd
	}
	if v.Form.Visibility == "" {
		v.Form.Visibility = domain.VisPrivate
	}
	v.StageTemplate = StageForm{Idx: "__N__", Accessories: []string{}}
	sel := map[string]bool{}
	for _, s := range in.Form.CategorySlugs {
		sel[s] = true
	}
	for _, c := range in.Categories {
		v.AllCategories = append(v.AllCategories, CategoryOption{Slug: c.Slug, Name: c.LocalName(vb.Loc.Lang), Icon: c.Icon, Selected: sel[c.Slug]})
	}
	cfg := EditorConfig{Mode: in.Mode, MaxImages: in.MaxImages, MaxImageBytes: in.MaxImageBytes, MaxStages: domain.MaxStages}
	if cfg.MaxImages == 0 {
		cfg.MaxImages = MaxImagesDefault
	}
	if cfg.MaxImageBytes == 0 {
		cfg.MaxImageBytes = MaxImageBytesDefault
	}
	cfg.SuggestURL = URL("labels.suggest")
	draftKey := "new"
	var editURL string
	switch in.Mode {
	case "edit":
		id := in.Recipe.ID
		v.Action, v.SubmitKey = URL("recipe.update", id), "editor.save"
		editURL = URL("recipe.edit", id)
		cfg.RecipeID, cfg.Version, draftKey = id, in.Form.Version, id
	case "inbox":
		id := in.Recipe.ID
		v.Action, v.SubmitKey = URL("inbox.update", id), "editor.save_and_keep"
		editURL = URL("inbox.edit", id)
		cfg.RecipeID, cfg.Version, draftKey = id, in.Form.Version, "inbox:"+id
	default:
		v.Action, v.SubmitKey = URL("recipe.create"), "editor.save"
		editURL = URL("recipe.new")
	}
	if in.Recipe != nil {
		for _, im := range in.Recipe.Images {
			v.Images = append(v.Images, vb.Image(im))
		}
		cfg.UploadURL = URL("image.add", in.Recipe.ID)
		cfg.OrderURL = URL("image.order", in.Recipe.ID)
		cfg.DeleteImageURLTemplate = URL("image.delete", in.Recipe.ID, "__ID__")
		v.Delete = DeleteDialogView{ID: in.Recipe.ID, Title: in.Recipe.Title, Action: URL("recipe.delete", in.Recipe.ID), Shared: in.Recipe.Visibility == domain.VisShared}
	}
	cfg.Action = v.Action
	cfg.SigninURL = URL("signin") + "?return_to=" + url.QueryEscape(editURL)
	if in.Actor != nil {
		cfg.UserID = in.Actor.ID
	}
	cfg.DraftKey = "nwr_draft:" + cfg.UserID + ":" + draftKey
	v.Config = cfg
	v.Title = "editor." + in.Mode + ".title" // placeholder replaced below
	v.Title = vb.Loc.T("editor." + in.Mode + ".title")
	v.ActiveNav = "add"
	if in.Mode != "new" {
		v.ActiveNav = ""
	}
	keys := []string{}
	for _, is := range in.Errors {
		name := FormFieldName(is.Field)
		if _, ok := v.Errors[name]; !ok {
			keys = append(keys, name)
		}
		v.Errors[name] = append(v.Errors[name], vb.Issue(is, false))
		v.Summary = append(v.Summary, ErrorItem{FieldID: FieldID(name), Message: vb.Issue(is, false)})
	}
	_ = keys
	for _, is := range in.Warnings {
		name := FormFieldName(is.Field)
		v.Warnings[name] = append(v.Warnings[name], vb.Issue(is, true))
	}
	return v
}

// IssueFieldsSorted lists the error keys (stable order for tests).
func IssueFieldsSorted(m map[string][]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
