package test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/liorokman/ninjarecipe/test/harness"
)

// fakeLocalAI answers /v1/decisions with a fixed probability per category and records the last request.
func fakeLocalAI(t *testing.T, probs map[string]float64) (*httptest.Server, func() (auth string, body map[string]any)) {
	t.Helper()
	var mu sync.Mutex
	var auth string
	var last map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(b, &req)
		mu.Lock()
		auth, last = r.Header.Get("Authorization"), req
		mu.Unlock()
		var ans []map[string]any
		for _, q := range req["questions"].([]any) {
			name := q.(map[string]any)["name"].(string)
			ans = append(ans, map[string]any{"name": name, "type": "predicate", "probability": probs[name]})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": req["model"], "answers": ans})
	}))
	t.Cleanup(srv.Close)
	return srv, func() (string, map[string]any) { mu.Lock(); defer mu.Unlock(); return auth, last }
}

func TestAdminCategorizerSettingsRealUI(t *testing.T) {
	h := start(t, harness.Options{WebDir: "../web"})
	admin := h.CreateUser("Admin", "ad@e.com", true, "en")
	user := h.CreateUser("Alice", "a@e.com", false, "en")
	ac, uc := h.NewClient(h.SessionCookie(admin)), h.NewClient(h.SessionCookie(user))
	srv, seen := fakeLocalAI(t, map[string]float64{"pizza": 0.97, "vegetables": 0.3})

	// non-admins cannot read or change the engine
	for _, p := range []string{"/admin/categorizer", "/admin/categorizer/test"} {
		if r, _ := uc.PostForm(p, url.Values{"url": {srv.URL}}); r.StatusCode != 403 {
			t.Fatalf("%s as user: %d", p, r.StatusCode)
		}
	}

	r, page := ac.Get("/admin/categories")
	if r.StatusCode != 200 || !strings.Contains(page, `data-test="categorizer-form"`) || !strings.Contains(page, `name="description"`) {
		t.Fatalf("admin page: %d", r.StatusCode)
	}

	// invalid input is rejected with field errors and nothing is stored
	r, page = ac.PostForm("/admin/categorizer", url.Values{"enabled": {"1"}, "url": {"ftp://x"}, "model": {"m"}, "threshold": {"150"}})
	if r.StatusCode != 422 || strings.Count(page, `role="alert"`) < 2 {
		t.Fatalf("invalid save: %d", r.StatusCode)
	}

	// save; the key is accepted but never rendered back
	r, _ = ac.PostForm("/admin/categorizer", url.Values{"enabled": {"1"}, "url": {srv.URL + "/"}, "model": {"laya-llama-cpp"}, "api_key": {"s3cret-key"}, "threshold": {"60"}})
	if r.StatusCode != 303 {
		t.Fatalf("save: %d", r.StatusCode)
	}
	_, page = ac.Get("/admin/categories")
	if strings.Contains(page, "s3cret-key") || !strings.Contains(page, `value="`+srv.URL+`"`) || !strings.Contains(page, `value="60"`) ||
		!strings.Contains(page, `name="clear_key"`) || !strings.Contains(page, "checked") {
		t.Fatal("saved settings must show (without the key) and offer to clear it")
	}

	// test connection uses the saved URL, key, model and categories (with the admin description)
	if r, _ = ac.PostForm("/admin/categories", url.Values{"slug": {"salads"}, "name": {"Salads"}, "name_he": {"סלטים"}, "description": {"cold dishes with raw vegetables"}, "sort_order": {"5"}}); r.StatusCode != 303 {
		t.Fatalf("create category: %d", r.StatusCode)
	}
	r, page = ac.PostForm("/admin/categorizer/test", url.Values{})
	if r.StatusCode != 200 || !strings.Contains(page, `data-test="engine-test-result"`) || !strings.Contains(page, "Pizza") || !strings.Contains(page, "97%") {
		t.Fatalf("test result: %d", r.StatusCode)
	}
	auth, body := seen()
	if auth != "Bearer s3cret-key" || body["model"] != "laya-llama-cpp" {
		t.Fatalf("request: auth=%q model=%v", auth, body["model"])
	}
	if !strings.Contains(string(mustJSON(body)), "cold dishes with raw vegetables") {
		t.Fatal("category description must reach the model")
	}

	// a blank key keeps the stored one; clearing removes it; the threshold applies (0.97 passes 60%, fails 99%)
	if r, _ = ac.PostForm("/admin/categorizer", url.Values{"enabled": {"1"}, "url": {srv.URL}, "model": {"laya-llama-cpp"}, "threshold": {"99"}}); r.StatusCode != 303 {
		t.Fatalf("resave: %d", r.StatusCode)
	}
	ac.PostForm("/admin/categorizer/test", url.Values{})
	if auth, _ = seen(); auth != "Bearer s3cret-key" {
		t.Fatalf("blank key field must keep the stored key, got %q", auth)
	}
	_, page = ac.PostForm("/admin/categorizer/test", url.Values{})
	if strings.Contains(page, "97%") {
		t.Fatal("threshold 99 must exclude a 0.97 match")
	}
	ac.PostForm("/admin/categorizer", url.Values{"enabled": {"1"}, "url": {srv.URL}, "model": {"m"}, "threshold": {"50"}, "clear_key": {"1"}})
	ac.PostForm("/admin/categorizer/test", url.Values{})
	if auth, _ = seen(); auth != "" {
		t.Fatalf("cleared key must not be sent, got %q", auth)
	}

	// an unreachable engine is reported, not crashed
	ac.PostForm("/admin/categorizer", url.Values{"url": {"http://127.0.0.1:1"}, "model": {"m"}, "threshold": {"50"}})
	if r, page = ac.PostForm("/admin/categorizer/test", url.Values{}); r.StatusCode != 200 || !strings.Contains(page, "notice-error") {
		t.Fatalf("unreachable: %d", r.StatusCode)
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
