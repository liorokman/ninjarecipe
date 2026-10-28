package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liorokman/ninjarecipe/internal/authz"
	"github.com/liorokman/ninjarecipe/internal/domain"
	"github.com/liorokman/ninjarecipe/internal/i18n"
	"github.com/liorokman/ninjarecipe/internal/images"
	"github.com/liorokman/ninjarecipe/internal/oven"
	"github.com/liorokman/ninjarecipe/internal/recipetext"
	"github.com/liorokman/ninjarecipe/internal/reqctx"
	"github.com/liorokman/ninjarecipe/internal/web"
	"github.com/liorokman/ninjarecipe/internal/web/fixtures"
)

// fxServer serves every route with canned data (no database). State changes live in memory.
type fxServer struct {
	mu   sync.Mutex
	rd   *web.Renderer
	cat  *i18n.Catalog
	d    *fixtures.Data
	cats []domain.Label
	base string
}

func newFixtureHandler(rd *web.Renderer, cat *i18n.Catalog, base string) http.Handler {
	s := &fxServer{rd: rd, cat: cat, d: fixtures.New(time.Now().UTC()), cats: fixtures.Categories(), base: base}
	mux := http.NewServeMux()
	h := map[string]http.Handler{}
	f := func(n string, fn http.HandlerFunc) { h[n] = fn }
	f("healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") })
	f("readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"db":"ok","media":"ok","oidc":"ok"}`)
	})
	f("static", rd.ServeStatic)
	f("manifest", rd.ServeManifest)
	f("sw", rd.ServeSW)
	f("icon192", rd.ServeIcon("icon-192.png"))
	f("icon512", rd.ServeIcon("icon-512.png"))
	f("signin", s.signin)
	f("auth.login", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "nwr_session", Value: "fx-alice", Path: "/"})
		http.Redirect(w, r, safeReturn(r.URL.Query().Get("return_to")), http.StatusSeeOther)
	})
	f("auth.callback", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/", http.StatusSeeOther) })
	f("auth.logout", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "nwr_session", Value: "fx-none", Path: "/"})
		http.Redirect(w, r, web.URL("auth.loggedout"), http.StatusSeeOther)
	})
	f("auth.loggedout", func(w http.ResponseWriter, r *http.Request) {
		rd.Page(w, r, 200, "logged_out.html", &web.LoggedOutView{Layout: web.Layout{Title: loc(r).T("signin.title")}})
	})
	f("lang", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if l := r.PostForm.Get("lang"); l == "en" || l == "he" {
			http.SetCookie(w, &http.Cookie{Name: "lang", Value: l, Path: "/", MaxAge: 31536000})
		}
		http.Redirect(w, r, safeReturn(r.PostForm.Get("return_to")), http.StatusSeeOther)
	})
	f("home", s.home)
	f("recipes.list", s.list)
	f("recipe.new", s.editorNew)
	f("recipe.create", s.save("new"))
	f("recipe.view", s.view)
	f("recipe.cook", s.cook)
	f("recipe.edit", s.editorEdit("edit"))
	f("recipe.update", s.save("edit"))
	f("recipe.delete", s.del)
	f("recipe.visibility", s.visibility)
	f("recipe.copy", func(w http.ResponseWriter, r *http.Request) {
		web.SetFlash(w, "success", "flash.recipe_copied")
		http.Redirect(w, r, web.URL("recipe.edit", r.PathValue("id")), http.StatusSeeOther)
	})
	f("recipe.favourite", s.favourite)
	f("cooklog.add", s.cooklogAdd)
	f("cooklog.delete", s.cooklogDelete)
	f("image.add", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(32 << 20)
		if r.FormValue("fail") == "1" {
			rd.Fragment(w, r, 415, "upload_error", &web.UploadError{Message: loc(r).T("err.heic")})
			return
		}
		vb := s.vb(r)
		w.Header().Set("X-Image-Id", "fx-new-img")
		rd.Fragment(w, r, 201, "image_tile", vb.Image(domain.Image{ID: "fx-new-img", RecipeID: r.PathValue("id"), Key: "0123456789abcdef0123456789abcdef", Position: 3, Width: 1600, Height: 1200}))
	})
	f("image.delete", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	f("image.order", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	f("media", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "private, max-age=3600")
		_, _ = w.Write(images.Placeholder())
	})
	f("labels.suggest", s.suggest)
	f("inbox.list", s.inboxList)
	f("inbox.view", s.inboxView)
	f("inbox.edit", s.editorEdit("inbox"))
	f("inbox.update", s.save("inbox"))
	f("inbox.accept", s.inboxDone("flash.kept"))
	f("inbox.reject", s.inboxDone("flash.thrown_away"))
	f("settings", func(w http.ResponseWriter, r *http.Request) {
		rd.Page(w, r, 200, "settings.html", &web.SettingsView{Layout: web.Layout{Title: loc(r).T("settings.title"), ActiveNav: "menu"}, Version: "dev", Commit: "fixture", BuildDate: "2026-10-27"})
	})
	f("tokens.list", s.tokens(""))
	f("tokens.create", func(w http.ResponseWriter, r *http.Request) {
		s.tokens("nwr_FixtureTokenShownOnce0123456789abcdefghijklmn")(w, r)
	})
	f("tokens.revoke", func(w http.ResponseWriter, r *http.Request) {
		web.SetFlash(w, "success", "flash.token_revoked")
		http.Redirect(w, r, web.URL("tokens.list"), http.StatusSeeOther)
	})
	f("admin.categories", func(w http.ResponseWriter, r *http.Request) {
		v := &web.AdminCategoriesView{Layout: web.Layout{Title: loc(r).T("admin.title"), ActiveNav: "menu"}}
		for i, c := range s.cats {
			v.Items = append(v.Items, web.AdminCategory{ID: c.ID, Slug: c.Slug, Name: c.Name, NameHe: c.NameHe, Icon: c.Icon, SortOrder: c.SortOrder, RecipeCount: i % 4})
		}
		rd.Page(w, r, 200, "admin_categories.html", v)
	})
	redir := func(name, flash string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			web.SetFlash(w, "success", flash)
			http.Redirect(w, r, web.URL(name), http.StatusSeeOther)
		}
	}
	f("admin.category.create", redir("admin.categories", "flash.category_saved"))
	f("admin.category.update", redir("admin.categories", "flash.category_saved"))
	f("admin.category.delete", redir("admin.categories", "flash.category_deleted"))
	web.Mount(mux, h, s.wrap)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { rd.Error(w, r, "not_found", 0) })
	return s.middleware(mux)
}

// ---- plumbing ----

func safeReturn(p string) string {
	if p == "" || p[0] != '/' || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") {
		return "/"
	}
	return p
}

func loc(r *http.Request) *i18n.Loc { return reqctx.Loc(r.Context()) }

func (s *fxServer) vb(r *http.Request) web.VB {
	return web.VB{Loc: loc(r), Now: time.Now(), BaseURL: s.base}
}

// middleware: language (?lang=, cookie), user (cookie nwr_session=fx-<name>, ?as=<name>|none; default alice).
func (s *fxServer) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if as := r.URL.Query().Get("as"); as != "" {
			http.SetCookie(w, &http.Cookie{Name: "nwr_session", Value: "fx-" + as, Path: "/"})
			r.AddCookie(&http.Cookie{Name: "nwr_session", Value: "fx-" + as})
		}
		if l := r.URL.Query().Get("lang"); l == "en" || l == "he" {
			http.SetCookie(w, &http.Cookie{Name: "lang", Value: l, Path: "/", MaxAge: 31536000})
			r.AddCookie(&http.Cookie{Name: "lang", Value: l})
		}
		name := "alice"
		if ck, err := r.Cookie("nwr_session"); err == nil {
			name = strings.TrimPrefix(ck.Value, "fx-")
		}
		ctx := r.Context()
		var u *domain.User
		if usr, ok := s.d.Users[name]; ok {
			u = usr
			ctx = reqctx.WithUser(ctx, u)
		}
		ul := ""
		if u != nil {
			ul = u.UILanguage
		}
		// The fixture user's stored language must not override the explicit cookie.
		lang := i18n.ResolveLang("", r, "en")
		if _, err := r.Cookie("lang"); err != nil && ul != "" {
			lang = ul
		}
		ctx = reqctx.WithLoc(ctx, s.cat.Localizer(lang, time.UTC))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *fxServer) wrap(rt web.Route, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := reqctx.User(r.Context())
		if rt.Access != web.Public && u == nil {
			if r.Header.Get("HX-Request") == "true" || r.Header.Get("Accept") == "application/json" {
				w.Header().Set("HX-Redirect", web.URL("signin")+"?return_to="+url.QueryEscape(r.Header.Get("HX-Current-URL")))
				w.WriteHeader(401)
				return
			}
			http.Redirect(w, r, web.URL("signin")+"?return_to="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		if rt.Access == web.Admin && !u.IsAdmin {
			s.rd.Error(w, r, "forbidden", 0)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		h.ServeHTTP(w, r)
	})
}

func (s *fxServer) recipe(w http.ResponseWriter, r *http.Request) *domain.Recipe {
	rec := s.d.Recipe(r.PathValue("id"))
	if rec == nil || !authz.CanView(reqctx.User(r.Context()), rec) {
		s.rd.Error(w, r, "not_found", 0)
		return nil
	}
	return rec
}

func (s *fxServer) summaries(u *domain.User, recs []*domain.Recipe) []domain.RecipeSummary {
	var out []domain.RecipeSummary
	for _, rec := range recs {
		sm := web.Summarize(rec)
		sm.IsFavourite = s.d.Favs[u.ID][rec.ID]
		out = append(out, sm)
	}
	return out
}

func (s *fxServer) visible(u *domain.User) []*domain.Recipe {
	var out []*domain.Recipe
	for _, rec := range s.d.Recipes {
		if authz.CanView(u, rec) {
			out = append(out, rec)
		}
	}
	return out
}

func (s *fxServer) layoutBase(r *http.Request, titleKey, nav string) web.Layout {
	return web.Layout{Title: loc(r).T(titleKey), ActiveNav: nav}
}

// ---- pages ----

func (s *fxServer) signin(w http.ResponseWriter, r *http.Request) {
	rt := safeReturn(r.URL.Query().Get("return_to"))
	s.rd.Page(w, r, 200, "signin.html", &web.SigninView{Layout: web.Layout{Title: loc(r).T("signin.title")}, LoginURL: web.URL("auth.login") + "?return_to=" + url.QueryEscape(rt)})
}

func (s *fxServer) home(w http.ResponseWriter, r *http.Request) {
	u := reqctx.User(r.Context())
	vb := s.vb(r)
	v := &web.HomeView{Layout: s.layoutBase(r, "home.title", "home")}
	have := map[string]bool{}
	vis := s.visible(u)
	for _, rec := range vis {
		for _, c := range rec.Categories() {
			have[c.Slug] = true
		}
	}
	for _, c := range s.cats {
		if have[c.Slug] {
			v.Tiles = append(v.Tiles, web.CategoryRef{Slug: c.Slug, Name: c.LocalName(vb.Loc.Lang), Icon: c.Icon})
		}
	}
	sums := s.summaries(u, vis)
	for i := range sums {
		t := s.d.Now.Add(-time.Duration(i+1) * 26 * time.Hour)
		sums[i].At = &t
	}
	for i, sm := range sums {
		if i < 5 {
			v.Recent = append(v.Recent, vb.Card(sm))
		}
		if sm.IsFavourite {
			v.Favourites = append(v.Favourites, vb.Card(sm))
		}
	}
	s.rd.Page(w, r, 200, "home.html", v)
}

const fxPageSize = 4 // small on purpose so "Show more" is exercised

func (s *fxServer) list(w http.ResponseWriter, r *http.Request) {
	u := reqctx.User(r.Context())
	vb := s.vb(r)
	q := web.ParseSearchQuery(r)
	var items []domain.RecipeSummary
	for _, sm := range s.summaries(u, s.visible(u)) {
		rec := s.d.Recipe(sm.ID)
		if q.Q != "" && !strings.Contains(strings.ToLower(rec.Title+" "+recipetext.FormatIngredients(rec.Ingredients)), strings.ToLower(q.Q)) {
			continue
		}
		if q.Scope == "mine" && rec.OwnerID != u.ID || q.Scope == "family" && rec.Visibility != "shared" {
			continue
		}
		if q.Fav && !sm.IsFavourite || q.Smoked && !sm.UsesWoodfire {
			continue
		}
		if len(q.Categories) > 0 && !anyCat(rec, q.Categories) || q.MaxTimeMin > 0 && sm.TotalSeconds > q.MaxTimeMin*60 {
			continue
		}
		if len(q.Modes) > 0 && !anyMode(rec, q.Modes) {
			continue
		}
		items = append(items, sm)
	}
	if q.Sort == "title" {
		sort.Slice(items, func(i, j int) bool { return items[i].Title < items[j].Title })
	}
	off, _ := strconv.Atoi(q.Cursor)
	end := off + fxPageSize
	next := ""
	if end < len(items) {
		next = strconv.Itoa(end)
	} else {
		end = len(items)
	}
	if off > len(items) {
		off = len(items)
	}
	page := domain.SearchPage{Items: items[off:end], NextCursor: next}
	if r.Header.Get("HX-Request") == "true" {
		s.rd.Fragment(w, r, 200, "results", vb.Results(q, page))
		return
	}
	s.rd.Page(w, r, 200, "recipes_list.html", vb.ListPage(q, s.cats, page))
}

func anyCat(r *domain.Recipe, slugs []string) bool {
	for _, c := range r.Categories() {
		if domain.Contains(slugs, c.Slug) {
			return true
		}
	}
	return false
}
func anyMode(r *domain.Recipe, modes []string) bool {
	for _, st := range r.Stages {
		if domain.Contains(modes, st.Mode) {
			return true
		}
	}
	return false
}

func (s *fxServer) pageIn(r *http.Request, rec *domain.Recipe) web.RecipePageIn {
	u := reqctx.User(r.Context())
	in := web.RecipePageIn{Actor: u, Recipe: rec, IsFavourite: s.d.Favs[u.ID][rec.ID], Logs: s.d.Logs[rec.ID]}
	in.Stats = stats(in.Logs)
	return in
}

func stats(logs []domain.CookLog) domain.CookStats {
	st := domain.CookStats{Count: len(logs)}
	sum := 0
	for i := range logs {
		if st.LastAt == nil || logs[i].CookedAt.After(*st.LastAt) {
			t := logs[i].CookedAt
			st.LastAt = &t
		}
		if logs[i].Rating != nil {
			sum += *logs[i].Rating
			st.RatingCount++
		}
	}
	if st.RatingCount > 0 {
		a := float64(sum) / float64(st.RatingCount)
		st.AvgRating = &a
	}
	return st
}

func (s *fxServer) view(w http.ResponseWriter, r *http.Request) {
	rec := s.recipe(w, r)
	if rec == nil {
		return
	}
	if rec.Status == domain.StatusInbox {
		http.Redirect(w, r, web.URL("inbox.view", rec.ID), http.StatusSeeOther)
		return
	}
	s.rd.Page(w, r, 200, "recipe_view.html", s.vb(r).RecipePage(s.pageIn(r, rec)))
}

func (s *fxServer) cook(w http.ResponseWriter, r *http.Request) {
	if rec := s.recipe(w, r); rec != nil {
		s.rd.Page(w, r, 200, "cook.html", s.vb(r).CookPage(reqctx.User(r.Context()), rec))
	}
}

func (s *fxServer) editorNew(w http.ResponseWriter, r *http.Request) {
	vb := s.vb(r)
	v := vb.Editor(web.EditorIn{Actor: reqctx.User(r.Context()), Mode: "new", Categories: s.cats})
	s.rd.Page(w, r, 200, "recipe_edit.html", v)
}

func (s *fxServer) editorEdit(mode string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec := s.recipe(w, r)
		if rec == nil {
			return
		}
		vb := s.vb(r)
		v := vb.Editor(web.EditorIn{Actor: reqctx.User(r.Context()), Mode: mode, Recipe: rec, Form: web.FormFromRecipe(rec), Categories: s.cats, CanDelete: authz.CanDelete(reqctx.User(r.Context()), rec)})
		s.rd.Page(w, r, 200, "recipe_edit.html", v)
	}
}

// save: validates with the real oven rules. Magic titles for UI testing: "conflict" => 409.
func (s *fxServer) save(mode string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := reqctx.User(r.Context())
		_ = r.ParseMultipartForm(8 << 20)
		_ = r.ParseForm()
		vb := s.vb(r)
		form := web.ParseRecipeForm(r)
		in := form.ToInput()
		wantsJSON := strings.Contains(r.Header.Get("Accept"), "application/json")
		var errs []domain.Issue
		errs = append(errs, in.FormIssues...)
		if strings.TrimSpace(in.Title) == "" {
			errs = append(errs, domain.Issue{Field: "title", Code: "required"})
		}
		for i := range in.Stages {
			oven.Normalize(&in.Stages[i])
		}
		e, warns := oven.ValidateStages(in.Stages, "oven_stages")
		errs = append(errs, e...)
		var rec *domain.Recipe
		if mode != "new" {
			rec = s.recipe(w, r)
			if rec == nil {
				return
			}
		}
		if strings.EqualFold(in.Title, "conflict") {
			if wantsJSON {
				s.rd.Fragment(w, r, 409, "conflict", &web.ConflictView{RecipeURL: web.URL("recipe.view", "fx-ribs")})
				return
			}
			s.rd.Error(w, r, "conflict", 0)
			return
		}
		if len(errs) > 0 {
			ed := vb.Editor(web.EditorIn{Actor: u, Mode: mode, Recipe: rec, Form: form, Categories: s.cats, Errors: errs, Warnings: warns})
			if wantsJSON {
				s.rd.Fragment(w, r, 422, "recipe_form", ed)
			} else {
				s.rd.Page(w, r, 422, "recipe_edit.html", ed)
			}
			return
		}
		id := "fx-ribs"
		if rec != nil {
			id = rec.ID
		}
		if mode == "inbox" {
			web.SetFlash(w, "success", "flash.kept")
		} else {
			web.SetFlash(w, "success", "flash.recipe_saved")
		}
		target := web.URL("recipe.view", id)
		if wantsJSON {
			w.Header().Set("Content-Type", "application/json")
			resp := map[string]any{"id": id, "url": target, "version": form.Version + 1}
			if len(warns) > 0 {
				resp["warnings"] = warns
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
	}
}

func (s *fxServer) del(w http.ResponseWriter, r *http.Request) {
	if rec := s.recipe(w, r); rec != nil {
		web.SetFlash(w, "success", "flash.recipe_deleted")
		http.Redirect(w, r, web.URL("recipes.list"), http.StatusSeeOther)
	}
}

func (s *fxServer) visibility(w http.ResponseWriter, r *http.Request) {
	rec := s.recipe(w, r)
	if rec == nil {
		return
	}
	u := reqctx.User(r.Context())
	if !authz.CanChangeVisibility(u, rec) {
		s.rd.Error(w, r, "forbidden", 0)
		return
	}
	_ = r.ParseForm()
	if r.PostForm.Get("shared") == "1" {
		rec.Visibility = domain.VisShared
	} else {
		rec.Visibility = domain.VisPrivate
	}
	if r.Header.Get("HX-Request") == "true" {
		s.rd.Fragment(w, r, 200, "visibility_control", web.VisibilityView{RecipeID: rec.ID, Shared: rec.Visibility == "shared", CanChange: true, OwnerName: rec.OwnerName})
		return
	}
	http.Redirect(w, r, web.URL("recipe.view", rec.ID), http.StatusSeeOther)
}

func (s *fxServer) favourite(w http.ResponseWriter, r *http.Request) {
	rec := s.recipe(w, r)
	if rec == nil {
		return
	}
	u := reqctx.User(r.Context())
	_ = r.ParseForm()
	on := r.PostForm.Get("on") == "1"
	if on {
		s.d.Favs[u.ID][rec.ID] = true
	} else {
		delete(s.d.Favs[u.ID], rec.ID)
	}
	if r.Header.Get("HX-Request") == "true" {
		s.rd.Fragment(w, r, 200, "favourite_button", web.FavouriteView{RecipeID: rec.ID, IsFavourite: on})
		return
	}
	http.Redirect(w, r, web.URL("recipe.view", rec.ID), http.StatusSeeOther)
}

func (s *fxServer) cooklogAdd(w http.ResponseWriter, r *http.Request) {
	rec := s.recipe(w, r)
	if rec == nil {
		return
	}
	u := reqctx.User(r.Context())
	_ = r.ParseMultipartForm(32 << 20)
	var rating *int
	if n, err := strconv.Atoi(r.FormValue("rating")); err == nil && n >= 1 && n <= 5 {
		rating = &n
	}
	l := domain.CookLog{ID: time.Now().UnixNano(), RecipeID: rec.ID, UserID: u.ID, AuthorName: u.DisplayName, CookedAt: time.Now(), Note: r.FormValue("note"), Rating: rating}
	s.d.Logs[rec.ID] = append([]domain.CookLog{l}, s.d.Logs[rec.ID]...)
	if r.Header.Get("HX-Request") == "true" {
		sec := s.vb(r).LogSection(s.pageIn(r, rec))
		sec.PhotoFailed = r.FormValue("photo_fail") == "1"
		s.rd.Fragment(w, r, 200, "cooklog_section", sec)
		return
	}
	http.Redirect(w, r, web.URL("recipe.view", rec.ID)+"#cook-logs", http.StatusSeeOther)
}

func (s *fxServer) cooklogDelete(w http.ResponseWriter, r *http.Request) {
	rec := s.recipe(w, r)
	if rec == nil {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("logID"), 10, 64)
	logs := s.d.Logs[rec.ID]
	for i := range logs {
		if logs[i].ID == id && authz.CanDeleteCookLog(reqctx.User(r.Context()), &logs[i]) {
			s.d.Logs[rec.ID] = append(logs[:i:i], logs[i+1:]...)
			break
		}
	}
	if r.Header.Get("HX-Request") == "true" {
		s.rd.Fragment(w, r, 200, "cooklog_section", s.vb(r).LogSection(s.pageIn(r, rec)))
		return
	}
	http.Redirect(w, r, web.URL("recipe.view", rec.ID)+"#cook-logs", http.StatusSeeOther)
}

func (s *fxServer) suggest(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(r.URL.Query().Get("q"))
	type item struct{ Type, Slug, Name string }
	var out []map[string]string
	l := loc(r).Lang
	for _, c := range s.cats {
		if strings.Contains(strings.ToLower(c.LocalName(l)), q) {
			out = append(out, map[string]string{"type": "category", "slug": c.Slug, "name": c.LocalName(l)})
		}
	}
	for _, t := range []string{"weekend", "bbq", "sunday", "brisket", "quick"} {
		if strings.Contains(t, q) {
			out = append(out, map[string]string{"type": "tag", "slug": t, "name": t})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *fxServer) inboxItems(u *domain.User) []*domain.Recipe {
	var out []*domain.Recipe
	for _, r := range s.d.Inbox {
		if r.OwnerID == u.ID {
			out = append(out, r)
		}
	}
	return out
}

func (s *fxServer) inboxList(w http.ResponseWriter, r *http.Request) {
	u := reqctx.User(r.Context())
	v := &web.InboxListView{Layout: s.layoutBase(r, "inbox.title", "inbox")}
	for _, rec := range s.inboxItems(u) {
		c := web.InboxCardView{ID: rec.ID, Title: rec.Title, SourceHost: web.HostOf(rec.SourceURL), Lang: web.LangFor(rec.Language), Dir: web.DirFor(rec.Language), ReceivedAt: rec.CreatedAt}
		if len(rec.Images) > 0 {
			c.ThumbURL = web.MediaURL(rec.Images[0].Key, "thumb")
		}
		v.Items = append(v.Items, c)
	}
	s.rd.Page(w, r, 200, "inbox_list.html", v)
}

func (s *fxServer) inboxView(w http.ResponseWriter, r *http.Request) {
	rec := s.recipe(w, r)
	if rec == nil || rec.Status != domain.StatusInbox {
		if rec != nil {
			http.Redirect(w, r, web.URL("recipe.view", rec.ID), http.StatusSeeOther)
		}
		return
	}
	v := &web.InboxItemView{Layout: web.Layout{Title: rec.Title}, RecipeBody: s.vb(r).Body(rec), MissingOven: len(rec.Stages) == 0}
	for _, o := range s.inboxItems(reqctx.User(r.Context())) {
		if o.ID != rec.ID {
			v.Next = &web.NextRef{ID: o.ID}
			break
		}
	}
	s.rd.Page(w, r, 200, "inbox_item.html", v)
}

func (s *fxServer) inboxDone(flash string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec := s.recipe(w, r)
		if rec == nil {
			return
		}
		for _, o := range s.inboxItems(reqctx.User(r.Context())) {
			if o.ID != rec.ID {
				web.SetFlash(w, "success", flash)
				http.Redirect(w, r, web.URL("inbox.view", o.ID), http.StatusSeeOther)
				return
			}
		}
		web.SetFlash(w, "success", "flash.inbox_empty")
		http.Redirect(w, r, web.URL("inbox.list"), http.StatusSeeOther)
	}
}

func (s *fxServer) tokens(newToken string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v := &web.TokensView{Layout: s.layoutBase(r, "tokens.title", "menu"), NewToken: newToken,
			ScopeOptions: []string{"inbox:write", "recipes:read"}, ExpiryOptions: []int{0, 30, 90, 365}}
		now := time.Now()
		for _, t := range s.d.Tokens {
			v.Tokens = append(v.Tokens, web.TokenView{ID: t.ID, Name: t.Name, Prefix: t.Prefix, Scopes: t.Scopes, CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt, ExpiresAt: t.ExpiresAt, Status: t.Status(now)})
		}
		s.rd.Page(w, r, 200, "tokens.html", v)
	}
}
