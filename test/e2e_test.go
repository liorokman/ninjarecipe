package test

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liorokman/ninjarecipe/internal/jobs"
	"github.com/liorokman/ninjarecipe/test/harness"
)

func start(t *testing.T, o harness.Options) *harness.Harness {
	t.Helper()
	if o.WebDir == "" {
		o.WebDir = "testweb"
	}
	h, err := harness.Start(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h
}

// browserLogin performs the real OIDC round trip for the given IdP user and returns the signed-in client.
func browserLogin(t *testing.T, h *harness.Harness, u harness.IdPUser, returnTo string) (*harness.Client, *http.Response) {
	t.Helper()
	c := h.NewClient(nil)
	resp, _ := c.Get("/auth/login?return_to=" + url.QueryEscape(returnTo))
	if resp.StatusCode != 302 {
		t.Fatalf("login start: %d", resp.StatusCode)
	}
	h.IdP.Next = &u
	idp := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r2, err := idp.Get(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	loc, err := url.Parse(r2.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	resp, _ = c.Get(loc.RequestURI())
	return c, resp
}

func TestOIDCLoginRoundTrip(t *testing.T) {
	h := start(t, harness.Options{AllowedGroups: []string{"family"}})
	c := h.NewClient(nil)
	resp, _ := c.Get("/")
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/signin?return_to=%2F" {
		t.Fatalf("unauth: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	_, body := c.Get("/signin?return_to=%2Frecipes")
	if !strings.Contains(body, `data-test="signin-button"`) || !strings.Contains(body, "return_to=%2Frecipes") {
		t.Fatalf("signin page: %s", body)
	}
	c, resp = browserLogin(t, h, harness.IdPUser{Sub: "s1", Email: "a@example.com", Name: "Alice", Groups: []string{"family"}}, "/recipes")
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/recipes" {
		t.Fatalf("callback: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, body = c.Get("/recipes")
	if resp.StatusCode != 200 {
		t.Fatalf("recipes after login: %d %s", resp.StatusCode, body)
	}
	// user provisioned, not admin
	us, _ := h.Svc().Store.R().ListUsers(context.Background())
	if len(us) != 1 || us[0].DisplayName != "Alice" || us[0].IsAdmin || us[0].Email != "a@example.com" {
		t.Fatalf("users: %+v", us)
	}
	// logout hits the IdP end-session endpoint with id_token_hint, then lands on logged-out
	resp, _ = c.PostForm("/auth/logout", url.Values{})
	if resp.StatusCode != 302 || !strings.HasPrefix(resp.Header.Get("Location"), h.IdP.Server.URL+"/end-session") ||
		!strings.Contains(resp.Header.Get("Location"), "id_token_hint=") ||
		!strings.Contains(resp.Header.Get("Location"), url.QueryEscape(h.URL+"/auth/logged-out")) {
		t.Fatalf("logout: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, _ = c.Get("/recipes")
	if resp.StatusCode != 303 {
		t.Fatalf("after logout still signed in: %d", resp.StatusCode)
	}
	resp, body = c.Get("/auth/logged-out")
	if resp.StatusCode != 200 {
		t.Fatalf("logged-out: %d", resp.StatusCode)
	}
}

func TestOIDCGroupGateAndAdmin(t *testing.T) {
	h := start(t, harness.Options{AllowedGroups: []string{"family"}})
	_, resp := browserLogin(t, h, harness.IdPUser{Sub: "x", Email: "x@e.com", Name: "X", Groups: []string{"other"}}, "/")
	if resp.StatusCode != 403 {
		t.Fatalf("group gate: %d", resp.StatusCode)
	}
	if us, _ := h.Svc().Store.R().ListUsers(context.Background()); len(us) != 0 {
		t.Fatal("denied user must not be provisioned")
	}
	c, resp := browserLogin(t, h, harness.IdPUser{Sub: "adm", Email: "l@e.com", Name: "Lior", Groups: []string{"family", "recipes-admins"}}, "/admin/categories")
	if resp.StatusCode != 303 {
		t.Fatalf("admin login: %d", resp.StatusCode)
	}
	resp, _ = c.Get("/admin/categories")
	if resp.StatusCode != 200 {
		t.Fatalf("admin page: %d", resp.StatusCode)
	}
	// access_denied from the IdP
	c2 := h.NewClient(nil)
	r, _ := c2.Get("/auth/login")
	h.IdP.Deny = true
	idp := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r2, _ := idp.Get(r.Header.Get("Location"))
	r2.Body.Close()
	loc, _ := url.Parse(r2.Header.Get("Location"))
	resp, _ = c2.Get(loc.RequestURI())
	if resp.StatusCode != 403 {
		t.Fatalf("access_denied: %d", resp.StatusCode)
	}
}

func TestOIDCRejectsForgedAndReplayedLogin(t *testing.T) {
	h := start(t, harness.Options{})
	// unknown/expired state
	c := h.NewClient(nil)
	resp, _ := c.Get("/auth/callback?code=x&state=nope")
	if resp.StatusCode != 400 {
		t.Fatalf("unknown state: %d", resp.StatusCode)
	}
	// state is single-use
	_, resp = browserLogin(t, h, harness.IdPUser{Sub: "s", Email: "s@e.com", Name: "S"}, "/")
	if resp.StatusCode != 303 {
		t.Fatalf("login: %d", resp.StatusCode)
	}
}

func TestSessionSurvivesRestart(t *testing.T) {
	h := start(t, harness.Options{})
	c, _ := browserLogin(t, h, harness.IdPUser{Sub: "s", Email: "s@e.com", Name: "S"}, "/")
	var cookie *http.Cookie
	for _, ck := range c.HTTP.Jar.Cookies(mustURL(h.URL)) {
		if strings.Contains(ck.Name, "nwr_session") {
			cookie = ck
		}
	}
	if cookie == nil {
		t.Fatal("no session cookie")
	}
	dir, idp, addr := h.DataDir, h.IdP, strings.TrimPrefix(h.URL, "http://")
	h.Stop()
	h2, err := harness.Start(harness.Options{DataDir: dir, IdP: idp, ListenAddr: addr, WebDir: "testweb"})
	if err != nil {
		t.Fatal(err)
	}
	defer h2.Stop()
	c2 := h2.NewClient(cookie)
	resp, _ := c2.Get("/recipes")
	if resp.StatusCode != 200 {
		t.Fatalf("session lost after restart: %d", resp.StatusCode)
	}
}

func TestNoDevLoginAndHeaders(t *testing.T) {
	h := start(t, harness.Options{})
	c := h.NewClient(nil)
	for _, p := range []string{"/auth/dev-login", "/dev-login", "/api/v1/dev-login"} {
		resp, _ := c.Get(p)
		if resp.StatusCode != 404 && resp.StatusCode != 303 {
			t.Fatalf("%s: %d", p, resp.StatusCode)
		}
		if resp.StatusCode == 303 && !strings.HasPrefix(resp.Header.Get("Location"), "/signin") {
			t.Fatalf("%s: %d", p, resp.StatusCode)
		}
		if resp.Header.Get("Set-Cookie") != "" {
			t.Fatalf("%s sets a cookie", p)
		}
	}
	resp, _ := c.Get("/signin")
	wantCSP := "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; connect-src 'self'; font-src 'self'; worker-src 'self'; manifest-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self' " + strings.TrimSuffix(h.IdP.Server.URL, "/")
	if got := resp.Header.Get("Content-Security-Policy"); got != wantCSP {
		t.Fatalf("CSP:\n got %s\nwant %s", got, wantCSP)
	}
	if strings.Contains(resp.Header.Get("Content-Security-Policy"), "unsafe-inline") {
		t.Fatal("unsafe-inline")
	}
	for k, v := range map[string]string{"X-Content-Type-Options": "nosniff", "Referrer-Policy": "same-origin", "Cross-Origin-Opener-Policy": "same-origin",
		"Permissions-Policy": "camera=(self), geolocation=(), microphone=(), screen-wake-lock=(self)"} {
		if resp.Header.Get(k) != v {
			t.Fatalf("%s = %q", k, resp.Header.Get(k))
		}
	}
	if resp.Header.Get("X-Request-Id") == "" {
		t.Fatal("no request id")
	}
	// health endpoints
	resp, body := c.Get("/healthz")
	if resp.StatusCode != 200 || body != "ok" {
		t.Fatalf("healthz: %d %q", resp.StatusCode, body)
	}
	resp, body = c.Get("/readyz")
	if resp.StatusCode != 200 || !strings.Contains(body, `"db":"ok"`) || !strings.Contains(body, `"media":"ok"`) {
		t.Fatalf("readyz: %d %s", resp.StatusCode, body)
	}
	// host check
	resp, _ = c.Do("GET", "/signin", nil, map[string]string{"Host": "evil.example.com"})
	_ = resp
}

func TestBackupSubcommandSnapshot(t *testing.T) {
	h := start(t, harness.Options{})
	u := h.CreateUser("Alice", "a@e.com", false, "en")
	seedRecipe(t, h, u, "Backup me")
	out := t.TempDir()
	p, err := jobs.Backup(context.Background(), h.Srv.DB, h.Svc(), out, 2, nowUTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(strings.TrimSuffix(p, ".db") + ".media.txt"); err != nil {
		t.Fatal(err)
	}
	// prune keeps the newest N
	for i := 1; i <= 3; i++ {
		_, err = jobs.Backup(context.Background(), h.Srv.DB, h.Svc(), out, 2, nowUTC().Add(secs(i*10)))
		if err != nil {
			t.Fatal(err)
		}
	}
	m, _ := filepath.Glob(filepath.Join(out, "recipes-*.db"))
	if len(m) != 2 {
		t.Fatalf("kept %d snapshots", len(m))
	}
}
