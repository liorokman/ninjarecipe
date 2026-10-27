package web

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"cooking/internal/authz"
	"cooking/internal/domain"
	"cooking/internal/images"
	"cooking/internal/reqctx"
	"cooking/internal/service"
)

func (a *App) home(w http.ResponseWriter, r *http.Request) {
	vb := a.vb(r)
	d, err := a.Svc.Home(r.Context(), a.user(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	v := &HomeView{Layout: Layout{Title: vb.Loc.T("home.title"), ActiveNav: "home"}}
	for _, c := range d.Tiles {
		v.Tiles = append(v.Tiles, CategoryRef{Slug: c.Slug, Name: c.LocalName(vb.Loc.Lang), Icon: c.Icon})
	}
	for _, s := range d.Recent {
		v.Recent = append(v.Recent, vb.Card(s))
	}
	for _, s := range d.Favourites {
		v.Favourites = append(v.Favourites, vb.Card(s))
	}
	a.R.Page(w, r, 200, "home.html", v)
}

func (a *App) list(w http.ResponseWriter, r *http.Request) {
	vb := a.vb(r)
	q := ParseSearchQuery(r)
	page, err := a.Svc.Search(r.Context(), a.user(r), q)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if isHTMX(r) {
		a.R.Fragment(w, r, 200, "results", vb.Results(q, page))
		return
	}
	cats, err := a.Svc.ListCategories(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.R.Page(w, r, 200, "recipes_list.html", vb.ListPage(q, cats, page))
}

func pageIn(u *domain.User, d *service.RecipeDetail) RecipePageIn {
	return RecipePageIn{Actor: u, Recipe: d.Recipe, IsFavourite: d.IsFavourite, Logs: d.Logs, Stats: d.Stats}
}

func (a *App) view(w http.ResponseWriter, r *http.Request) {
	d, err := a.Svc.GetRecipe(r.Context(), a.user(r), r.PathValue("id"), true)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if d.Recipe.Status == domain.StatusInbox {
		http.Redirect(w, r, URL("inbox.view", d.Recipe.ID), http.StatusSeeOther)
		return
	}
	a.R.Page(w, r, 200, "recipe_view.html", a.vb(r).RecipePage(pageIn(a.user(r), d)))
}

func (a *App) cook(w http.ResponseWriter, r *http.Request) {
	d, err := a.Svc.GetRecipe(r.Context(), a.user(r), r.PathValue("id"), true)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if d.Recipe.Status == domain.StatusInbox {
		a.R.Error(w, r, "not_found", 0)
		return
	}
	a.R.Page(w, r, 200, "cook.html", a.vb(r).CookPage(a.user(r), d.Recipe))
}

// ---- editor ----

func (a *App) editorView(r *http.Request, mode string, rec *domain.Recipe, form RecipeForm, errs, warns []domain.Issue) (*EditorView, error) {
	cats, err := a.Svc.ListCategories(r.Context())
	if err != nil {
		return nil, err
	}
	u := a.user(r)
	return a.vb(r).Editor(EditorIn{Actor: u, Mode: mode, Recipe: rec, Form: form, Categories: cats, Errors: errs, Warnings: warns,
		MaxImages: a.Svc.MaxImages, MaxImageBytes: int(a.imageLimit()), CanDelete: rec != nil && authz.CanDelete(u, rec)}), nil
}

func (a *App) editorNew(w http.ResponseWriter, r *http.Request) {
	v, err := a.editorView(r, "new", nil, RecipeForm{Language: domain.LangUnd, Visibility: domain.VisPrivate}, nil, nil)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.R.Page(w, r, 200, "recipe_edit.html", v)
}

func (a *App) editorEdit(mode string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d, err := a.Svc.GetRecipe(r.Context(), a.user(r), r.PathValue("id"), false)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		rec := d.Recipe
		if mode == "edit" && rec.Status == domain.StatusInbox {
			http.Redirect(w, r, URL("inbox.edit", rec.ID), http.StatusSeeOther)
			return
		}
		if mode == "inbox" && rec.Status != domain.StatusInbox {
			a.R.Error(w, r, "not_found", 0)
			return
		}
		v, err := a.editorView(r, mode, rec, FormFromRecipe(rec), nil, nil)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		a.R.Page(w, r, 200, "recipe_edit.html", v)
	}
}

// save handles create (mode "new"), update ("edit") and edit+accept ("inbox").
func (a *App) save(mode string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := a.parseForm(w, r, formLimit); err != nil {
			a.fail(w, r, err)
			return
		}
		u := a.user(r)
		form := ParseRecipeForm(r)
		in := form.ToInput()
		id := r.PathValue("id")
		var rec *domain.Recipe
		var warns []domain.Issue
		var err error
		switch mode {
		case "new":
			rec, warns, err = a.Svc.CreateRecipe(r.Context(), u, in)
		case "edit":
			rec, warns, err = a.Svc.UpdateRecipe(r.Context(), u, id, form.Version, in)
		default:
			share := r.PostForm.Get("share") == "1" || form.Visibility == domain.VisShared
			var next string
			next, warns, err = a.Svc.EditAndAcceptInbox(r.Context(), u, id, form.Version, in, share)
			if err == nil {
				a.afterInbox(w, r, next, "flash.kept")
				return
			}
		}
		if err != nil {
			a.saveError(w, r, mode, id, form, err, warns)
			return
		}
		target := URL("recipe.view", rec.ID)
		if wantsJSON(r) {
			resp := map[string]any{"id": rec.ID, "url": target, "version": rec.Version}
			if len(warns) > 0 {
				vb := a.vb(r)
				var ws []map[string]any
				for _, wn := range warns {
					ws = append(ws, map[string]any{"field": FormFieldName(wn.Field), "code": wn.Code, "message": vb.Issue(wn, true)})
				}
				resp["warnings"] = ws
			}
			SetFlash(w, "success", "flash.recipe_saved")
			writeJSON(w, 200, resp)
			return
		}
		SetFlash(w, "success", "flash.recipe_saved")
		http.Redirect(w, r, target, http.StatusSeeOther)
	}
}

func (a *App) saveError(w http.ResponseWriter, r *http.Request, mode, id string, form RecipeForm, err error, warns []domain.Issue) {
	var ve *domain.ValidationError
	switch {
	case errors.As(err, &ve):
		var rec *domain.Recipe
		if mode != "new" {
			if d, e := a.Svc.GetRecipe(r.Context(), a.user(r), id, false); e == nil {
				rec = d.Recipe
			}
		}
		v, e := a.editorView(r, mode, rec, form, ve.Errors, ve.Warnings)
		if e != nil {
			a.fail(w, r, e)
			return
		}
		if wantsJSON(r) {
			a.R.Fragment(w, r, 422, "recipe_form", v)
			return
		}
		a.R.Page(w, r, 422, "recipe_edit.html", v)
	case errors.Is(err, domain.ErrConflict):
		if wantsJSON(r) {
			a.R.Fragment(w, r, 409, "conflict", &ConflictView{RecipeURL: URL("recipe.view", id)})
			return
		}
		a.R.Error(w, r, "conflict", 0)
	default:
		a.fail(w, r, err)
	}
}

func (a *App) del(w http.ResponseWriter, r *http.Request) {
	if err := a.Svc.DeleteRecipe(r.Context(), a.user(r), r.PathValue("id")); err != nil {
		a.fail(w, r, err)
		return
	}
	SetFlash(w, "success", "flash.recipe_deleted")
	http.Redirect(w, r, URL("recipes.list"), http.StatusSeeOther)
}

func (a *App) visibility(w http.ResponseWriter, r *http.Request) {
	if err := a.parseForm(w, r, formLimit); err != nil {
		a.fail(w, r, err)
		return
	}
	id := r.PathValue("id")
	u := a.user(r)
	if err := a.Svc.SetVisibility(r.Context(), u, id, r.PostForm.Get("shared") == "1"); err != nil {
		a.fail(w, r, err)
		return
	}
	if isHTMX(r) {
		d, err := a.Svc.GetRecipe(r.Context(), u, id, false)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		a.R.Fragment(w, r, 200, "visibility_control", VisibilityView{RecipeID: id, Shared: d.Recipe.Visibility == domain.VisShared,
			CanChange: authz.CanChangeVisibility(u, d.Recipe), OwnerName: d.Recipe.OwnerName})
		return
	}
	http.Redirect(w, r, URL("recipe.view", id), http.StatusSeeOther)
}

func (a *App) copy(w http.ResponseWriter, r *http.Request) {
	n, err := a.Svc.CopyRecipe(r.Context(), a.user(r), r.PathValue("id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	SetFlash(w, "success", "flash.recipe_copied")
	http.Redirect(w, r, URL("recipe.edit", n.ID), http.StatusSeeOther)
}

func (a *App) favourite(w http.ResponseWriter, r *http.Request) {
	if err := a.parseForm(w, r, formLimit); err != nil {
		a.fail(w, r, err)
		return
	}
	id := r.PathValue("id")
	on := r.PostForm.Get("on") == "1"
	if err := a.Svc.SetFavourite(r.Context(), a.user(r), id, on); err != nil {
		a.fail(w, r, err)
		return
	}
	if isHTMX(r) {
		a.R.Fragment(w, r, 200, "favourite_button", FavouriteView{RecipeID: id, IsFavourite: on})
		return
	}
	http.Redirect(w, r, a.backTo(r, URL("recipe.view", id)), http.StatusSeeOther)
}

// backTo returns the same-origin Referer path or def.
func (a *App) backTo(r *http.Request, def string) string {
	if ref := r.Header.Get("Referer"); ref != "" {
		if u, err := url.Parse(ref); err == nil && (u.Host == "" || strings.EqualFold(u.Host, hostOf(a.Cfg.BaseURL))) {
			return SafeReturnTo(u.RequestURI())
		}
	}
	return def
}

func (a *App) cooklogAdd(w http.ResponseWriter, r *http.Request) {
	if err := a.parseForm(w, r, a.imageLimit()+formLimit); err != nil {
		a.fail(w, r, err)
		return
	}
	id := r.PathValue("id")
	u := a.user(r)
	in := service.CookLogInput{Note: strings.TrimSpace(r.PostForm.Get("note"))}
	if n, err := strconv.Atoi(r.PostForm.Get("rating")); err == nil {
		in.Rating = &n
	}
	if r.MultipartForm != nil {
		if fh := r.MultipartForm.File["photo"]; len(fh) > 0 && fh[0].Size > 0 {
			f, err := fh[0].Open()
			if err == nil {
				defer f.Close()
				in.Photo = f
			}
		}
	}
	photoErr, err := a.Svc.LogCook(r.Context(), u, id, in)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if isHTMX(r) {
		d, err := a.Svc.GetRecipe(r.Context(), u, id, false)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		pi := pageIn(u, d)
		pi.PhotoFailed = photoErr != nil
		a.R.Fragment(w, r, 200, "cooklog_section", a.vb(r).LogSection(pi))
		return
	}
	if photoErr != nil {
		SetFlash(w, "warning", "flash.photo_failed")
	}
	http.Redirect(w, r, URL("recipe.view", id)+"#cook-logs", http.StatusSeeOther)
}

func (a *App) cooklogDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	logID, _ := strconv.ParseInt(r.PathValue("logID"), 10, 64)
	u := a.user(r)
	if err := a.Svc.DeleteCookLog(r.Context(), u, id, logID); err != nil {
		a.fail(w, r, err)
		return
	}
	if isHTMX(r) {
		d, err := a.Svc.GetRecipe(r.Context(), u, id, false)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		a.R.Fragment(w, r, 200, "cooklog_section", a.vb(r).LogSection(pageIn(u, d)))
		return
	}
	http.Redirect(w, r, URL("recipe.view", id)+"#cook-logs", http.StatusSeeOther)
}

// ---- images ----

func (a *App) uploadError(w http.ResponseWriter, r *http.Request, status int, key string) {
	a.R.Fragment(w, r, status, "upload_error", &UploadError{Message: reqctx.Loc(r.Context()).T(key)})
}

func (a *App) imageAdd(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, a.imageLimit()+formLimit)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			a.uploadError(w, r, 413, "err.too_large_image")
			return
		}
		a.uploadError(w, r, 422, "err.image_failed")
		return
	}
	fhs := r.MultipartForm.File["image"]
	if len(fhs) != 1 {
		a.uploadError(w, r, 422, "err.image_failed")
		return
	}
	f, err := fhs[0].Open()
	if err != nil {
		a.uploadError(w, r, 422, "err.image_failed")
		return
	}
	defer f.Close()
	im, err := a.Svc.AddImage(r.Context(), a.user(r), id, f)
	switch {
	case err == nil:
		w.Header().Set("X-Image-Id", im.ID)
		view := a.vb(r).Image(*im)
		a.R.Fragment(w, r, 201, "image_tile", view)
	case errors.Is(err, images.ErrHEIC):
		a.uploadError(w, r, 415, "err.heic")
	case errors.Is(err, domain.ErrUnsupportedMedia):
		a.uploadError(w, r, 415, "err.heic")
	case errors.Is(err, domain.ErrTooLarge):
		a.uploadError(w, r, 413, "err.too_large_image")
	case errors.Is(err, domain.ErrLimit):
		a.uploadError(w, r, 422, "err.too_many_images")
	case errors.Is(err, domain.ErrBusy):
		a.uploadError(w, r, 503, "err.busy")
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrForbidden):
		a.fail(w, r, err)
	default:
		a.fail(w, r, err)
	}
}

func (a *App) imageDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Svc.DeleteImage(r.Context(), a.user(r), id, r.PathValue("imageID")); err != nil {
		a.fail(w, r, err)
		return
	}
	if wantsJSON(r) || r.Header.Get("X-Requested-With") != "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, URL("recipe.edit", id), http.StatusSeeOther)
}

func (a *App) imageOrder(w http.ResponseWriter, r *http.Request) {
	if err := a.parseForm(w, r, formLimit); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.Svc.ReorderImages(r.Context(), a.user(r), r.PathValue("id"), r.PostForm["ids"]); err != nil {
		if errors.Is(err, domain.ErrBadRequest) {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// media serves GET /media/{key}/{variant} with authz (session, or bearer with recipes:read).
func (a *App) media(w http.ResponseWriter, r *http.Request) {
	key, variant := r.PathValue("key"), r.PathValue("variant")
	if !images.ValidKey(key) || (variant != "full" && variant != "thumb") {
		http.NotFound(w, r)
		return
	}
	u := a.user(r)
	if u == nil {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if t := reqctx.Token(r.Context()); t != nil && !t.HasScope(domain.ScopeRecipesRead) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	im, err := a.Svc.MediaFor(r.Context(), u, key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(images.VariantPath(a.Svc.DataDir, key, variant))
	if err != nil {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "private, max-age=60")
		_, _ = w.Write(images.Placeholder())
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("ETag", `"`+im.Key+"-"+variant+`"`)
	http.ServeContent(w, r, "", st.ModTime(), f)
}

func (a *App) suggest(w http.ResponseWriter, r *http.Request) {
	loc := reqctx.Loc(r.Context())
	lang := "en"
	if loc != nil {
		lang = loc.Lang
	}
	out, err := a.Svc.Suggest(r.Context(), a.user(r), r.URL.Query().Get("q"), lang)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, 200, out)
}
