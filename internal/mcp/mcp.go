// Package mcp is the MCP server over the JSON API (40 §9.2): stateless Streamable HTTP, one tool per REST twin.
// Tools never touch the service layer themselves; they call the API's own handlers through a Dispatcher, so
// results, validation, scopes and problem+json errors are identical to REST.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/liorokman/ninjarecipe/internal/domain"
	"github.com/liorokman/ninjarecipe/internal/reqctx"
)

// Dispatcher runs one authenticated API call as the user and token found in ctx, without rate limiting, and
// returns the status and body the REST endpoint would send.
type Dispatcher func(ctx context.Context, method, target string, header http.Header, body []byte) (status int, respBody []byte)

// Instructions is sent to clients on initialize (40 §9.2).
const Instructions = `You help a household that cooks on a Ninja Woodfire OO101 outdoor oven find recipes. Before searching, read get_assistant_brief and list_outcomes to learn what they want and what they threw away. Before submitting, check search_recipes with the source URL. Always fill oven_stages using get_oven_reference: map the recipe's method to the oven's modes and stay within the listed ranges. Send the page's canonical URL as source_url and use known category slugs from list_categories. Submissions go to the user's review list; you cannot keep, share, edit or delete recipes.`

// toolDef is one tool. Scope "" = any valid token.
type toolDef struct {
	Name, Description, Scope string
	ReadOnly                 bool
	Schema                   string // JSON Schema of the arguments; "$defs" are added for submit_recipe
	Call                     func(args json.RawMessage) (method, target string, header http.Header, body []byte, err error)
}

// Names returns the tool names in listing order (tests).
func Names() []string {
	var out []string
	for _, t := range tools() {
		out = append(out, t.Name)
	}
	return out
}

// NewHandler returns the MCP endpoint. components is the "components.schemas" object of openapi.json; the
// submit_recipe argument schema reuses its RecipeImport so that MCP and REST accept exactly the same recipe.
// The handler expects the request to be authenticated already (reqctx user and token set).
func NewHandler(d Dispatcher, version string, components map[string]json.RawMessage) http.Handler {
	return sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server {
		return newServer(d, version, components, reqctx.User(r.Context()), reqctx.Token(r.Context()))
	}, &sdk.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		// The app's HostCheck middleware already pins Host to APP_BASE_URL; the SDK's own localhost check would reject a
		// reverse proxy that connects over 127.0.0.1.
		DisableLocalhostProtection: true,
	})
}

func newServer(d Dispatcher, version string, components map[string]json.RawMessage, u *domain.User, tok *domain.APIToken) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "ninja-recipes", Title: "Ninja Woodfire Recipe Keeper", Version: version}, &sdk.ServerOptions{Instructions: Instructions})
	for _, t := range tools() {
		// A tool whose scope the token lacks is left out of tools/list (40 §9.2).
		if tok == nil || (t.Scope != "" && !tok.HasScope(t.Scope)) {
			continue
		}
		t := t
		s.AddTool(&sdk.Tool{Name: t.Name, Description: t.Description, InputSchema: schemaFor(t, components),
			Annotations: &sdk.ToolAnnotations{ReadOnlyHint: t.ReadOnly}},
			func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				method, target, header, body, err := t.Call(req.Params.Arguments)
				if err != nil {
					return textResult(true, problemJSON(400, "bad-request", "Bad request", err.Error())), nil
				}
				ctx = reqctx.WithToken(reqctx.WithUser(ctx, u), tok)
				status, resp := d(ctx, method, target, header, body)
				if status >= 400 {
					return textResult(true, string(resp)), nil
				}
				if status == http.StatusNoContent || len(resp) == 0 {
					resp = []byte(`{"ok":true}`)
				}
				return textResult(false, string(resp)), nil
			})
	}
	return s
}

func textResult(isErr bool, text string) *sdk.CallToolResult {
	return &sdk.CallToolResult{IsError: isErr, Content: []sdk.Content{&sdk.TextContent{Text: text}}}
}

func problemJSON(status int, slug, title, detail string) string {
	b, _ := json.Marshal(map[string]any{"type": slug, "title": title, "status": status, "detail": detail})
	return string(b)
}

func schemaFor(t toolDef, components map[string]json.RawMessage) json.RawMessage {
	if t.Name != "submit_recipe" {
		return json.RawMessage(t.Schema)
	}
	// RecipeImport and everything it references, as $defs; refs are rewritten from OpenAPI to JSON Schema form.
	defs := map[string]json.RawMessage{}
	for k, v := range components {
		defs[k] = json.RawMessage(strings.ReplaceAll(string(v), "#/components/schemas/", "#/$defs/"))
	}
	b, _ := json.Marshal(defs)
	return json.RawMessage(strings.Replace(t.Schema, `"$defs":{}`, `"$defs":`+string(b), 1))
}

type args = map[string]json.RawMessage

func parse(raw json.RawMessage) (args, error) {
	a := args{}
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return a, nil
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("arguments must be a JSON object")
	}
	return a, nil
}

func str(a args, k string) (string, error) {
	v, ok := a[k]
	if !ok || string(v) == "null" {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return "", fmt.Errorf("%s must be a string", k)
	}
	return s, nil
}

func intArg(a args, k string) (string, error) {
	v, ok := a[k]
	if !ok || string(v) == "null" {
		return "", nil
	}
	var n int
	if err := json.Unmarshal(v, &n); err != nil {
		return "", fmt.Errorf("%s must be an integer", k)
	}
	return fmt.Sprint(n), nil
}

func get(path string) func(json.RawMessage) (string, string, http.Header, []byte, error) {
	return func(json.RawMessage) (string, string, http.Header, []byte, error) {
		return "GET", path, nil, nil, nil
	}
}

func byID(method, prefix string) func(json.RawMessage) (string, string, http.Header, []byte, error) {
	return func(raw json.RawMessage) (string, string, http.Header, []byte, error) {
		a, err := parse(raw)
		if err != nil {
			return "", "", nil, nil, err
		}
		id, err := str(a, "id")
		if err != nil || id == "" {
			return "", "", nil, nil, fmt.Errorf("id is required")
		}
		return method, prefix + url.PathEscape(id), nil, nil, nil
	}
}

const none = `{"type":"object","properties":{},"additionalProperties":false}`

func tools() []toolDef {
	return []toolDef{
		{Name: "get_assistant_brief", ReadOnly: true, Schema: none,
			Description: "What the household asked you to look for, in their own words, and the language they read. Read this before searching.",
			Call:        get("/api/v1/assistant/brief")},
		{Name: "list_categories", ReadOnly: true, Schema: none,
			Description: "Known category slugs (use these in submit_recipe) and the tags already in use.",
			Call:        get("/api/v1/categories")},
		{Name: "get_oven_reference", ReadOnly: true, Schema: none,
			Description: "The oven's modes with allowed temperature and time ranges, pizza presets, pellet blends and accessories. Use it to fill oven_stages.",
			Call:        get("/api/v1/meta/oven")},
		{Name: "search_recipes", ReadOnly: true, Scope: domain.ScopeRecipesRead,
			Description: "Search recipes the household already has. Pass source_url to check whether a page is already saved.",
			Schema: `{"type":"object","additionalProperties":false,"properties":{"q":{"type":"string"},"source_url":{"type":"string"},` +
				`"scope":{"type":"string","enum":["all","mine","family"]},"category":{"type":"array","items":{"type":"string"}},` +
				`"cursor":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":100}}}`,
			Call: func(raw json.RawMessage) (string, string, http.Header, []byte, error) {
				a, err := parse(raw)
				if err != nil {
					return "", "", nil, nil, err
				}
				q := url.Values{}
				for _, k := range []string{"q", "source_url", "scope", "cursor"} {
					v, err := str(a, k)
					if err != nil {
						return "", "", nil, nil, err
					}
					if v != "" {
						q.Set(k, v)
					}
				}
				if l, err := intArg(a, "limit"); err != nil {
					return "", "", nil, nil, err
				} else if l != "" {
					q.Set("limit", l)
				}
				if v, ok := a["category"]; ok {
					var cats []string
					if err := json.Unmarshal(v, &cats); err != nil {
						return "", "", nil, nil, fmt.Errorf("category must be an array of strings")
					}
					for _, c := range cats {
						q.Add("category", c)
					}
				}
				return "GET", "/api/v1/recipes?" + q.Encode(), nil, nil, nil
			}},
		{Name: "get_recipe", ReadOnly: true, Scope: domain.ScopeRecipesRead, Description: "One recipe by id, in full.",
			Schema: `{"type":"object","additionalProperties":false,"required":["id"],"properties":{"id":{"type":"string"}}}`,
			Call:   byID("GET", "/api/v1/recipes/")},
		{Name: "submit_recipe", Scope: domain.ScopeInboxWrite,
			Description: "Submit a recipe to the user's review list. Fails with duplicate if they already have it, or previously-reviewed if they kept or threw it away before; do not retry those.",
			Schema: `{"type":"object","additionalProperties":false,"required":["recipe"],"properties":{"recipe":{"$ref":"#/$defs/RecipeImport"},` +
				`"idempotency_key":{"type":"string","minLength":1,"maxLength":100},"allow_duplicate":{"type":"boolean"}},"$defs":{}}`,
			Call: func(raw json.RawMessage) (string, string, http.Header, []byte, error) {
				a, err := parse(raw)
				if err != nil {
					return "", "", nil, nil, err
				}
				rec, ok := a["recipe"]
				if !ok {
					return "", "", nil, nil, fmt.Errorf("recipe is required")
				}
				h := http.Header{"Content-Type": {"application/json"}}
				if k, err := str(a, "idempotency_key"); err != nil {
					return "", "", nil, nil, err
				} else if k != "" {
					h.Set("Idempotency-Key", k)
				}
				target := "/api/v1/inbox"
				if v, ok := a["allow_duplicate"]; ok {
					var b bool
					if err := json.Unmarshal(v, &b); err != nil {
						return "", "", nil, nil, fmt.Errorf("allow_duplicate must be a boolean")
					}
					if b {
						target += "?allow_duplicate=1"
					}
				}
				return "POST", target, h, rec, nil
			}},
		{Name: "list_inbox", ReadOnly: true, Scope: domain.ScopeInboxWrite, Schema: none,
			Description: "Recipes you submitted that are still waiting for the household's decision.",
			Call:        get("/api/v1/inbox")},
		{Name: "withdraw_inbox_item", Scope: domain.ScopeInboxWrite, Description: "Take back a submission that is still waiting for review.",
			Schema: `{"type":"object","additionalProperties":false,"required":["id"],"properties":{"id":{"type":"string"}}}`,
			Call:   byID("DELETE", "/api/v1/inbox/")},
		{Name: "list_outcomes", ReadOnly: true, Scope: domain.ScopeInboxWrite,
			Description: "What the household kept or threw away, oldest first, with their reasons. Pass the previous next_cursor as after to read only new decisions.",
			Schema:      `{"type":"object","additionalProperties":false,"properties":{"after":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":100}}}`,
			Call: func(raw json.RawMessage) (string, string, http.Header, []byte, error) {
				a, err := parse(raw)
				if err != nil {
					return "", "", nil, nil, err
				}
				q := url.Values{}
				if v, err := str(a, "after"); err != nil {
					return "", "", nil, nil, err
				} else if v != "" {
					q.Set("after", v)
				}
				if l, err := intArg(a, "limit"); err != nil {
					return "", "", nil, nil, err
				} else if l != "" {
					q.Set("limit", l)
				}
				target := "/api/v1/inbox/outcomes"
				if len(q) > 0 {
					target += "?" + q.Encode()
				}
				return "GET", target, nil, nil, nil
			}},
	}
}
