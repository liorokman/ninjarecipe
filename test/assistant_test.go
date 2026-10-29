package test

import (
	"encoding/json"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/liorokman/ninjarecipe/internal/mcp"
	"github.com/liorokman/ninjarecipe/test/harness"
)

func inputsIn(n *html.Node) []*html.Node {
	var out []*html.Node
	walk(n, func(x *html.Node) {
		if x.Type == html.ElementNode && x.Data == "input" {
			out = append(out, x)
		}
	})
	return out
}

func submit(t *testing.T, c *harness.Client, body string, hdr ...string) (int, map[string]any) {
	t.Helper()
	st, _, b := api(c, "POST", "/api/v1/inbox", body, hdr...)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return st, m
}

// Story: the assistant submits, the household throws away / keeps, the assistant learns and is not offered the same page again.
func TestAssistantFeedbackLoopOverREST(t *testing.T) {
	h := start(t, harness.Options{})
	alice := h.CreateUser("Alice", "a@e.com", false, "en")
	c := h.NewClient(nil)
	c.Bearer = h.Token(alice)
	readOnly := h.NewClient(nil)
	readOnly.Bearer = h.Token(alice, "recipes:read")
	a := h.NewClient(h.SessionCookie(alice))

	st, first := submit(t, c, `{"title":"Ribs","source_url":"https://www.example.com/ribs/?utm_source=x"}`)
	st2, second := submit(t, c, `{"title":"Pizza","source_url":"https://example.com/pizza"}`)
	if st != 201 || st2 != 201 {
		t.Fatalf("submit: %d %d", st, st2)
	}
	// the household throws the first away with a reason and keeps the second
	resp, _ := a.PostForm("/inbox/"+first["id"].(string)+"/reject", url.Values{"reason": {"too_complicated"}, "note": {"two days of prep"}})
	if resp.StatusCode != 303 {
		t.Fatalf("reject: %d", resp.StatusCode)
	}
	if resp, _ = a.PostForm("/inbox/"+second["id"].(string)+"/accept", url.Values{"share": {"1"}}); resp.StatusCode != 303 {
		t.Fatalf("accept: %d", resp.StatusCode)
	}

	// the same page, differently written, is refused with the earlier decision
	st, _, body := api(c, "POST", "/api/v1/inbox?allow_duplicate=1", `{"title":"Ribs 2","source_url":"http://example.com/ribs"}`)
	var p map[string]any
	_ = json.Unmarshal(body, &p)
	if st != 409 || !strings.HasSuffix(p["type"].(string), "/problems/previously-reviewed") || p["outcome"] != "rejected" || p["reason"] != "too_complicated" || p["decided_at"] == "" {
		t.Fatalf("previously-reviewed: %d %s", st, body)
	}
	// a kept source: rule 2 first (duplicate), then rule 3 once the recipe is gone
	st, _, body = api(c, "POST", "/api/v1/inbox", `{"title":"Pizza","source_url":"https://example.com/pizza/"}`)
	if st != 409 || !strings.Contains(string(body), "/problems/duplicate") || !strings.Contains(string(body), `"existing_id"`) {
		t.Fatalf("duplicate: %d %s", st, body)
	}
	st, _, body = api(c, "POST", "/api/v1/inbox?allow_duplicate=1", `{"title":"Pizza","source_url":"https://example.com/pizza/"}`)
	if st != 409 || !strings.Contains(string(body), `"outcome":"kept"`) || !strings.Contains(string(body), `"reason":null`) {
		t.Fatalf("kept: %d %s", st, body)
	}
	// search by the same normalized key
	if _, _, body = api(readOnly, "GET", "/api/v1/recipes?source_url=http://www.example.com/pizza", ""); !strings.Contains(string(body), second["id"].(string)) {
		t.Fatalf("source_url search: %s", body)
	}

	// outcomes feed
	st, _, body = api(c, "GET", "/api/v1/inbox/outcomes", "")
	var feed struct {
		Items []struct {
			Cursor    string
			ItemID    string  `json:"item_id"`
			RecipeID  *string `json:"recipe_id"`
			Title     string
			SourceURL *string `json:"source_url"`
			Outcome   string
			Reason    *string
			Note      string
			Edited    bool
			Shared    bool
			DecidedAt string `json:"decided_at"`
		}
		NextCursor *string `json:"next_cursor"`
	}
	_ = json.Unmarshal(body, &feed)
	if st != 200 || len(feed.Items) != 2 || feed.NextCursor == nil || *feed.NextCursor != feed.Items[1].Cursor {
		t.Fatalf("outcomes: %d %s", st, body)
	}
	o0, o1 := feed.Items[0], feed.Items[1]
	if o0.Outcome != "rejected" || o0.Reason == nil || *o0.Reason != "too_complicated" || o0.Note != "two days of prep" || o0.RecipeID != nil ||
		o0.SourceURL == nil || *o0.SourceURL != "https://www.example.com/ribs/?utm_source=x" || o0.DecidedAt == "" {
		t.Fatalf("rejected item: %+v", o0)
	}
	if o1.Outcome != "kept" || !o1.Shared || o1.Edited || o1.Reason != nil || o1.RecipeID == nil || *o1.RecipeID != second["id"] {
		t.Fatalf("kept item: %+v", o1)
	}
	// resume from the cursor: nothing new, cursor echoed
	_, _, body = api(c, "GET", "/api/v1/inbox/outcomes?after="+o1.Cursor, "")
	if !strings.Contains(string(body), `"items":[]`) || !strings.Contains(string(body), `"next_cursor":"`+o1.Cursor+`"`) {
		t.Fatalf("resume: %s", body)
	}
	if st, _, _ := api(c, "GET", "/api/v1/inbox/outcomes?after=zzz", ""); st != 400 {
		t.Fatalf("bad cursor: %d", st)
	}
	if st, _, _ := api(c, "GET", "/api/v1/inbox/outcomes?limit=0", ""); st != 400 {
		t.Fatalf("bad limit: %d", st)
	}
	if st, _, _ := api(readOnly, "GET", "/api/v1/inbox/outcomes", ""); st != 403 {
		t.Fatalf("outcomes need inbox:write: %d", st)
	}
	// withdrawing through the API records nothing
	_, third := submit(t, c, `{"title":"Third","source_url":"https://example.com/third"}`)
	if st, _, _ := api(c, "DELETE", "/api/v1/inbox/"+third["id"].(string), ""); st != 204 {
		t.Fatal("withdraw")
	}
	if _, _, body = api(c, "GET", "/api/v1/inbox/outcomes", ""); strings.Contains(string(body), "Third") {
		t.Fatalf("withdraw must not record an outcome: %s", body)
	}
	if st, _ := submit(t, c, `{"title":"Third","source_url":"https://example.com/third"}`); st != 201 {
		t.Fatal("a withdrawn source may be submitted again")
	}
	// another user has their own feed
	bob := h.CreateUser("Bob", "b@e.com", false, "en")
	bc := h.NewClient(nil)
	bc.Bearer = h.Token(bob)
	if _, _, body = api(bc, "GET", "/api/v1/inbox/outcomes", ""); !strings.Contains(string(body), `"items":[]`) || !strings.Contains(string(body), `"next_cursor":null`) {
		t.Fatalf("bob's feed: %s", body)
	}
}

func TestAssistantBriefOverRESTAndWeb(t *testing.T) {
	h := start(t, harness.Options{WebDir: "../web"}) // real templates
	alice := h.CreateUser("Alice", "a@e.com", false, "he")
	a := h.NewClient(h.SessionCookie(alice))
	writeOnly := h.NewClient(nil)
	writeOnly.Bearer = h.Token(alice, "inbox:write")

	st, _, body := api(writeOnly, "GET", "/api/v1/assistant/brief", "")
	if st != 200 || !strings.Contains(string(body), `"text":""`) || !strings.Contains(string(body), `"language":"he"`) || !strings.Contains(string(body), `"updated_at":null`) {
		t.Fatalf("empty brief (any token works): %d %s", st, body)
	}
	if st, _, _ := api(h.NewClient(nil), "GET", "/api/v1/assistant/brief", ""); st != 401 {
		t.Fatalf("brief needs a token: %d", st)
	}

	_, page := a.Get("/settings/tokens")
	if !has(t, page, "brief-form") || !has(t, page, "brief-save") {
		t.Fatal("tokens page must show the brief form")
	}
	root := parse(t, page)
	f := fieldsOf(findByID(root, "brief-text").Parent)
	f.Set("text", "More smoked fish. Nothing with pork.")
	resp, _ := a.PostForm("/settings/assistant-brief", f)
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/settings/tokens" {
		t.Fatalf("save: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	_, page = a.Get("/settings/tokens")
	if !strings.Contains(page, "More smoked fish. Nothing with pork.") {
		t.Fatal("saved text must be shown again")
	}
	_, _, body = api(writeOnly, "GET", "/api/v1/assistant/brief", "")
	var b map[string]any
	_ = json.Unmarshal(body, &b)
	if b["text"] != "More smoked fish. Nothing with pork." || b["updated_at"] == nil {
		t.Fatalf("brief over REST: %s", body)
	}
	// too long: the page comes back with the typed text and an error, nothing is saved
	long := strings.Repeat("x", 2001)
	resp, page = a.PostForm("/settings/assistant-brief", url.Values{"text": {long}})
	if resp.StatusCode != 422 || !strings.Contains(page, long) || !strings.Contains(page, `role="alert"`) {
		t.Fatalf("too long: %d", resp.StatusCode)
	}
	_, _, body = api(writeOnly, "GET", "/api/v1/assistant/brief", "")
	if !strings.Contains(string(body), "More smoked fish") {
		t.Fatal("a rejected brief must not replace the stored one")
	}
}

func TestInboxThrowAwayFormReal(t *testing.T) {
	h := start(t, harness.Options{WebDir: "../web"})
	alice := h.CreateUser("Alice", "a@e.com", false, "en")
	c := h.NewClient(nil)
	c.Bearer = h.Token(alice)
	a := h.NewClient(h.SessionCookie(alice))
	_, it := submit(t, c, `{"title":"Ribs","source_url":"https://example.com/ribs"}`)
	id := it["id"].(string)

	_, page := a.Get("/inbox/" + id)
	root := parse(t, page)
	form := byTest(root, "reject-form")
	if len(form) != 1 || attr(form[0], "action") != "/inbox/"+id+"/reject" {
		t.Fatalf("reject form: %d", len(form))
	}
	chips := byTest(root, "reject-reasons")
	if len(chips) != 1 {
		t.Fatal("reason chips")
	}
	var seen []string
	for _, in := range inputsIn(chips[0]) {
		if attr(in, "type") != "radio" || attr(in, "name") != "reason" || !hasAttr(in, "data-clearable") || hasAttr(in, "required") || hasAttr(in, "checked") {
			t.Fatalf("a reason must be an optional, unselected, clearable radio: %+v", in.Attr)
		}
		seen = append(seen, attr(in, "value"))
	}
	want := []string{"not_our_taste", "too_complicated", "not_for_our_oven", "already_have", "poor_details"}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("reasons %v want %v", seen, want)
	}
	if !has(t, page, "reject-note") || !has(t, page, "reject-confirm") {
		t.Fatal("note field and confirm button")
	}

	// a reason is never required: throwing away with nothing chosen works
	resp, _ := a.PostForm("/inbox/"+id+"/reject", url.Values{})
	if resp.StatusCode != 303 {
		t.Fatalf("plain reject: %d", resp.StatusCode)
	}
	_, _, body := api(c, "GET", "/api/v1/inbox/outcomes", "")
	if !strings.Contains(string(body), `"outcome":"rejected"`) || !strings.Contains(string(body), `"reason":null`) {
		t.Fatalf("outcome without reason: %s", body)
	}
	// an unknown reason is treated as none
	_, it = submit(t, c, `{"title":"Lamb"}`)
	if resp, _ = a.PostForm("/inbox/"+it["id"].(string)+"/reject", url.Values{"reason": {"<script>"}}); resp.StatusCode != 303 {
		t.Fatalf("unknown reason: %d", resp.StatusCode)
	}
	_, _, body = api(c, "GET", "/api/v1/inbox/outcomes", "")
	if strings.Contains(string(body), "script") {
		t.Fatalf("unknown reason must not be stored: %s", body)
	}
}

// ---- OpenAPI ----

func TestOpenAPIServedAndInStepWithRoutes(t *testing.T) {
	h := start(t, harness.Options{})
	st, hd, body := api(h.NewClient(nil), "GET", "/api/v1/openapi.json", "")
	if st != 200 || hd["Cache-Control"] != "no-cache" || !strings.HasPrefix(hd["Content-Type"], "application/json") {
		t.Fatalf("openapi: %d %v", st, hd)
	}
	var doc struct {
		OpenAPI string `json:"openapi"`
		Servers []struct{ URL string }
		Paths   map[string]map[string]json.RawMessage
	}
	if err := json.Unmarshal(body, &doc); err != nil || doc.OpenAPI != "3.1.0" || len(doc.Servers) != 1 || doc.Servers[0].URL != h.URL+"/api/v1" {
		t.Fatalf("doc: %v %+v", err, doc.Servers)
	}
	var have []string
	for p, ms := range doc.Paths {
		for m := range ms {
			have = append(have, strings.ToUpper(m)+" "+p)
		}
	}
	sort.Strings(have)
	want := []string{"DELETE /inbox/{id}", "GET /assistant/brief", "GET /categories", "GET /inbox", "GET /inbox/outcomes", "GET /meta/oven",
		"GET /recipes", "GET /recipes/{id}", "POST /inbox"}
	sort.Strings(want)
	if !reflect.DeepEqual(have, want) {
		t.Fatalf("documented operations\n have %v\n want %v", have, want)
	}
}

// ---- MCP ----

type rpcResp struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int
		Message string
	} `json:"error"`
}

func rpc(t *testing.T, c *harness.Client, method string, params any) (int, rpcResp) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	st, _, out := api(c, "POST", "/api/v1/mcp", string(b), "Accept", "application/json, text/event-stream")
	var r rpcResp
	_ = json.Unmarshal(out, &r)
	return st, r
}

type toolResult struct {
	IsError bool `json:"isError"`
	Content []struct{ Type, Text string }
}

func callTool(t *testing.T, c *harness.Client, name string, args any) toolResult {
	t.Helper()
	st, r := rpc(t, c, "tools/call", map[string]any{"name": name, "arguments": args})
	if st != 200 || r.Error != nil {
		t.Fatalf("tools/call %s: %d %+v", name, st, r.Error)
	}
	var tr toolResult
	if err := json.Unmarshal(r.Result, &tr); err != nil || len(tr.Content) != 1 || tr.Content[0].Type != "text" {
		t.Fatalf("tools/call %s result: %v %s", name, err, r.Result)
	}
	return tr
}

func toolNames(t *testing.T, c *harness.Client) []string {
	t.Helper()
	st, r := rpc(t, c, "tools/list", map[string]any{})
	if st != 200 || r.Error != nil {
		t.Fatalf("tools/list: %d %+v", st, r.Error)
	}
	var l struct{ Tools []struct{ Name string } }
	_ = json.Unmarshal(r.Result, &l)
	var out []string
	for _, x := range l.Tools {
		out = append(out, x.Name)
	}
	sort.Strings(out)
	return out
}

func TestMCPServer(t *testing.T) {
	// the test makes more than the default burst of 20 requests per token; raise the refill rate
	h := start(t, harness.Options{Env: map[string]string{"API_RATE_LIMIT_PER_MIN": "600000"}})
	alice := h.CreateUser("Alice", "a@e.com", false, "en")
	full := h.NewClient(nil)
	full.Bearer = h.Token(alice)
	readOnly := h.NewClient(nil)
	readOnly.Bearer = h.Token(alice, "recipes:read")
	writeOnly := h.NewClient(nil)
	writeOnly.Bearer = h.Token(alice, "inbox:write")

	// transport: POST only, bearer required
	if st, _, _ := api(full, "GET", "/api/v1/mcp", ""); st != 405 {
		t.Fatalf("GET /mcp: %d", st)
	}
	if st, _, _ := api(full, "DELETE", "/api/v1/mcp", ""); st != 405 {
		t.Fatalf("DELETE /mcp: %d", st)
	}
	if st, _, _ := api(h.NewClient(nil), "POST", "/api/v1/mcp", `{}`); st != 401 {
		t.Fatalf("anonymous /mcp: %d", st)
	}

	// initialize carries the instructions
	st, r := rpc(t, full, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "1"}})
	var init struct {
		Instructions string
		ServerInfo   struct{ Name string }
	}
	_ = json.Unmarshal(r.Result, &init)
	if st != 200 || init.Instructions != mcp.Instructions || init.ServerInfo.Name != "ninja-recipes" {
		t.Fatalf("initialize: %d %s", st, r.Result)
	}

	// tools/list is filtered by scope
	all := mcp.Names()
	sort.Strings(all)
	if got := toolNames(t, full); !reflect.DeepEqual(got, all) {
		t.Fatalf("full token tools %v want %v", got, all)
	}
	wantRO := []string{"get_assistant_brief", "get_oven_reference", "get_recipe", "list_categories", "search_recipes"}
	if got := toolNames(t, readOnly); !reflect.DeepEqual(got, wantRO) {
		t.Fatalf("recipes:read tools %v want %v", got, wantRO)
	}
	wantWO := []string{"get_assistant_brief", "get_oven_reference", "list_categories", "list_inbox", "list_outcomes", "submit_recipe", "withdraw_inbox_item"}
	if got := toolNames(t, writeOnly); !reflect.DeepEqual(got, wantWO) {
		t.Fatalf("inbox:write tools %v want %v", got, wantWO)
	}
	// a tool hidden from the token cannot be called either
	if st, r := rpc(t, readOnly, "tools/call", map[string]any{"name": "submit_recipe", "arguments": map[string]any{"recipe": map[string]any{"title": "x"}}}); st == 200 && r.Error == nil {
		var tr toolResult
		_ = json.Unmarshal(r.Result, &tr)
		if !tr.IsError {
			t.Fatalf("submit_recipe without scope must fail: %s", r.Result)
		}
	}

	// every tool returns what its REST twin returns
	same := func(tool string, args any, method, path, body string) {
		t.Helper()
		tr := callTool(t, full, tool, args)
		st, _, rest := api(full, method, path, body)
		if tr.IsError != (st >= 400) {
			t.Fatalf("%s: isError=%v rest=%d", tool, tr.IsError, st)
		}
		var a, b any
		if err := json.Unmarshal([]byte(tr.Content[0].Text), &a); err != nil {
			t.Fatalf("%s: not JSON: %s", tool, tr.Content[0].Text)
		}
		_ = json.Unmarshal(rest, &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("%s differs from REST\n mcp  %s\n rest %s", tool, tr.Content[0].Text, rest)
		}
	}
	same("get_assistant_brief", map[string]any{}, "GET", "/api/v1/assistant/brief", "")
	same("list_categories", nil, "GET", "/api/v1/categories", "")
	same("get_oven_reference", map[string]any{}, "GET", "/api/v1/meta/oven", "")
	same("list_inbox", map[string]any{}, "GET", "/api/v1/inbox", "")
	same("list_outcomes", map[string]any{"limit": 10}, "GET", "/api/v1/inbox/outcomes?limit=10", "")
	same("search_recipes", map[string]any{"q": "ribs", "scope": "all", "limit": 5, "category": []string{"beef", "pizza"}}, "GET", "/api/v1/recipes?category=beef&category=pizza&limit=5&q=ribs&scope=all", "")

	// submit -> list -> duplicate -> withdraw, all through MCP
	recipe := map[string]any{"title": "Smoked ribs", "source_url": "https://example.com/mcp-ribs", "categories": []string{"beef"},
		"oven_stages": []map[string]any{{"mode": "smoker", "temperature_c": 120, "duration_seconds": 10800}}}
	tr := callTool(t, full, "submit_recipe", map[string]any{"recipe": recipe, "idempotency_key": "k-1"})
	var res struct {
		ID, Status string
		ReviewURL  string `json:"review_url"`
	}
	_ = json.Unmarshal([]byte(tr.Content[0].Text), &res)
	if tr.IsError || res.Status != "inbox" || !strings.HasSuffix(res.ReviewURL, "/inbox/"+res.ID) {
		t.Fatalf("submit_recipe: %+v", tr)
	}
	if again := callTool(t, full, "submit_recipe", map[string]any{"recipe": recipe, "idempotency_key": "k-1"}); again.IsError || !strings.Contains(again.Content[0].Text, res.ID) {
		t.Fatalf("idempotent replay: %+v", again)
	}
	dup := callTool(t, full, "submit_recipe", map[string]any{"recipe": recipe})
	if !dup.IsError || !strings.Contains(dup.Content[0].Text, "/problems/duplicate") || !strings.Contains(dup.Content[0].Text, res.ID) {
		t.Fatalf("duplicate must surface the problem JSON: %+v", dup)
	}
	bad := callTool(t, full, "submit_recipe", map[string]any{"recipe": map[string]any{"title": ""}})
	if !bad.IsError || !strings.Contains(bad.Content[0].Text, "/problems/validation") || !strings.Contains(bad.Content[0].Text, "invalid_params") {
		t.Fatalf("validation problem: %+v", bad)
	}
	if noRecipe := callTool(t, full, "submit_recipe", map[string]any{}); !noRecipe.IsError {
		t.Fatal("missing recipe must be an error result")
	}
	list := callTool(t, full, "list_inbox", map[string]any{})
	if !strings.Contains(list.Content[0].Text, res.ID) {
		t.Fatalf("list_inbox: %s", list.Content[0].Text)
	}
	if w := callTool(t, full, "withdraw_inbox_item", map[string]any{"id": res.ID}); w.IsError {
		t.Fatalf("withdraw: %+v", w)
	}
	if w := callTool(t, full, "withdraw_inbox_item", map[string]any{"id": res.ID}); !w.IsError || !strings.Contains(w.Content[0].Text, "/problems/not-found") {
		t.Fatalf("second withdraw: %+v", w)
	}
	if g := callTool(t, full, "get_recipe", map[string]any{"id": "nope"}); !g.IsError {
		t.Fatal("unknown recipe")
	}

	// the "previously reviewed" refusal reaches MCP too
	a := h.NewClient(h.SessionCookie(alice))
	_, it := submit(t, full, `{"title":"Thrown","source_url":"https://example.com/thrown"}`)
	if resp, _ := a.PostForm("/inbox/"+it["id"].(string)+"/reject", url.Values{"reason": {"poor_details"}}); resp.StatusCode != 303 {
		t.Fatal("reject")
	}
	prev := callTool(t, full, "submit_recipe", map[string]any{"recipe": map[string]any{"title": "Thrown", "source_url": "https://example.com/thrown?utm_x=1"}, "allow_duplicate": true})
	if !prev.IsError || !strings.Contains(prev.Content[0].Text, "previously-reviewed") || !strings.Contains(prev.Content[0].Text, "poor_details") {
		t.Fatalf("previously-reviewed via MCP: %+v", prev)
	}
	out := callTool(t, full, "list_outcomes", map[string]any{})
	if !strings.Contains(out.Content[0].Text, "poor_details") {
		t.Fatalf("list_outcomes: %s", out.Content[0].Text)
	}
}

func TestMCPToolSchemasAreValid(t *testing.T) {
	h := start(t, harness.Options{})
	alice := h.CreateUser("Alice", "a@e.com", false, "en")
	c := h.NewClient(nil)
	c.Bearer = h.Token(alice)
	_, r := rpc(t, c, "tools/list", map[string]any{})
	var l struct {
		Tools []struct {
			Name        string
			Description string
			InputSchema map[string]any `json:"inputSchema"`
		}
	}
	if err := json.Unmarshal(r.Result, &l); err != nil || len(l.Tools) != len(mcp.Names()) {
		t.Fatalf("tools: %v %d", err, len(l.Tools))
	}
	refRe := regexp.MustCompile(`"\$ref":"([^"]+)"`)
	for _, tl := range l.Tools {
		if tl.Description == "" || tl.InputSchema["type"] != "object" {
			t.Errorf("%s: description/schema type", tl.Name)
		}
		raw, _ := json.Marshal(tl.InputSchema)
		defs, _ := tl.InputSchema["$defs"].(map[string]any)
		for _, m := range refRe.FindAllStringSubmatch(string(raw), -1) {
			name := strings.TrimPrefix(m[1], "#/$defs/")
			if !strings.HasPrefix(m[1], "#/$defs/") || defs[name] == nil {
				t.Errorf("%s: dangling ref %s", tl.Name, m[1])
			}
		}
		if strings.Contains(string(raw), "#/components/") {
			t.Errorf("%s: OpenAPI ref leaked into the MCP schema", tl.Name)
		}
		if tl.Name == "submit_recipe" {
			rs, _ := tl.InputSchema["properties"].(map[string]any)["recipe"].(map[string]any)
			if rs["$ref"] != "#/$defs/RecipeImport" || defs["RecipeImport"] == nil || defs["OvenStage"] == nil {
				t.Errorf("submit_recipe must reuse the REST RecipeImport schema: %v", rs)
			}
		}
	}
}
