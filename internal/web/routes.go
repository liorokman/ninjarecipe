package web

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// Access levels for browser routes.
const (
	Public  = "public"
	Session = "session"
	Admin   = "admin"
)

// Route is one named browser route (40 §2). Templates build URLs with the `url` func.
type Route struct {
	Name, Method, Path, Access string
}

// Routes is the one table used by routes registration, template url funcs and tests.
var Routes = []Route{
	{"healthz", "GET", "/healthz", Public},
	{"readyz", "GET", "/readyz", Public},
	{"static", "GET", "/static/{version}/{path...}", Public},
	{"manifest", "GET", "/manifest.webmanifest", Public},
	{"sw", "GET", "/sw.js", Public},
	{"icon192", "GET", "/icon-192.png", Public},
	{"icon512", "GET", "/icon-512.png", Public},
	{"signin", "GET", "/signin", Public},
	{"auth.login", "GET", "/auth/login", Public},
	{"auth.callback", "GET", "/auth/callback", Public},
	{"auth.logout", "POST", "/auth/logout", Session},
	{"auth.loggedout", "GET", "/auth/logged-out", Public},
	{"lang", "POST", "/settings/language", Public},
	{"home", "GET", "/{$}", Session},
	{"recipes.list", "GET", "/recipes", Session},
	{"recipe.new", "GET", "/recipes/new", Session},
	{"recipe.create", "POST", "/recipes", Session},
	{"recipe.view", "GET", "/recipes/{id}", Session},
	{"recipe.cook", "GET", "/recipes/{id}/cook", Session},
	{"recipe.edit", "GET", "/recipes/{id}/edit", Session},
	{"recipe.update", "POST", "/recipes/{id}/edit", Session},
	{"recipe.delete", "POST", "/recipes/{id}/delete", Session},
	{"recipe.visibility", "POST", "/recipes/{id}/visibility", Session},
	{"recipe.copy", "POST", "/recipes/{id}/copy", Session},
	{"recipe.favourite", "POST", "/recipes/{id}/favourite", Session},
	{"cooklog.add", "POST", "/recipes/{id}/cook-logs", Session},
	{"cooklog.delete", "POST", "/recipes/{id}/cook-logs/{logID}/delete", Session},
	{"image.add", "POST", "/recipes/{id}/images", Session},
	{"image.delete", "POST", "/recipes/{id}/images/{imageID}/delete", Session},
	{"image.order", "POST", "/recipes/{id}/images/order", Session},
	{"media", "GET", "/media/{key}/{variant}", Public}, // session OR bearer, checked by the handler
	{"labels.suggest", "GET", "/labels/suggest", Session},
	{"inbox.list", "GET", "/inbox", Session},
	{"inbox.view", "GET", "/inbox/{id}", Session},
	{"inbox.edit", "GET", "/inbox/{id}/edit", Session},
	{"inbox.update", "POST", "/inbox/{id}/edit", Session},
	{"inbox.accept", "POST", "/inbox/{id}/accept", Session},
	{"inbox.reject", "POST", "/inbox/{id}/reject", Session},
	{"settings", "GET", "/settings", Session},
	{"tokens.list", "GET", "/settings/tokens", Session},
	{"tokens.create", "POST", "/settings/tokens", Session},
	{"tokens.revoke", "POST", "/settings/tokens/{id}/revoke", Session},
	{"admin.categories", "GET", "/admin/categories", Admin},
	{"admin.category.create", "POST", "/admin/categories", Admin},
	{"admin.category.update", "POST", "/admin/categories/{id}/edit", Admin},
	{"admin.category.delete", "POST", "/admin/categories/{id}/delete", Admin},
}

var routeByName = func() map[string]Route {
	m := map[string]Route{}
	for _, r := range Routes {
		m[r.Name] = r
	}
	return m
}()

// LookupRoute returns a route by name.
func LookupRoute(name string) (Route, bool) { r, ok := routeByName[name]; return r, ok }

// Pattern returns the ServeMux pattern ("POST /recipes/{id}/edit").
func (r Route) Pattern() string { return r.Method + " " + r.Path }

func displayPath(p string) string { return strings.ReplaceAll(p, "{$}", "") }

// URL builds the path for a named route, substituting {param}s in order. Panics on unknown name
// (a programming error caught by tests).
func URL(name string, args ...any) string {
	s, err := buildURL(name, args)
	if err != nil {
		panic(err)
	}
	return s
}

func buildURL(name string, args []any) (string, error) {
	r, ok := routeByName[name]
	if !ok {
		return "", fmt.Errorf("unknown route %q", name)
	}
	p := displayPath(r.Path)
	if p == "" {
		p = "/"
	}
	var out strings.Builder
	ai := 0
	for i := 0; i < len(p); {
		if p[i] != '{' {
			out.WriteByte(p[i])
			i++
			continue
		}
		j := strings.IndexByte(p[i:], '}')
		if j < 0 {
			return "", fmt.Errorf("bad route %q", r.Path)
		}
		if ai >= len(args) {
			return "", fmt.Errorf("route %q: missing argument", name)
		}
		v := fmt.Sprint(args[ai])
		if strings.HasSuffix(p[i:i+j], "...") {
			parts := strings.Split(v, "/")
			for k := range parts {
				parts[k] = url.PathEscape(parts[k])
			}
			v = strings.Join(parts, "/")
		} else {
			v = url.PathEscape(v)
		}
		out.WriteString(v)
		ai++
		i += j + 1
	}
	return out.String(), nil
}

// URLQ is URL plus query pairs: URLQ("recipes.list", "category", "pizza").
// Path params come first (as many as the route has), the rest are key/value pairs.
func URLQ(name string, args ...any) string {
	r, ok := routeByName[name]
	if !ok {
		panic(fmt.Errorf("unknown route %q", name))
	}
	n := strings.Count(r.Path, "{") - strings.Count(r.Path, "{$}")
	if n > len(args) {
		n = len(args)
	}
	base := URL(name, args[:n]...)
	rest := args[n:]
	q := url.Values{}
	for i := 0; i+1 < len(rest); i += 2 {
		q.Add(fmt.Sprint(rest[i]), fmt.Sprint(rest[i+1]))
	}
	if len(q) == 0 {
		return base
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		for _, v := range q[k] {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
	}
	return base + "?" + strings.Join(parts, "&")
}

// Mount registers handlers for every named route; it panics when a route has no handler or a handler has
// no route (guards against drift). wrap may add per-route middleware (auth by Route.Access).
func Mount(mux *http.ServeMux, handlers map[string]http.Handler, wrap func(Route, http.Handler) http.Handler) {
	for _, r := range Routes {
		h, ok := handlers[r.Name]
		if !ok {
			panic("web: no handler for route " + r.Name)
		}
		if wrap != nil {
			h = wrap(r, h)
		}
		mux.Handle(r.Pattern(), h)
	}
	for n := range handlers {
		if _, ok := routeByName[n]; !ok {
			panic("web: handler for unknown route " + n)
		}
	}
}
