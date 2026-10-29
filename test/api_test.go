package test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/liorokman/ninjarecipe/test/harness"
)

type problem struct {
	Type, Title, Detail, Instance string
	RequestID                     string `json:"request_id"`
	Status                        int
	ExistingID                    string `json:"existing_id"`
	InvalidParams                 []struct {
		Name, Code, Reason string
		Params             map[string]any
	} `json:"invalid_params"`
}

func api(c *harness.Client, method, path, body string, hdr ...string) (int, map[string]string, []byte) {
	h := map[string]string{"Content-Type": "application/json"}
	for i := 0; i+1 < len(hdr); i += 2 {
		h[hdr[i]] = hdr[i+1]
	}
	var r *bytes.Reader
	if body != "" {
		r = bytes.NewReader([]byte(body))
	} else {
		r = bytes.NewReader(nil)
	}
	resp, b := c.Do(method, path, r, h)
	out := map[string]string{}
	for k := range resp.Header {
		out[k] = resp.Header.Get(k)
	}
	return resp.StatusCode, out, []byte(b)
}

func TestAPIMatrix(t *testing.T) {
	h := start(t, harness.Options{})
	alice := h.CreateUser("Alice", "a@e.com", false, "en")
	bob := h.CreateUser("Bob", "b@e.com", false, "en")
	c := h.NewClient(nil)
	c.Bearer = h.Token(alice)
	readOnly := h.NewClient(nil)
	readOnly.Bearer = h.Token(alice, "recipes:read")
	writeOnly := h.NewClient(nil)
	writeOnly.Bearer = h.Token(alice, "inbox:write")
	bobC := h.NewClient(nil)
	bobC.Bearer = h.Token(bob)

	// public meta endpoint
	st, _, body := api(h.NewClient(nil), "GET", "/api/v1/meta/oven", "")
	if st != 200 || !strings.Contains(string(body), `"temp_max":370`) || !strings.Contains(string(body), `"gourmet_roast"`) {
		t.Fatalf("meta: %d %s", st, body)
	}
	// 401 without / with bad token, WWW-Authenticate + problem+json
	st, hd, body := api(h.NewClient(nil), "GET", "/api/v1/recipes", "")
	var p problem
	_ = json.Unmarshal(body, &p)
	if st != 401 || hd["Www-Authenticate"] != "Bearer" || !strings.HasPrefix(hd["Content-Type"], "application/problem+json") || !strings.HasSuffix(p.Type, "/problems/unauthorized") || p.RequestID == "" || p.Instance != "/api/v1/recipes" {
		t.Fatalf("401: %d %v %s", st, hd, body)
	}
	bad := h.NewClient(nil)
	bad.Bearer = "nwr_" + strings.Repeat("x", 43)
	if st, _, _ := api(bad, "GET", "/api/v1/recipes", ""); st != 401 {
		t.Fatalf("bad token %d", st)
	}
	// the session cookie is never accepted on the API
	cookieClient := h.NewClient(h.SessionCookie(alice))
	if st, _, _ := api(cookieClient, "GET", "/api/v1/recipes", ""); st != 401 {
		t.Fatalf("cookie on API: %d", st)
	}
	// scopes
	if st, _, _ := api(readOnly, "POST", "/api/v1/inbox", `{"title":"x"}`); st != 403 {
		t.Fatalf("scope inbox:write: %d", st)
	}
	if st, _, _ := api(writeOnly, "GET", "/api/v1/recipes", ""); st != 403 {
		t.Fatalf("scope recipes:read: %d", st)
	}
	if st, _, _ := api(writeOnly, "GET", "/api/v1/categories", ""); st != 200 {
		t.Fatalf("categories need any valid token: %d", st)
	}
	// import: 201 + ImportResult
	imp := `{"title":"Smoked short ribs","source_url":"https://example.com/ribs","categories":["beef","bbq"],"tags":["weekend"],
		"ingredients":[{"amount":1.5,"unit":"kg","item":"beef short ribs"},{"text":"2 tbsp smoked paprika"}],
		"steps":[{"text":"Rub the ribs."}],
		"oven_stages":[{"mode":"smoker","temperature_c":300,"duration_seconds":10800},{"mode":"broil","temperature_c":280,"duration_seconds":300}],
		"images":[{"data_base64":"` + base64.StdEncoding.EncodeToString(harness.JPEG(400, 300, 1)) + `","caption":"cover"},{"url":"https://127.0.0.1:1/x.jpg"}]}`
	st, _, body = api(c, "POST", "/api/v1/inbox", imp, "Idempotency-Key", "abc-1")
	var res struct {
		ID, Status, Title string
		ReviewURL         string `json:"review_url"`
		CreatedAt         string `json:"created_at"`
		Warnings          []struct {
			Name, Code string
			Params     map[string]any
		}
	}
	_ = json.Unmarshal(body, &res)
	if st != 201 || res.Status != "inbox" || res.ReviewURL != h.URL+"/inbox/"+res.ID || !strings.HasSuffix(res.CreatedAt, "Z") {
		t.Fatalf("import: %d %s", st, body)
	}
	w := map[string]string{}
	for _, x := range res.Warnings {
		w[x.Name] = x.Code
	}
	if w["categories[1]"] != "unknown_category" || w["oven_stages[0].temperature_c"] != "out_of_range" || w["images[1]"] == "" {
		t.Fatalf("warnings: %v", w)
	}
	// idempotent replay -> 200, same id
	st, _, body = api(c, "POST", "/api/v1/inbox", imp, "Idempotency-Key", "abc-1")
	var again struct{ ID string }
	_ = json.Unmarshal(body, &again)
	if st != 200 || again.ID != res.ID {
		t.Fatalf("replay: %d %s", st, body)
	}
	// duplicate source_url -> 409 with existing_id; allow_duplicate bypasses
	st, hd, body = api(c, "POST", "/api/v1/inbox", imp)
	_ = json.Unmarshal(body, &p)
	if st != 409 || p.ExistingID != res.ID || !strings.HasSuffix(p.Type, "/problems/duplicate") || !strings.HasPrefix(hd["Content-Type"], "application/problem+json") {
		t.Fatalf("duplicate: %d %s", st, body)
	}
	if st, _, _ := api(c, "POST", "/api/v1/inbox", imp, "Idempotency-Key", "abc-2"); st != 409 {
		t.Fatal("different key still duplicates")
	}
	if st, _, _ := api(c, "POST", "/api/v1/inbox?allow_duplicate=1", imp, "Idempotency-Key", "abc-3"); st != 201 {
		t.Fatalf("allow_duplicate: %d", st)
	}
	// 422 with invalid_params
	st, _, body = api(c, "POST", "/api/v1/inbox", `{"title":"","oven_stages":[{"mode":"broil","temperature_c":300,"duration_seconds":60,"woodfire":true}]}`)
	p = problem{}
	_ = json.Unmarshal(body, &p)
	names := map[string]string{}
	for _, ip := range p.InvalidParams {
		names[ip.Name] = ip.Code
		if ip.Reason == "" {
			t.Fatal("reason must be set (English)")
		}
	}
	if st != 422 || names["title"] != "required" || names["oven_stages[0].woodfire"] != "forbidden" || p.Detail != "2 fields are invalid." {
		t.Fatalf("422: %d %s", st, body)
	}
	// malformed JSON 400, bad idempotency key 400
	if st, _, _ := api(c, "POST", "/api/v1/inbox", `{nope`); st != 400 {
		t.Fatalf("bad json %d", st)
	}
	if st, _, _ := api(c, "POST", "/api/v1/inbox", `{"title":"x"}`, "Idempotency-Key", strings.Repeat("k", 101)); st != 400 {
		t.Fatal("long idempotency key")
	}
	// inbox list / withdraw
	st, _, body = api(c, "GET", "/api/v1/inbox", "")
	var list []map[string]any
	_ = json.Unmarshal(body, &list)
	if st != 200 || len(list) != 2 || list[0]["status"] != "inbox" {
		t.Fatalf("inbox list: %d %s", st, body)
	}
	if st, _, _ := api(bobC, "DELETE", "/api/v1/inbox/"+res.ID, ""); st != 404 {
		t.Fatal("bob must not withdraw alice's item")
	}
	if st, _, b := api(c, "DELETE", "/api/v1/inbox/"+res.ID, ""); st != 204 {
		u, _ := h.Svc().Store.R().GetUser(ctxb, alice.ID)
		_, e2 := h.Svc().RejectInbox(ctxb, u, res.ID, "", "")
		t.Fatalf("withdraw %d %s direct=%v", st, b, e2)
	}
	if st, _, _ := api(c, "DELETE", "/api/v1/inbox/"+res.ID, ""); st != 404 {
		t.Fatal("second withdraw")
	}
	// read API: accepted recipes only, visibility respected
	r1 := seedRecipe(t, h, alice, "Alice private")
	r2 := seedRecipe(t, h, alice, "Alice shared")
	if err := h.Svc().SetVisibility(ctxb, alice, r2.ID, true); err != nil {
		t.Fatal(err)
	}
	st, _, body = api(readOnly, "GET", "/api/v1/recipes?limit=1", "")
	var page struct {
		Items      []map[string]any `json:"items"`
		NextCursor *string          `json:"next_cursor"`
	}
	_ = json.Unmarshal(body, &page)
	if st != 200 || len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatalf("list: %d %s", st, body)
	}
	st, _, body = api(readOnly, "GET", "/api/v1/recipes?limit=1&cursor="+*page.NextCursor, "")
	var page2 struct{ Items []map[string]any }
	_ = json.Unmarshal(body, &page2)
	if st != 200 || len(page2.Items) != 1 || page2.Items[0]["id"] == page.Items[0]["id"] {
		t.Fatalf("page 2: %d %s", st, body)
	}
	bobR := h.NewClient(nil)
	bobR.Bearer = h.Token(bob, "recipes:read")
	st, _, body = api(bobR, "GET", "/api/v1/recipes", "")
	_ = json.Unmarshal(body, &page)
	if st != 200 || len(page.Items) != 1 || page.Items[0]["id"] != r2.ID {
		t.Fatalf("bob sees only the shared one: %s", body)
	}
	if st, _, _ := api(bobR, "GET", "/api/v1/recipes/"+r1.ID, ""); st != 404 {
		t.Fatal("private recipe of another user must be 404")
	}
	st, _, body = api(readOnly, "GET", "/api/v1/recipes/"+r2.ID, "")
	var full map[string]any
	_ = json.Unmarshal(body, &full)
	stages := full["oven_stages"].([]any)
	s0 := stages[0].(map[string]any)
	if st != 200 || full["version"] == nil || s0["preheat"] != true || s0["frame_level"] != "bottom" || full["visibility"] != "shared" || full["owner"] == nil {
		t.Fatalf("recipe: %d %s", st, body)
	}
	if st, _, body = api(readOnly, "GET", "/api/v1/recipes?source_url=https://nothing", ""); !strings.Contains(string(body), `"items":[]`) {
		t.Fatalf("source_url filter: %s", body)
	}
	if st, _, _ := api(readOnly, "GET", "/api/v1/recipes?limit=500", ""); st != 400 {
		t.Fatal("limit bound")
	}
	if st, _, _ := api(readOnly, "GET", "/api/v1/recipes/not-a-uuid", ""); st != 404 {
		t.Fatal("malformed id")
	}
	st, _, body = api(readOnly, "GET", "/api/v1/categories", "")
	if st != 200 || !strings.Contains(string(body), `"slug":"pizza"`) || !strings.Contains(string(body), `"name_he":"פיצה"`) {
		t.Fatalf("categories: %s", body)
	}
	// unknown API path and wrong method
	if st, _, body := api(c, "GET", "/api/v1/nope", ""); st != 404 || !strings.Contains(string(body), "problems/not-found") {
		t.Fatalf("unknown api path: %d %s", st, body)
	}
	if st, _, _ := api(c, "PUT", "/api/v1/recipes/"+r2.ID, "{}"); st != 405 {
		t.Fatalf("method: %d", st)
	}
	// a token can never edit/share/delete: those are browser-only routes
	if resp, _ := c.PostForm("/recipes/"+r2.ID+"/delete", nil); resp.StatusCode == 200 || resp.StatusCode == 204 {
		t.Fatalf("bearer must not delete: %d", resp.StatusCode)
	}
}

func TestAPIRateLimitAndBodyCap(t *testing.T) {
	h := start(t, harness.Options{})
	u := h.CreateUser("Alice", "a@e.com", false, "en")
	c := h.NewClient(nil)
	c.Bearer = h.Token(u)
	var got429 bool
	for i := 0; i < 40; i++ {
		st, hd, body := api(c, "GET", "/api/v1/categories", "")
		if st == 429 {
			got429 = true
			if hd["Retry-After"] == "" || !strings.Contains(string(body), "rate-limited") {
				t.Fatalf("429 shape: %v %s", hd, body)
			}
			break
		}
	}
	if !got429 {
		t.Fatal("burst of 20 per token must be exceeded")
	}
	// another token has its own bucket
	c2 := h.NewClient(nil)
	c2.Bearer = h.Token(u)
	if st, _, _ := api(c2, "GET", "/api/v1/categories", ""); st != 200 {
		t.Fatal("per-token bucket")
	}
}
