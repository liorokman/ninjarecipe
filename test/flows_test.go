package test

import (
	"net/url"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/liorokman/ninjarecipe/internal/domain"
	"github.com/liorokman/ninjarecipe/internal/web"
	"github.com/liorokman/ninjarecipe/test/harness"
)

// ---- tiny DOM helpers (x/net/html) ----

func parse(t testing.TB, body string) *html.Node {
	t.Helper()
	n, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func attr(n *html.Node, k string) string {
	for _, a := range n.Attr {
		if a.Key == k {
			return a.Val
		}
	}
	return ""
}

func walk(n *html.Node, f func(*html.Node)) {
	f(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, f)
	}
}

func byTest(root *html.Node, name string) []*html.Node {
	var out []*html.Node
	walk(root, func(n *html.Node) {
		if n.Type == html.ElementNode && attr(n, "data-test") == name {
			out = append(out, n)
		}
	})
	return out
}

func has(t testing.TB, body, name string) bool { return len(byTest(parse(t, body), name)) > 0 }

func fieldsOf(form *html.Node) url.Values {
	v := url.Values{}
	walk(form, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		name := attr(n, "name")
		if name == "" {
			return
		}
		switch n.Data {
		case "input":
			typ := attr(n, "type")
			if typ == "file" || typ == "submit" {
				return
			}
			if (typ == "checkbox" || typ == "radio") && !hasAttr(n, "checked") {
				return
			}
			v.Add(name, attr(n, "value"))
		case "textarea":
			if n.FirstChild != nil {
				v.Add(name, n.FirstChild.Data)
			} else {
				v.Add(name, "")
			}
		case "select":
			v.Add(name, attr(n, "value"))
		}
	})
	return v
}

func hasAttr(n *html.Node, k string) bool {
	for _, a := range n.Attr {
		if a.Key == k {
			return true
		}
	}
	return false
}

// ---- permission-flag matrix on /recipes/{id} (70 §4.3) ----

func TestPermissionFlagMatrix(t *testing.T) {
	h := start(t, harness.Options{})
	alice := h.CreateUser("Alice", "a@e.com", false, "en")
	bob := h.CreateUser("Bob", "b@e.com", false, "en")
	admin := h.CreateUser("Admin", "l@e.com", true, "en")
	priv := seedRecipe(t, h, alice, "Private")
	shared := seedRecipe(t, h, alice, "Shared")
	_ = h.Svc().SetVisibility(ctxb, alice, shared.ID, true)
	ownerless := seedRecipe(t, h, alice, "Ownerless")
	_ = h.Svc().SetVisibility(ctxb, alice, ownerless.ID, true)
	_, _ = h.Srv.DB.Writer.Exec(`UPDATE recipes SET owner_id=NULL WHERE id=?`, ownerless.ID)

	type exp struct{ edit, del, vis, copy, fav, log bool }
	all := exp{true, true, false, true, true, true}
	owner := exp{true, true, true, false, true, true}
	cases := []struct {
		name string
		u    *domain.User
		id   string
		want *exp // nil = 404
	}{
		{"owner private", alice, priv.ID, &owner},
		{"owner shared", alice, shared.ID, &owner},
		{"other shared", bob, shared.ID, &all},
		{"admin shared", admin, shared.ID, &all},
		{"ownerless", bob, ownerless.ID, &all},
		{"other private", bob, priv.ID, nil},
		{"admin private", admin, priv.ID, nil},
	}
	for _, c := range cases {
		cl := h.NewClient(h.SessionCookie(c.u))
		resp, body := cl.Get("/recipes/" + c.id)
		if c.want == nil {
			if resp.StatusCode != 404 {
				t.Errorf("%s: want 404, got %d", c.name, resp.StatusCode)
			}
			if has(t, body, "edit-link") {
				t.Errorf("%s: 404 page leaks controls", c.name)
			}
			continue
		}
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d", c.name, resp.StatusCode)
		}
		got := exp{has(t, body, "edit-link"), has(t, body, "delete-form") && has(t, body, "delete-open"), has(t, body, "visibility-switch"),
			has(t, body, "copy-form"), has(t, body, "favourite-button"), has(t, body, "cooklog-form")}
		if got != *c.want {
			t.Errorf("%s: got %+v want %+v", c.name, got, *c.want)
		}
		if !has(t, body, "visibility-badge") || !has(t, body, "start-cooking") || !has(t, body, "stage-card") {
			t.Errorf("%s: badge/start/stage missing", c.name)
		}
	}
	// permissions are enforced server-side too
	bobC := h.NewClient(h.SessionCookie(bob))
	if r, _ := bobC.PostForm("/recipes/"+shared.ID+"/visibility", url.Values{"shared": {"0"}}); r.StatusCode != 403 {
		t.Errorf("bob unshare: %d", r.StatusCode)
	}
	if r, _ := bobC.PostForm("/recipes/"+priv.ID+"/delete", nil); r.StatusCode != 404 {
		t.Errorf("bob delete private: %d", r.StatusCode)
	}
	if r, _ := bobC.Get("/recipes/" + priv.ID + "/edit"); r.StatusCode != 404 {
		t.Errorf("bob edit private: %d", r.StatusCode)
	}
	if r, _ := bobC.Get("/recipes/" + priv.ID + "/cook"); r.StatusCode != 404 {
		t.Errorf("bob cook private: %d", r.StatusCode)
	}
	// htmx visibility toggle returns the fragment
	aliceC := h.NewClient(h.SessionCookie(alice))
	r, body := aliceC.PostForm("/recipes/"+priv.ID+"/visibility", url.Values{"shared": {"1"}}, "HX-Request", "true")
	if r.StatusCode != 200 || !strings.Contains(body, `data-shared="true"`) {
		t.Errorf("htmx visibility: %d %s", r.StatusCode, body)
	}
	r, body = aliceC.PostForm("/recipes/"+priv.ID+"/favourite", url.Values{"on": {"1"}}, "HX-Request", "true")
	if r.StatusCode != 200 || !strings.Contains(body, "true") {
		t.Errorf("htmx favourite: %d %s", r.StatusCode, body)
	}
	// no-JS fallbacks redirect
	r, _ = aliceC.PostForm("/recipes/"+priv.ID+"/favourite", url.Values{"on": {"0"}})
	if r.StatusCode != 303 {
		t.Errorf("favourite fallback: %d", r.StatusCode)
	}
}

// ---- story flows ----

func TestStoryCreateEditShareDelete(t *testing.T) {
	h := start(t, harness.Options{})
	alice := h.CreateUser("Alice", "a@e.com", false, "en")
	bob := h.CreateUser("Bob", "b@e.com", false, "en")
	a, b := h.NewClient(h.SessionCookie(alice)), h.NewClient(h.SessionCookie(bob))

	_, body := a.Get("/recipes/new")
	form := fieldsOf(findByID(parse(t, body), "recipe-form"))
	form.Set("title", "Smoked ribs")
	form.Set("ingredients", "# Rub\n2 tbsp paprika\nsalt")
	form.Set("steps", "Rub [10:00]\nSmoke")
	form.Set("stage[0].mode", "smoker")
	form.Set("stage[0].temperature_c", "120")
	form.Set("stage[0].duration_seconds", "10800")
	form.Add("category", "beef")
	form.Set("tags", "BBQ, weekend")
	// invalid first: 422 with inline errors and input preserved
	bad := cloneValues(form)
	bad.Set("stage[0].temperature_c", "900")
	bad.Set("title", "")
	resp, body := a.PostForm("/recipes", bad)
	if resp.StatusCode != 422 || !strings.Contains(body, `data-field="title"`) || !strings.Contains(body, `data-field="stage[0].temperature_c"`) || !strings.Contains(body, "Between 40 and 370") || !strings.Contains(body, "paprika") {
		t.Fatalf("422 page: %d %s", resp.StatusCode, body)
	}
	// JSON submit failure returns the form fragment only
	resp, body = a.PostForm("/recipes", bad, "Accept", "application/json")
	if resp.StatusCode != 422 || strings.Contains(body, "<html") || !strings.Contains(body, `id="recipe-form"`) || !strings.Contains(body, `data-test="error-summary"`) {
		t.Fatalf("422 fragment: %d %s", resp.StatusCode, body)
	}
	// create (JSON submit)
	resp, body = a.PostForm("/recipes", form, "Accept", "application/json")
	if resp.StatusCode != 200 || !strings.Contains(body, `"version":1`) {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}
	id := regexp.MustCompile(`"id":"([0-9a-f-]+)"`).FindStringSubmatch(body)[1]
	resp, body = a.Get("/recipes/" + id)
	if resp.StatusCode != 200 || !strings.Contains(body, "Smoked ribs") || !strings.Contains(body, "smoker") || !strings.Contains(body, `flash`) {
		t.Fatalf("view: %d", resp.StatusCode)
	}
	// edit title and stage, version in the form, 303 for plain submit
	_, body = a.Get("/recipes/" + id + "/edit")
	edit := fieldsOf(findByID(parse(t, body), "recipe-form"))
	if edit.Get("version") != "1" || edit.Get("title") != "Smoked ribs" || edit.Get("stage[0].temperature_c") != "120" {
		t.Fatalf("edit prefill: %v", edit)
	}
	edit.Set("title", "Smoked short ribs")
	edit.Set("stage[0].temperature_c", "125")
	resp, _ = a.PostForm("/recipes/"+id+"/edit", edit)
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/recipes/"+id {
		t.Fatalf("update: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	// stale version -> 409 fragment (fetch) and page (plain)
	resp, body = a.PostForm("/recipes/"+id+"/edit", edit, "Accept", "application/json")
	if resp.StatusCode != 409 || !strings.Contains(body, `id="conflict"`) || strings.Contains(body, "<html") {
		t.Fatalf("409 fetch: %d %s", resp.StatusCode, body)
	}
	resp, body = a.PostForm("/recipes/"+id+"/edit", edit)
	if resp.StatusCode != 409 || !strings.Contains(body, `data-code="conflict"`) {
		t.Fatalf("409 page: %d %s", resp.StatusCode, body)
	}
	// share, bob edits, owner sees last changed by Bob
	if r, _ := a.PostForm("/recipes/"+id+"/visibility", url.Values{"shared": {"1"}}); r.StatusCode != 303 {
		t.Fatal("share")
	}
	_, body = b.Get("/recipes/" + id + "/edit")
	be := fieldsOf(findByID(parse(t, body), "recipe-form"))
	be.Set("title", "Bob's tweak")
	if r, _ := b.PostForm("/recipes/"+id+"/edit", be); r.StatusCode != 303 {
		t.Fatalf("bob edit: %d", r.StatusCode)
	}
	_, body = a.Get("/recipes/" + id)
	if !has(t, body, "last-changed") || !strings.Contains(body, "Bob") || !strings.Contains(body, "Bob&#39;s tweak") && !strings.Contains(body, "Bob's tweak") {
		t.Fatalf("last changed: %s", body)
	}
	// bob copies (private copy opens in the editor)
	resp, _ = b.PostForm("/recipes/"+id+"/copy", nil)
	if resp.StatusCode != 303 || !strings.HasSuffix(resp.Header.Get("Location"), "/edit") {
		t.Fatalf("copy: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	// search finds it for the family
	_, body = b.Get("/recipes?q=tweak")
	if !has(t, body, "results-card") {
		t.Fatal("search")
	}
	// delete (bob may delete a shared recipe), flash on the list, gone
	resp, _ = b.PostForm("/recipes/"+id+"/delete", nil)
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/recipes" {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if r, _ := a.Get("/recipes/" + id); r.StatusCode != 404 {
		t.Fatal("deleted recipe still there")
	}
}

func cloneValues(v url.Values) url.Values {
	o := url.Values{}
	for k, vs := range v {
		o[k] = append([]string(nil), vs...)
	}
	return o
}

func findByID(root *html.Node, id string) *html.Node {
	var out *html.Node
	walk(root, func(n *html.Node) {
		if n.Type == html.ElementNode && attr(n, "id") == id && out == nil {
			out = n
		}
	})
	return out
}

func TestStoryInboxImportEditKeep(t *testing.T) {
	h := start(t, harness.Options{})
	alice := h.CreateUser("Alice", "a@e.com", false, "en")
	a := h.NewClient(h.SessionCookie(alice))
	api := h.NewClient(nil)
	api.Bearer = h.Token(alice)
	for _, title := range []string{"First import", "Second import"} {
		st, _, _ := apiCall(api, "POST", "/api/v1/inbox", `{"title":"`+title+`","ingredients":[{"text":"1 kg lamb"}],"steps":[{"text":"roast"}]}`)
		if st != 201 {
			t.Fatal(st)
		}
	}
	_, body := a.Get("/inbox")
	cards := byTest(parse(t, body), "inbox-card")
	if len(cards) != 2 {
		t.Fatalf("cards %d", len(cards))
	}
	_, home := a.Get("/")
	if !has(t, home, "inbox-badge") {
		t.Fatal("badge")
	}
	first := attr(cards[0], "href")
	resp, body := a.Get(first)
	if resp.StatusCode != 200 || !has(t, body, "missing-oven") || !has(t, body, "inbox-keep") || !has(t, body, "inbox-edit") || !has(t, body, "inbox-reject") {
		t.Fatalf("inbox item: %d", resp.StatusCode)
	}
	// edit then keep
	_, body = a.Get(first + "/edit")
	root := parse(t, body)
	f := fieldsOf(findByID(root, "recipe-form"))
	f.Set("title", "Lamb, fixed")
	f.Set("stage[0].mode", "bake")
	f.Set("stage[0].temperature_c", "200")
	f.Set("stage[0].duration_seconds", "1800")
	resp, _ = a.PostForm(first+"/edit", f)
	if resp.StatusCode != 303 || !strings.HasPrefix(resp.Header.Get("Location"), "/inbox/") {
		t.Fatalf("edit+keep: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	next := resp.Header.Get("Location")
	// kept item is a normal recipe and searchable
	resp, _ = a.Get(first)
	if resp.StatusCode != 303 || !strings.HasPrefix(resp.Header.Get("Location"), "/recipes/") {
		t.Fatalf("kept item should redirect to its recipe page: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	_, body = a.Get("/recipes?q=fixed")
	if !has(t, body, "results-card") {
		t.Fatal("kept recipe not searchable")
	}
	// reject the last one: redirect to the empty inbox with a flash
	resp, _ = a.PostForm(next+"/reject", nil)
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/inbox" {
		t.Fatalf("reject: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	_, body = a.Get("/inbox")
	if has(t, body, "inbox-card") || !strings.Contains(body, "All done") {
		t.Fatal("inbox should be empty with flash")
	}
	// another user's inbox item is invisible
	bob := h.CreateUser("Bob", "b@e.com", false, "en")
	st, _, b := apiCall(api, "POST", "/api/v1/inbox", `{"title":"Private to alice"}`)
	_ = st
	id := regexp.MustCompile(`"id":"([0-9a-f-]+)"`).FindStringSubmatch(string(b))[1]
	bc := h.NewClient(h.SessionCookie(bob))
	for _, p := range []string{"/inbox/" + id, "/inbox/" + id + "/edit", "/recipes/" + id} {
		if r, _ := bc.Get(p); r.StatusCode != 404 {
			t.Errorf("%s: %d", p, r.StatusCode)
		}
	}
	if r, _ := bc.PostForm("/inbox/"+id+"/accept", nil); r.StatusCode != 404 {
		t.Errorf("accept other's: %d", r.StatusCode)
	}
}

func apiCall(c *harness.Client, method, path, body string) (int, map[string]string, []byte) {
	return api(c, method, path, body)
}

func TestSessionExpiryAndHTMXUnauth(t *testing.T) {
	h := start(t, harness.Options{})
	alice := h.CreateUser("Alice", "a@e.com", false, "en")
	cookie := h.SessionCookie(alice)
	c := h.NewClient(cookie)
	_, _ = h.Srv.DB.Writer.Exec(`UPDATE sessions SET expires_at='2000-01-01T00:00:00.000Z'`)
	resp, _ := c.PostForm("/recipes", url.Values{"title": {"x"}}, "Referer", h.URL+"/recipes/new")
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/signin?return_to=%2Frecipes%2Fnew" {
		t.Fatalf("expired POST: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, _ = c.Get("/recipes?q=a", "HX-Request", "true", "HX-Current-URL", h.URL+"/recipes?q=a")
	if resp.StatusCode != 401 || resp.Header.Get("HX-Redirect") != "/signin?return_to=%2Frecipes%3Fq%3Da" {
		t.Fatalf("htmx: %d %s", resp.StatusCode, resp.Header.Get("HX-Redirect"))
	}
	// sliding expiry: a valid session older than 10 min gets its expiry extended
	c2 := h.NewClient(h.SessionCookie(alice))
	_, _ = h.Srv.DB.Writer.Exec(`UPDATE sessions SET last_seen_at='2020-01-01T00:00:00.000Z' WHERE expires_at > '2026'`)
	c2.Get("/recipes")
	var seen string
	h.Srv.DB.Reader.QueryRow(`SELECT MAX(last_seen_at) FROM sessions`).Scan(&seen)
	if seen < "2026" {
		t.Fatalf("last_seen not refreshed: %s", seen)
	}
}

func TestCSRFAndSignedOutRedirects(t *testing.T) {
	h := start(t, harness.Options{})
	alice := h.CreateUser("Alice", "a@e.com", false, "en")
	c := h.NewClient(h.SessionCookie(alice))
	r := seedRecipe(t, h, alice, "Mine")
	resp, _ := c.PostForm("/recipes/"+r.ID+"/delete", nil, "Sec-Fetch-Site", "cross-site")
	if resp.StatusCode != 403 {
		t.Fatalf("cross-site POST: %d", resp.StatusCode)
	}
	resp, _ = c.PostForm("/recipes/"+r.ID+"/delete", nil, "Origin", "https://evil.example.com", "Sec-Fetch-Site", "")
	if resp.StatusCode != 403 {
		t.Fatalf("foreign origin: %d", resp.StatusCode)
	}
	// language switch is public and validates return_to
	anon := h.NewClient(nil)
	resp, _ = anon.PostForm("/settings/language", url.Values{"lang": {"he"}, "return_to": {"//evil.com"}})
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/" {
		t.Fatalf("lang: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	_, body := anon.Get("/signin")
	if !strings.Contains(body, `dir="rtl"`) {
		t.Fatal("lang cookie should switch to Hebrew")
	}
	// logged-in language change persists on the user
	if resp, _ := c.PostForm("/settings/language", url.Values{"lang": {"he"}, "return_to": {"/recipes"}}); resp.StatusCode != 303 || resp.Header.Get("Location") != "/recipes" {
		t.Fatal("lang logged in")
	}
	u, _ := h.Svc().Store.R().GetUser(ctxb, alice.ID)
	if u.UILanguage != "he" {
		t.Fatalf("ui_language %q", u.UILanguage)
	}
}

// ---- rendered-link crawl: every link / form of every page resolves ----

func TestRenderedLinkCrawl(t *testing.T) {
	h := start(t, harness.Options{})
	seed, err := h.SeedUI()
	if err != nil {
		t.Fatal(err)
	}
	pages := []string{"/", "/recipes", "/recipes/new", "/recipes/" + seed.Recipes["ribs"], "/recipes/" + seed.Recipes["ribs"] + "/cook",
		"/recipes/" + seed.Recipes["ribs"] + "/edit", "/recipes/" + seed.Recipes["chicken"], "/inbox", "/inbox/" + seed.Recipes["inbox1"],
		"/inbox/" + seed.Recipes["inbox1"] + "/edit", "/settings", "/settings/tokens", "/admin/categories", "/signin", "/auth/logged-out"}
	for who, user := range map[string]string{"owner": "alice", "other": "bob", "admin": "admin"} {
		c := h.NewClient(h.SessionCookie(seed.Users[user]))
		for _, p := range pages {
			resp, body := c.Get(p)
			if resp.StatusCode >= 500 {
				t.Errorf("%s %s: %d", who, p, resp.StatusCode)
				continue
			}
			if resp.StatusCode != 200 {
				continue // other users legitimately get 404/403/303 on some pages
			}
			if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "script-src 'self';") || strings.Contains(resp.Header.Get("Content-Security-Policy"), "unsafe-inline") {
				t.Errorf("%s %s: CSP", who, p)
			}
			root := parse(t, body)
			walk(root, func(n *html.Node) {
				if n.Type != html.ElementNode {
					return
				}
				switch n.Data {
				case "a":
					href := attr(n, "href")
					if !strings.HasPrefix(href, "/") || strings.HasPrefix(href, "//") || strings.HasPrefix(href, "/static/") || strings.HasPrefix(href, "/media/") || strings.HasPrefix(href, "/auth/") {
						return
					}
					r, _ := c.Get(href)
					if r.StatusCode == 404 && who == "owner" || r.StatusCode == 405 || r.StatusCode >= 500 {
						t.Errorf("%s %s: link %s -> %d", who, p, href, r.StatusCode)
					}
				case "form":
					act := attr(n, "action")
					m := strings.ToUpper(attr(n, "method"))
					if act == "" || m == "" || act == web.URL("auth.logout") || strings.HasPrefix(act, "/settings/language") {
						return
					}
					if m == "GET" {
						if r, _ := c.Get(act); r.StatusCode == 404 || r.StatusCode == 405 || r.StatusCode >= 500 {
							t.Errorf("%s %s: GET form %s -> %d", who, p, act, r.StatusCode)
						}
						return
					}
					if strings.HasSuffix(act, "/delete") || strings.HasSuffix(act, "/reject") || strings.HasSuffix(act, "/revoke") || strings.HasSuffix(act, "/accept") {
						// destructive: only check the route exists (405 would mean unregistered)
						req, _ := c.HTTP.Head(h.URL + act)
						if req != nil {
							req.Body.Close()
							if req.StatusCode == 404 && who == "owner" && !strings.Contains(act, "/recipes/") {
								t.Errorf("%s %s: form %s unregistered", who, p, act)
							}
						}
						return
					}
					r, _ := c.PostForm(act, fieldsOf(n))
					if r.StatusCode == 405 || r.StatusCode >= 500 || (r.StatusCode == 404 && who == "owner") {
						t.Errorf("%s %s: POST form %s -> %d", who, p, act, r.StatusCode)
					}
				}
			})
		}
	}
}
