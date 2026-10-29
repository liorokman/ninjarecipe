package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/liorokman/ninjarecipe/internal/domain"
	"github.com/liorokman/ninjarecipe/internal/i18n"
	"github.com/liorokman/ninjarecipe/internal/reqctx"
	"github.com/liorokman/ninjarecipe/internal/web/fixtures"
	"github.com/liorokman/ninjarecipe/locales"
)

func TestRouteTable(t *testing.T) {
	seen := map[string]bool{}
	mux := http.NewServeMux()
	for _, r := range Routes {
		if seen[r.Name] {
			t.Fatalf("duplicate route name %s", r.Name)
		}
		seen[r.Name] = true
		rt := r
		mux.HandleFunc(r.Pattern(), func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("X-Route", rt.Name) })
	}
	for _, name := range []string{"healthz", "readyz", "static", "manifest", "sw", "icon192", "icon512", "signin", "auth.login", "auth.callback", "auth.logout",
		"auth.loggedout", "lang", "home", "recipes.list", "recipe.new", "recipe.create", "recipe.view", "recipe.cook", "recipe.edit", "recipe.update", "recipe.delete",
		"recipe.visibility", "recipe.copy", "recipe.favourite", "cooklog.add", "cooklog.delete", "image.add", "image.delete", "image.order", "media", "labels.suggest",
		"inbox.list", "inbox.view", "inbox.edit", "inbox.update", "inbox.accept", "inbox.reject", "settings", "tokens.list", "tokens.create", "tokens.revoke", "assistant.brief",
		"admin.categories", "admin.category.update", "admin.category.delete"} {
		if !seen[name] {
			t.Errorf("contract route %s missing", name)
		}
	}
	// every named route resolves to itself, wrong methods give 405
	for _, r := range Routes {
		var args []any
		for i := 0; i < strings.Count(r.Path, "{")-strings.Count(r.Path, "{$}"); i++ {
			args = append(args, "x1")
		}
		path := URL(r.Name, args...)
		req := httptest.NewRequest(r.Method, path, nil)
		h, pat := mux.Handler(req)
		if pat != r.Pattern() && !(r.Name == "recipe.new" || r.Name == "image.order") {
			t.Errorf("%s %s resolved to %q, want %q", r.Method, path, pat, r.Pattern())
		}
		_ = h
		other := "DELETE"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(other, path, nil))
		if rec.Code != 405 && rec.Code != 404 {
			t.Errorf("DELETE %s: %d", path, rec.Code)
		}
	}
	// literal wins over wildcard
	if _, pat := mux.Handler(httptest.NewRequest("GET", "/recipes/new", nil)); pat != "GET /recipes/new" {
		t.Errorf("/recipes/new -> %s", pat)
	}
	if _, pat := mux.Handler(httptest.NewRequest("POST", "/recipes/abc/images/order", nil)); pat != "POST /recipes/{id}/images/order" {
		t.Errorf("images/order -> %s", pat)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/healthz", nil))
	if rec.Code != 405 {
		t.Errorf("POST /healthz: %d", rec.Code)
	}
}

func TestURLBuilding(t *testing.T) {
	if URL("home") != "/" || URL("recipes.list") != "/recipes" || URL("recipe.edit", "abc") != "/recipes/abc/edit" {
		t.Fatal("basic")
	}
	if URL("cooklog.delete", "r1", 42) != "/recipes/r1/cook-logs/42/delete" {
		t.Fatal("two params")
	}
	if URL("recipe.view", "a/b c") != "/recipes/a%2Fb%20c" {
		t.Fatalf("escaping: %s", URL("recipe.view", "a/b c"))
	}
	if URL("static", "v1", "js/app.js") != "/static/v1/js/app.js" {
		t.Fatal("static")
	}
	if got := URLQ("recipes.list", "category", "pizza", "q", "a b"); got != "/recipes?category=pizza&q=a+b" {
		t.Fatalf("urlq %s", got)
	}
	if got := URLQ("recipe.view", "id1", "x", "1"); got != "/recipes/id1?x=1" {
		t.Fatalf("urlq with param %s", got)
	}
	if _, err := buildURL("nope", nil); err == nil {
		t.Fatal("unknown route")
	}
	if _, err := buildURL("recipe.edit", nil); err == nil {
		t.Fatal("missing arg")
	}
}

func TestSafeReturnTo(t *testing.T) {
	for in, want := range map[string]string{"": "/", "/recipes?q=x": "/recipes?q=x", "//evil.com": "/", "/\\evil.com": "/", "https://evil.com": "/", "recipes": "/", "/a\nb": "/", "/": "/"} {
		if got := SafeReturnTo(in); got != want {
			t.Errorf("SafeReturnTo(%q) = %q want %q", in, got, want)
		}
	}
}

func catalog(t *testing.T) *i18n.Catalog {
	c, err := i18n.Load(locales.FS)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestFuncs(t *testing.T) {
	cat := catalog(t)
	f := Funcs(FuncCtx{Loc: cat.Localizer("en", time.UTC), AssetVersion: "abc", Now: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)})
	call := func(name string, args ...any) any {
		fn := reflect.ValueOf(f[name])
		in := make([]reflect.Value, len(args))
		for i, a := range args {
			in[i] = reflect.ValueOf(a)
		}
		if fn.Type().IsVariadic() && len(args) == fn.Type().NumIn()-1 {
			// no variadic args supplied
		}
		out := fn.Call(in)
		return out[0].Interface()
	}
	if call("asset", "css/app.css") != "/static/abc/css/app.css" || call("fmtDuration", 5400) != "1:30" || call("fmtTemp", 220) != "220 °C" {
		t.Fatal("asset/duration/temp")
	}
	if call("fieldID", "title") != "f-title" || call("fieldID", "stage[0].temperature_c") != "stage-0-temperature_c" || call("fieldID", "stage[12].duration2_seconds") != "stage-12-duration2_seconds" {
		t.Fatal("fieldID")
	}
	if call("secH", 5400) != 1 || call("secM", 5400) != 30 || call("secS", 5430) != 30 {
		t.Fatal("sec parts")
	}
	if call("dirFor", "he") != "rtl" || call("dirFor", "und") != "auto" || call("langFor", "und") != "" || call("langFor", "he") != "he" {
		t.Fatal("dir/lang")
	}
	if call("host", "https://www.example.com/a/b?x=1") != "example.com" || call("host", "%%") != "" {
		t.Fatal("host")
	}
	if !reflect.DeepEqual(call("seq", 0, 3), []int{0, 1, 2, 3}) || !reflect.DeepEqual(call("stars", 3), []bool{true, true, true, false, false}) {
		t.Fatal("seq/stars")
	}
	if call("contains", []string{"a", "b"}, "b") != true || call("join", []string{"a", "b"}, ",") != "a,b" {
		t.Fatal("contains/join")
	}
	n := 7
	if call("deref", &n) != 7 || call("deref", (*int)(nil)) != 0 {
		t.Fatal("deref")
	}
	g := 1500.0
	if call("fmtUnit", &g, "g") != "kg" || call("fmtUnit", &g, "tsp") != "tsp" {
		t.Fatal("fmtUnit")
	}
	if fq := f["fmtQty"].(func(any, ...any) string); fq(&g, "g") != "1.5" || fq(func() *float64 { v := 0.5; return &v }(), "tsp") != "½" || fq((*float64)(nil), "g") != "" {
		t.Fatalf("fmtQty %q", fq(&g, "g"))
	}
	d := f["dict"].(func(...any) (map[string]any, error))
	if m, err := d("a", 1, "b", "x"); err != nil || m["b"] != "x" {
		t.Fatal("dict")
	}
	if _, err := d("a"); err == nil {
		t.Fatal("odd dict")
	}
	if tf := f["T"].(func(string, ...any) string); tf("oven.mode.broil") != "Broil" {
		t.Fatal("T")
	}
	if u := f["url"].(func(string, ...any) (string, error)); func() bool { _, err := u("nope"); return err == nil }() {
		t.Fatal("url must error on unknown names")
	}
}

func TestParseRecipeFormAndInput(t *testing.T) {
	v := url.Values{
		"version": {"3"}, "title": {" Ribs "}, "language": {"en"}, "servings": {"4"}, "prep_minutes": {"x"}, "category": {"beef", "pizza"},
		"tags": {"BBQ, weekend"}, "ingredients": {"# Rub\n2 tbsp paprika"}, "steps": {"Rub [10:00]"},
		"stage[1].mode": {"max_roast"}, "stage[1].temperature_c": {"250"}, "stage[1].duration_seconds": {"600"},
		"stage[0].mode": {"smoker"}, "stage[0].temperature_c": {"120"}, "stage[0].duration_seconds": {"10800"}, "stage[0].woodfire": {"1"},
		"stage[0].accessories": {"pro_heat_pan", "roast_rack"}, "stage[0].preheat_minutes": {"abc"},
	}
	r := httptest.NewRequest("POST", "/recipes", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := r.ParseForm(); err != nil {
		t.Fatal(err)
	}
	f := ParseRecipeForm(r)
	if f.Version != 3 || len(f.Stages) != 2 || f.Stages[0].Mode != "smoker" || f.Stages[1].Mode != "max_roast" || f.Stages[0].Idx != "0" || !f.Stages[0].Woodfire {
		t.Fatalf("stages re-sorted and re-indexed: %+v", f.Stages)
	}
	in := f.ToInput()
	if in.Title != "Ribs" || len(in.Categories) != 2 || strings.Join(in.Tags, ",") != "bbq,weekend" || *in.Servings != 4 || in.Stages[0].Position != 1 {
		t.Fatalf("input: %+v", in)
	}
	if in.Ingredients[0].Section != "Rub" || *in.Steps[0].TimerSeconds != 600 {
		t.Fatal("parsed text fields")
	}
	got := map[string]bool{}
	for _, is := range in.FormIssues {
		got[is.Field] = true
	}
	if !got["prep_minutes"] || !got["stage[0].preheat_minutes"] || len(got) != 2 {
		t.Fatalf("number parse issues: %v", in.FormIssues)
	}
	// prefill round trip
	rec := fixtures.New(time.Now()).Recipe("fx-ribs")
	form := FormFromRecipe(rec)
	if len(form.Stages) != 2 || form.Stages[0].DurationSeconds != "10800" || !strings.Contains(form.Ingredients, "# Ribs") || !strings.Contains(form.Steps, "[30:00]") || form.Tags != "weekend, bbq" && form.Tags != "bbq, weekend" {
		t.Fatalf("form from recipe: %+v", form)
	}
	back := form.ToInput()
	if len(back.FormIssues) != 0 || len(back.Ingredients) != len(rec.Ingredients) || len(back.Steps) != len(rec.Steps) || back.Stages[0].Mode != "smoker" {
		t.Fatalf("round trip: %+v", back)
	}
	if FormFieldName("oven_stages[2].temperature_c") != "stage[2].temperature_c" || FormFieldName("categories") != "category" || FormFieldName("title") != "title" {
		t.Fatal("FormFieldName")
	}
}

func TestParseSearchQuery(t *testing.T) {
	r := httptest.NewRequest("GET", "/recipes?q=+ribs+&scope=family&category=beef&category=&tag=a&tag=b&mode=smoker&mode=bogus&smoked=1&max_time=30&fav=1&sort=title&cursor=zz", nil)
	q := ParseSearchQuery(r)
	if q.Q != "ribs" || q.Scope != "family" || len(q.Categories) != 1 || len(q.Tags) != 2 || len(q.Modes) != 1 || !q.Smoked || q.MaxTimeMin != 30 || !q.Fav || q.Sort != "title" || q.Cursor != "zz" {
		t.Fatalf("%+v", q)
	}
	q = ParseSearchQuery(httptest.NewRequest("GET", "/recipes?scope=evil&sort=relevance", nil))
	if q.Scope != "all" || q.Sort != "updated" {
		t.Fatalf("defaults: %+v", q)
	}
	if ParseSearchQuery(httptest.NewRequest("GET", "/recipes?q=x", nil)).Sort != "relevance" {
		t.Fatal("relevance default with q")
	}
	if got := URLQ("recipes.list", QueryArgs(domain.SearchQuery{Q: "a", Scope: "mine", Categories: []string{"x"}, Smoked: true, Sort: "title"})...); got != "/recipes?category=x&q=a&scope=mine&smoked=1&sort=title" {
		t.Fatal(got)
	}
	if NextURL(domain.SearchQuery{Q: "a", Sort: "relevance"}, "") != "" || NextURL(domain.SearchQuery{Sort: "updated"}, "c1") != "/recipes?cursor=c1" {
		t.Fatal("NextURL")
	}
}

func TestBuildersStageAndPerms(t *testing.T) {
	cat := catalog(t)
	vb := VB{Loc: cat.Localizer("en", time.UTC), Now: time.Now(), BaseURL: "https://r.example.com"}
	fx := fixtures.New(time.Now())
	ribs, chicken := fx.Recipe("fx-ribs"), fx.Recipe("fx-chicken")
	body := vb.Body(ribs)
	if len(body.Stages) != 2 || !body.Stages[0].SmokerIgnition || !body.Stages[0].Woodfire || body.Stages[0].TotalSeconds != 10800 || !body.Stages[0].PreAlert ||
		body.Stages[1].Preheat != true || body.Stages[0].FrameLevel != "bottom" || body.SourceHost != "example-kitchen.com" || body.Ingredients[0].UnitKey != "kg" {
		t.Fatalf("body: %+v", body.Stages)
	}
	g := vb.Stage(chicken.Stages[0])
	if !g.IsGourmet || g.TotalSeconds != 4800 || !g.OilWarning || g.SmokerIgnition {
		t.Fatalf("gourmet: %+v", g)
	}
	br := vb.Stage(domain.OvenStage{Mode: "broil", TemperatureC: ip(300), DurationSeconds: ip(60)})
	if br.FrameLevel != "top" || !br.ShowFrameProminent || br.PreAlert || br.Preheat {
		t.Fatalf("broil: %+v", br)
	}
	alice, bob, admin := fx.Users["alice"], fx.Users["bob"], fx.Users["admin"]
	check := func(name string, u *domain.User, r *domain.Recipe, want PermFlags) {
		if got := Perms(u, r, false); got != want {
			t.Errorf("%s: got %+v want %+v", name, got, want)
		}
	}
	all := PermFlags{CanEdit: true, CanDelete: true, CanFavourite: true, CanLogCook: true, CanCopy: true}
	owner := all
	owner.IsOwner, owner.CanChangeVisibility = true, true
	other := all
	other.ShowCopy = true
	check("owner private", alice, ribs, owner)
	check("owner shared", alice, chicken, owner)
	check("other shared", bob, chicken, other)
	check("admin shared", admin, chicken, other)
	check("ownerless", bob, fx.Recipe("fx-ownerless"), other)
	check("other private", bob, ribs, PermFlags{})
	// last changed only when someone else changed it
	if lc := vb.RecipePage(RecipePageIn{Actor: alice, Recipe: chicken}).LastChanged; lc == nil || lc.ByName != "Bob" {
		t.Fatalf("last changed: %+v", lc)
	}
	if vb.RecipePage(RecipePageIn{Actor: alice, Recipe: ribs}).LastChanged != nil {
		t.Fatal("no last-changed when updated by the creator")
	}
	// Layout/RecipeBody ambiguity resolved by the methods
	rv := vb.RecipePage(RecipePageIn{Actor: alice, Recipe: ribs})
	if rv.Title() != "Smoked short ribs" || rv.Layout.Title != "Smoked short ribs" || rv.Dir() != "ltr" {
		t.Fatal("title methods")
	}
	if cv := vb.CookPage(alice, chicken); cv.Config.Stages[0].Stage2Notice != "140 °C" || !cv.Config.Stages[0].IsGourmet || cv.Config.UserID != alice.ID {
		t.Fatalf("cook config: %+v", cv.Config)
	}
}

func ip(i int) *int { return &i }

func TestIssueMessages(t *testing.T) {
	cat := catalog(t)
	vb := VB{Loc: cat.Localizer("en", time.UTC)}
	cases := []struct {
		is   domain.Issue
		warn bool
		want string
	}{
		{domain.Issue{Field: "title", Code: "required"}, false, "Name is required"},
		{domain.Issue{Field: "oven_stages[0].temperature_c", Code: "out_of_range", Params: map[string]any{"min": 40, "max": 370, "unit": "c"}}, false, "Between 40 and 370 °C"},
		{domain.Issue{Field: "stage[0].temperature_c", Code: "out_of_range", Params: map[string]any{"min": 230, "max": 370, "mode": "max_roast", "unit": "c"}}, true, "Unusual for Max Roast (230-370 °C)"},
		{domain.Issue{Field: "duration_seconds", Code: "out_of_range", Params: map[string]any{"min": 60, "max": 14400, "unit": "s"}}, false, "Between 1 min and 4 h"},
		{domain.Issue{Field: "woodfire", Code: "forbidden", Params: map[string]any{"mode": "broil"}}, false, "Woodfire flavour can't be used here"},
		{domain.Issue{Field: "title", Code: "too_long", Params: map[string]any{"max": 200}}, false, "Name is too long (at most 200)"},
	}
	for _, c := range cases {
		if got := vb.Issue(c.is, c.warn); got != c.want {
			t.Errorf("%v: %q want %q", c.is, got, c.want)
		}
	}
	he := VB{Loc: cat.Localizer("he", time.UTC)}
	if got := he.Issue(domain.Issue{Field: "title", Code: "required"}, false); !strings.Contains(got, "השם") {
		t.Fatalf("he: %q", got)
	}
}

func TestFlashCookieRoundTrip(t *testing.T) {
	cat := catalog(t)
	rd := NewRenderer(RendererOpts{Catalog: cat})
	w := httptest.NewRecorder()
	SetFlash(w, "success", "flash.recipe_saved")
	ck := w.Result().Cookies()[0]
	if !ck.HttpOnly || ck.MaxAge != 60 || ck.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie: %+v", ck)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(ck)
	w2 := httptest.NewRecorder()
	fv := rd.TakeFlash(w2, r, cat.Localizer("he", time.UTC))
	if fv == nil || fv.Kind != "success" || fv.Text != "המתכון נשמר" {
		t.Fatalf("%+v", fv)
	}
	if c := w2.Result().Cookies(); len(c) != 1 || c[0].MaxAge != -1 {
		t.Fatal("flash must be cleared")
	}
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.AddCookie(&http.Cookie{Name: "nwr_flash", Value: "!!!"})
	if rd.TakeFlash(httptest.NewRecorder(), r2, cat.Localizer("en", nil)) != nil {
		t.Fatal("garbage flash ignored")
	}
}

func TestUnauthenticatedRules(t *testing.T) {
	a := &App{Cfg: nil}
	a.Cfg = cfgFor("https://r.example.com")
	do := func(method, target string, hdr map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, target, nil)
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		a.unauthenticated(w, r)
		return w
	}
	w := do("GET", "/recipes?q=x", nil)
	if w.Code != 303 || w.Header().Get("Location") != "/signin?return_to=%2Frecipes%3Fq%3Dx" {
		t.Fatalf("GET: %d %s", w.Code, w.Header().Get("Location"))
	}
	w = do("GET", "/recipes", map[string]string{"HX-Request": "true", "HX-Current-URL": "https://r.example.com/recipes?q=y"})
	if w.Code != 401 || w.Header().Get("HX-Redirect") != "/signin?return_to=%2Frecipes%3Fq%3Dy" {
		t.Fatalf("htmx: %d %s", w.Code, w.Header().Get("HX-Redirect"))
	}
	w = do("POST", "/recipes/1/edit", map[string]string{"Accept": "application/json"})
	if w.Code != 401 || w.Header().Get("HX-Redirect") == "" {
		t.Fatalf("fetch: %d", w.Code)
	}
	w = do("POST", "/recipes/1/delete", map[string]string{"Referer": "https://r.example.com/recipes/1"})
	if w.Code != 303 || w.Header().Get("Location") != "/signin?return_to=%2Frecipes%2F1" {
		t.Fatalf("POST: %d %s", w.Code, w.Header().Get("Location"))
	}
	w = do("POST", "/recipes/1/delete", map[string]string{"Referer": "https://evil.example.com/x"})
	if w.Header().Get("Location") != "/signin?return_to=%2F" {
		t.Fatalf("foreign referer: %s", w.Header().Get("Location"))
	}
	_ = reqctx.User
}

func TestMountPanicsOnDrift(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Mount must panic when a route has no handler")
		}
	}()
	Mount(http.NewServeMux(), map[string]http.Handler{}, nil)
}

func TestRouterFriendly404And405(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /x", func(w http.ResponseWriter, r *http.Request) {})
	h := Router(mux, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404); w.Write([]byte("custom 404")) },
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404); w.Write([]byte("api 404")) })
	for target, want := range map[string]string{"/nope": "custom 404", "/api/v1/nope": "api 404"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
		if w.Code != 404 || w.Body.String() != want {
			t.Errorf("%s: %d %q", target, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/x", nil))
	if w.Code != 405 || w.Header().Get("Allow") == "" {
		t.Fatalf("405: %d %v", w.Code, w.Header())
	}
}
