package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"cooking/internal/auth"
	"cooking/internal/domain"
	"cooking/internal/httpx"
	"cooking/internal/reqctx"
	"cooking/internal/service"
)

// ---- inbox ----

func (a *App) afterInbox(w http.ResponseWriter, r *http.Request, next, flash string) {
	if next != "" {
		SetFlash(w, "success", flash)
		http.Redirect(w, r, URL("inbox.view", next), http.StatusSeeOther)
		return
	}
	SetFlash(w, "success", "flash.inbox_empty")
	http.Redirect(w, r, URL("inbox.list"), http.StatusSeeOther)
}

func (a *App) inboxList(w http.ResponseWriter, r *http.Request) {
	vb := a.vb(r)
	items, err := a.Svc.ListInbox(r.Context(), a.user(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	v := &InboxListView{Layout: Layout{Title: vb.Loc.T("inbox.title"), ActiveNav: "inbox"}}
	for _, s := range items {
		c := InboxCardView{ID: s.ID, Title: s.Title, SourceHost: HostOf(s.SourceURL), Lang: LangFor(s.Language), Dir: DirFor(s.Language), ReceivedAt: s.CreatedAt}
		if s.CoverKey != "" {
			c.ThumbURL = MediaURL(s.CoverKey, "thumb")
		}
		v.Items = append(v.Items, c)
	}
	a.R.Page(w, r, 200, "inbox_list.html", v)
}

func (a *App) inboxView(w http.ResponseWriter, r *http.Request) {
	rec, next, err := a.Svc.GetInboxItem(r.Context(), a.user(r), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// an item that was already kept lives on its recipe page
			if d, e := a.Svc.GetRecipe(r.Context(), a.user(r), r.PathValue("id"), false); e == nil && d.Recipe.Status == domain.StatusActive {
				http.Redirect(w, r, URL("recipe.view", d.Recipe.ID), http.StatusSeeOther)
				return
			}
		}
		a.fail(w, r, err)
		return
	}
	v := &InboxItemView{Layout: Layout{ActiveNav: "inbox"}, RecipeBody: a.vb(r).Body(rec), MissingOven: len(rec.Stages) == 0}
	v.Layout.Title = rec.Title
	if next != "" {
		v.Next = &NextRef{ID: next}
	}
	a.R.Page(w, r, 200, "inbox_item.html", v)
}

func (a *App) inboxAccept(w http.ResponseWriter, r *http.Request) {
	if err := a.parseForm(w, r, formLimit); err != nil {
		a.fail(w, r, err)
		return
	}
	next, err := a.Svc.AcceptInbox(r.Context(), a.user(r), r.PathValue("id"), r.PostForm.Get("share") == "1")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.afterInbox(w, r, next, "flash.kept")
}

func (a *App) inboxReject(w http.ResponseWriter, r *http.Request) {
	next, err := a.Svc.RejectInbox(r.Context(), a.user(r), r.PathValue("id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.afterInbox(w, r, next, "flash.thrown_away")
}

// ---- settings, tokens, admin ----

func (a *App) settings(w http.ResponseWriter, r *http.Request) {
	a.R.Page(w, r, 200, "settings.html", &SettingsView{Layout: Layout{Title: a.vb(r).Loc.T("settings.title"), ActiveNav: "menu"},
		Version: a.Version, Commit: a.Commit, BuildDate: a.BuildDate})
}

func (a *App) tokensView(r *http.Request, newTok string, errs map[string]string) (*TokensView, error) {
	vb := a.vb(r)
	toks, err := a.Svc.ListTokens(r.Context(), a.user(r))
	if err != nil {
		return nil, err
	}
	v := &TokensView{Layout: Layout{Title: vb.Loc.T("tokens.title"), ActiveNav: "menu"}, NewToken: newTok, FormErrors: errs,
		ScopeOptions: []string{domain.ScopeInboxWrite, domain.ScopeRecipesRead}, ExpiryOptions: []int{0, 30, 90, 365}}
	now := a.Svc.Clock.Now()
	for _, t := range toks {
		v.Tokens = append(v.Tokens, TokenView{ID: t.ID, Name: t.Name, Prefix: t.Prefix, Scopes: t.Scopes, CreatedAt: t.CreatedAt,
			LastUsedAt: t.LastUsedAt, ExpiresAt: t.ExpiresAt, Status: t.Status(now)})
	}
	return v, nil
}

func (a *App) tokensList(w http.ResponseWriter, r *http.Request) {
	v, err := a.tokensView(r, "", nil)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.R.Page(w, r, 200, "tokens.html", v)
}

func (a *App) tokensCreate(w http.ResponseWriter, r *http.Request) {
	if err := a.parseForm(w, r, formLimit); err != nil {
		a.fail(w, r, err)
		return
	}
	days, _ := strconv.Atoi(r.PostForm.Get("expires_in_days"))
	plain, _, err := a.Svc.CreateToken(r.Context(), a.user(r), r.PostForm.Get("name"), r.PostForm["scopes"], days)
	vb := a.vb(r)
	if err != nil {
		var ve *domain.ValidationError
		errs := map[string]string{}
		switch {
		case errors.As(err, &ve):
			for _, is := range ve.Errors {
				errs[is.Field] = vb.Issue(is, false)
			}
		case errors.Is(err, domain.ErrLimit):
			errs["name"] = vb.Loc.T("issue.token_limit")
		default:
			a.fail(w, r, err)
			return
		}
		v, e := a.tokensView(r, "", errs)
		if e != nil {
			a.fail(w, r, e)
			return
		}
		a.R.Page(w, r, 422, "tokens.html", v)
		return
	}
	v, err := a.tokensView(r, plain, nil)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	v.Flash = &FlashView{Kind: "success", Text: vb.Loc.T("flash.token_created")}
	a.R.Page(w, r, 200, "tokens.html", v)
}

func (a *App) tokensRevoke(w http.ResponseWriter, r *http.Request) {
	if err := a.Svc.RevokeToken(r.Context(), a.user(r), r.PathValue("id")); err != nil {
		a.fail(w, r, err)
		return
	}
	SetFlash(w, "success", "flash.token_revoked")
	http.Redirect(w, r, URL("tokens.list"), http.StatusSeeOther)
}

func (a *App) adminView(r *http.Request, errs map[string]string) (*AdminCategoriesView, error) {
	vb := a.vb(r)
	cs, counts, err := a.Svc.AdminCategories(r.Context(), a.user(r))
	if err != nil {
		return nil, err
	}
	v := &AdminCategoriesView{Layout: Layout{Title: vb.Loc.T("admin.title"), ActiveNav: "menu"}, FormErrors: errs}
	for _, c := range cs {
		v.Items = append(v.Items, AdminCategory{ID: c.ID, Slug: c.Slug, Name: c.Name, NameHe: c.NameHe, Icon: c.Icon, SortOrder: c.SortOrder, RecipeCount: counts[c.ID]})
	}
	return v, nil
}

func (a *App) adminList(w http.ResponseWriter, r *http.Request) {
	v, err := a.adminView(r, nil)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.R.Page(w, r, 200, "admin_categories.html", v)
}

func categoryInput(r *http.Request) service.CategoryInput {
	n, _ := strconv.Atoi(r.PostForm.Get("sort_order"))
	return service.CategoryInput{Slug: strings.TrimSpace(r.PostForm.Get("slug")), Name: r.PostForm.Get("name"), NameHe: r.PostForm.Get("name_he"),
		Icon: r.PostForm.Get("icon"), SortOrder: n}
}

func (a *App) adminSave(update bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := a.parseForm(w, r, formLimit); err != nil {
			a.fail(w, r, err)
			return
		}
		in := categoryInput(r)
		var err error
		if update {
			id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
			err = a.Svc.UpdateCategory(r.Context(), a.user(r), id, in)
		} else {
			err = a.Svc.CreateCategory(r.Context(), a.user(r), in)
		}
		a.adminDone(w, r, err, "flash.category_saved")
	}
}

func (a *App) adminDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	a.adminDone(w, r, a.Svc.DeleteCategory(r.Context(), a.user(r), id), "flash.category_deleted")
}

func (a *App) adminDone(w http.ResponseWriter, r *http.Request, err error, flash string) {
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		vb := a.vb(r)
		errs := map[string]string{}
		for _, is := range ve.Errors {
			errs[is.Field] = vb.Issue(is, false)
		}
		v, e := a.adminView(r, errs)
		if e != nil {
			a.fail(w, r, e)
			return
		}
		a.R.Page(w, r, 422, "admin_categories.html", v)
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	SetFlash(w, "success", flash)
	http.Redirect(w, r, URL("admin.categories"), http.StatusSeeOther)
}

// ---- auth pages ----

func (a *App) signin(w http.ResponseWriter, r *http.Request) {
	rt := SafeReturnTo(r.URL.Query().Get("return_to"))
	if a.user(r) != nil {
		http.Redirect(w, r, rt, http.StatusSeeOther)
		return
	}
	a.R.Page(w, r, 200, "signin.html", &SigninView{Layout: Layout{Title: a.vb(r).Loc.T("signin.title")}, LoginURL: URL("auth.login") + "?return_to=" + url.QueryEscape(rt)})
}

func (a *App) loggedOut(w http.ResponseWriter, r *http.Request) {
	a.R.Page(w, r, 200, "logged_out.html", &LoggedOutView{Layout: Layout{Title: a.vb(r).Loc.T("signin.title")}})
}

func randToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	rt := SafeReturnTo(r.URL.Query().Get("return_to"))
	state, nonce, verifier := randToken(), randToken(), oauth2.GenerateVerifier()
	u, err := a.OIDC.AuthURL(r.Context(), state, nonce, verifier)
	if err != nil {
		a.R.Error(w, r, "unavailable", 0)
		return
	}
	if err := a.Svc.Store.W().PutLoginState(r.Context(), domain.OIDCState{State: state, Verifier: verifier, Nonce: nonce, ReturnTo: rt,
		ExpiresAt: a.Svc.Clock.Now().Add(10 * time.Minute)}); err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, u, http.StatusFound)
}

func (a *App) callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		if e == "access_denied" {
			a.R.Error(w, r, "not_allowed", 0)
		} else {
			a.R.Error(w, r, "signin_failed", 0)
		}
		return
	}
	st, err := a.Svc.Store.W().TakeLoginState(r.Context(), q.Get("state"))
	if err != nil || !st.ExpiresAt.After(a.Svc.Clock.Now()) {
		a.R.Error(w, r, "signin_expired", 0)
		return
	}
	id, err := a.OIDC.Exchange(r.Context(), q.Get("code"), st.Verifier, st.Nonce)
	if err != nil {
		if errors.Is(err, auth.ErrUnavailable) {
			a.R.Error(w, r, "unavailable", 0)
		} else {
			a.R.Error(w, r, "signin_failed", 0)
		}
		return
	}
	allowed, admin := auth.Gate(a.Cfg, id.Groups)
	if !allowed {
		a.R.Error(w, r, "not_allowed", 0)
		return
	}
	u, err := a.Svc.UpsertOIDCUser(r.Context(), service.OIDCClaims{Issuer: id.Issuer, Subject: id.Subject, Email: id.Email,
		DisplayName: id.DisplayName(), IsAdmin: admin, Locale: id.Locale}, a.initialLang(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.Sessions.Create(r.Context(), w, r, u.ID, id.RawIDToken, clientIP(r)); err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, SafeReturnTo(st.ReturnTo), http.StatusSeeOther)
}

func clientIP(r *http.Request) string {
	if ip := httpx.ClientIPFromContext(r.Context()); ip != "" {
		return ip
	}
	return r.RemoteAddr
}

// initialLang: lang cookie, else Accept-Language, else "" (NULL).
func (a *App) initialLang(r *http.Request) string {
	if ck, err := r.Cookie("lang"); err == nil && (ck.Value == "en" || ck.Value == "he") {
		return ck.Value
	}
	if r.Header.Get("Accept-Language") != "" {
		return reqctx.Loc(r.Context()).Lang
	}
	return ""
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	idTok := ""
	if s := reqctx.Session(r.Context()); s != nil {
		idTok = s.IDToken
	}
	a.Sessions.Destroy(r.Context(), w, r)
	if u := a.OIDC.EndSessionURL(idTok); u != "" {
		http.Redirect(w, r, u, http.StatusFound)
		return
	}
	http.Redirect(w, r, URL("auth.loggedout"), http.StatusFound)
}

func (a *App) setLang(w http.ResponseWriter, r *http.Request) {
	if err := a.parseForm(w, r, formLimit); err != nil {
		a.fail(w, r, err)
		return
	}
	lang := r.PostForm.Get("lang")
	if lang != "en" && lang != "he" {
		a.R.Error(w, r, "bad_request", 0)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "lang", Value: lang, Path: "/", MaxAge: 365 * 24 * 3600, SameSite: http.SameSiteLaxMode, Secure: a.Cfg.Secure()})
	if u := a.user(r); u != nil {
		if err := a.Svc.SetLanguage(r.Context(), u, lang); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	http.Redirect(w, r, SafeReturnTo(r.PostForm.Get("return_to")), http.StatusSeeOther)
}

// ---- health ----

func (a *App) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, "ok")
}

func (a *App) mediaWritable() bool {
	a.mediaMu.Lock()
	defer a.mediaMu.Unlock()
	if time.Since(a.mediaOKAt) < 30*time.Second {
		return a.mediaOK
	}
	dir := filepath.Join(a.Svc.DataDir, "media")
	ok := os.MkdirAll(dir, 0o755) == nil
	if ok {
		f, err := os.CreateTemp(dir, ".rw-*")
		if err != nil {
			ok = false
		} else {
			f.Close()
			os.Remove(f.Name())
		}
	}
	a.mediaOK, a.mediaOKAt = ok, time.Now()
	return ok
}

func (a *App) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	res := map[string]string{"db": "ok", "media": "ok", "oidc": a.OIDC.Status()}
	status := 200
	if a.DB == nil || a.DB.Ping(ctx) != nil {
		res["db"], status = "error", 503
	}
	if !a.mediaWritable() {
		res["media"], status = "error", 503
	}
	writeJSON(w, status, res)
}

// Handlers returns the route-name -> handler map for Mount.
func (a *App) Handlers() map[string]http.Handler {
	h := map[string]http.Handler{}
	f := func(n string, fn http.HandlerFunc) { h[n] = fn }
	f("healthz", a.healthz)
	f("readyz", a.readyz)
	f("static", a.R.ServeStatic)
	f("manifest", a.R.ServeManifest)
	f("sw", a.R.ServeSW)
	f("icon192", a.R.ServeIcon("icon-192.png"))
	f("icon512", a.R.ServeIcon("icon-512.png"))
	f("signin", a.signin)
	f("auth.login", a.login)
	f("auth.callback", a.callback)
	f("auth.logout", a.logout)
	f("auth.loggedout", a.loggedOut)
	f("lang", a.setLang)
	f("home", a.home)
	f("recipes.list", a.list)
	f("recipe.new", a.editorNew)
	f("recipe.create", a.save("new"))
	f("recipe.view", a.view)
	f("recipe.cook", a.cook)
	f("recipe.edit", a.editorEdit("edit"))
	f("recipe.update", a.save("edit"))
	f("recipe.delete", a.del)
	f("recipe.visibility", a.visibility)
	f("recipe.copy", a.copy)
	f("recipe.favourite", a.favourite)
	f("cooklog.add", a.cooklogAdd)
	f("cooklog.delete", a.cooklogDelete)
	f("image.add", a.imageAdd)
	f("image.delete", a.imageDelete)
	f("image.order", a.imageOrder)
	f("media", a.media)
	f("labels.suggest", a.suggest)
	f("inbox.list", a.inboxList)
	f("inbox.view", a.inboxView)
	f("inbox.edit", a.editorEdit("inbox"))
	f("inbox.update", a.save("inbox"))
	f("inbox.accept", a.inboxAccept)
	f("inbox.reject", a.inboxReject)
	f("settings", a.settings)
	f("tokens.list", a.tokensList)
	f("tokens.create", a.tokensCreate)
	f("tokens.revoke", a.tokensRevoke)
	f("admin.categories", a.adminList)
	f("admin.category.create", a.adminSave(false))
	f("admin.category.update", a.adminSave(true))
	f("admin.category.delete", a.adminDelete)
	return h
}

// Register mounts all browser routes on mux.
func (a *App) Register(mux *http.ServeMux) { Mount(mux, a.Handlers(), a.Wrap) }

// NotFound renders the 404 page for unmatched paths.
func (a *App) NotFound(w http.ResponseWriter, r *http.Request) { a.R.Error(w, r, "not_found", 0) }
