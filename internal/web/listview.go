package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/liorokman/ninjarecipe/internal/domain"
)

// SortKeys lists valid sort values.
var SortKeys = []string{"relevance", "updated", "title", "cooked", "rating", "recent"}

// ParseSearchQuery reads the query parameters of GET /recipes (40 §2.3). Invalid values fall back to defaults.
func ParseSearchQuery(r *http.Request) domain.SearchQuery {
	v := r.URL.Query()
	q := domain.SearchQuery{Q: strings.TrimSpace(v.Get("q")), Scope: v.Get("scope"), Sort: v.Get("sort"), Cursor: v.Get("cursor")}
	if utf8.RuneCountInString(q.Q) > 200 {
		q.Q = string([]rune(q.Q)[:200])
	}
	if q.Scope != "mine" && q.Scope != "family" {
		q.Scope = "all"
	}
	valid := false
	for _, k := range SortKeys {
		if k == q.Sort {
			valid = true
		}
	}
	if !valid || (q.Sort == "relevance" && q.Q == "") {
		q.Sort = ""
	}
	if q.Sort == "" {
		if q.Q != "" {
			q.Sort = "relevance"
		} else {
			q.Sort = "updated"
		}
	}
	q.Categories = nonEmpty(v["category"])
	q.Tags = nonEmpty(v["tag"])
	for _, m := range v["mode"] {
		if domain.Contains(domain.OvenModes, m) {
			q.Modes = append(q.Modes, m)
		}
	}
	q.Smoked = v.Get("smoked") == "1"
	q.Fav = v.Get("fav") == "1"
	if n, err := strconv.Atoi(v.Get("max_time")); err == nil && n > 0 {
		q.MaxTimeMin = n
	}
	return q
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// QueryArgs turns a query into URLQ key/value args (without cursor), omitting defaults.
func QueryArgs(q domain.SearchQuery) []any {
	var a []any
	if q.Q != "" {
		a = append(a, "q", q.Q)
	}
	if q.Scope != "" && q.Scope != "all" {
		a = append(a, "scope", q.Scope)
	}
	for _, c := range q.Categories {
		a = append(a, "category", c)
	}
	for _, t := range q.Tags {
		a = append(a, "tag", t)
	}
	for _, m := range q.Modes {
		a = append(a, "mode", m)
	}
	if q.Smoked {
		a = append(a, "smoked", "1")
	}
	if q.MaxTimeMin > 0 {
		a = append(a, "max_time", q.MaxTimeMin)
	}
	if q.Fav {
		a = append(a, "fav", "1")
	}
	defSort := "updated"
	if q.Q != "" {
		defSort = "relevance"
	}
	if q.Sort != defSort {
		a = append(a, "sort", q.Sort)
	}
	return a
}

// NextURL is the results URL carrying the cursor ("" if none).
func NextURL(q domain.SearchQuery, next string) string {
	if next == "" {
		return ""
	}
	return URLQ("recipes.list", append(QueryArgs(q), "cursor", next)...)
}

// ListPage builds the list view model and results fragment model.
func (vb VB) ListPage(q domain.SearchQuery, cats []domain.Label, page domain.SearchPage) *RecipesListView {
	v := &RecipesListView{Q: q.Q, Scope: q.Scope, Sort: q.Sort, Smoked: q.Smoked, Fav: q.Fav, MaxTime: q.MaxTimeMin, SelectedTags: q.Tags}
	v.Layout = Layout{Title: vb.Loc.T("list.title"), ActiveNav: "search"}
	selC := map[string]bool{}
	for _, c := range q.Categories {
		selC[c] = true
	}
	selM := map[string]bool{}
	for _, m := range q.Modes {
		selM[m] = true
	}
	without := func(drop string, val string) string {
		var args []any
		a := QueryArgs(q)
		for i := 0; i+1 < len(a); i += 2 {
			if a[i] == drop && (val == "" || a[i+1] == val) {
				continue
			}
			args = append(args, a[i], a[i+1])
		}
		return URLQ("recipes.list", args...)
	}
	for _, c := range cats {
		v.Categories = append(v.Categories, CategoryOption{Slug: c.Slug, Name: c.LocalName(vb.Loc.Lang), Icon: c.Icon, Selected: selC[c.Slug]})
		if selC[c.Slug] {
			v.Active = append(v.Active, ActiveFilter{Label: c.Icon + " " + c.LocalName(vb.Loc.Lang), RemoveURL: without("category", c.Slug)})
		}
	}
	for _, m := range domain.OvenModes {
		v.Modes = append(v.Modes, ModeOption{Key: m, Selected: selM[m]})
		if selM[m] {
			v.Active = append(v.Active, ActiveFilter{Label: vb.Loc.T("oven.mode." + m), RemoveURL: without("mode", m)})
		}
	}
	for _, t := range q.Tags {
		v.Active = append(v.Active, ActiveFilter{Label: "#" + t, RemoveURL: without("tag", t)})
	}
	if q.Smoked {
		v.Active = append(v.Active, ActiveFilter{Label: vb.Loc.T("list.filter.smoked"), RemoveURL: without("smoked", "")})
	}
	if q.Fav {
		v.Active = append(v.Active, ActiveFilter{Label: vb.Loc.T("list.filter.favourites"), RemoveURL: without("fav", "")})
	}
	if q.Scope != "all" {
		v.Active = append(v.Active, ActiveFilter{Label: vb.Loc.T("list.scope." + q.Scope), RemoveURL: without("scope", "")})
	}
	if q.MaxTimeMin > 0 {
		v.Active = append(v.Active, ActiveFilter{Label: vb.Loc.T("list.filter.max_time", "Minutes", q.MaxTimeMin), RemoveURL: without("max_time", "")})
	}
	for _, k := range SortKeys {
		if k == "relevance" && q.Q == "" {
			continue
		}
		v.SortOptions = append(v.SortOptions, SortOption{Key: k, Selected: k == q.Sort})
	}
	v.HasFilters = len(v.Active) > 0
	v.Results = vb.Results(q, page)
	v.Results.HasFilters = v.HasFilters
	return v
}

// Results builds the results fragment model.
func (vb VB) Results(q domain.SearchQuery, page domain.SearchPage) ResultsView {
	rv := ResultsView{Q: q.Q, NextURL: NextURL(q, page.NextCursor), HasFilters: q.Smoked || q.Fav || len(q.Categories)+len(q.Modes)+len(q.Tags) > 0 || q.Scope != "all" || q.MaxTimeMin > 0}
	for _, s := range page.Items {
		rv.Cards = append(rv.Cards, vb.Card(s))
	}
	return rv
}

var _ = url.QueryEscape
