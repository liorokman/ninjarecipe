// Package api implements the JSON API under /api/v1 (40 §3-6): bearer auth, scopes, rate limit, problem+json.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/liorokman/ninjarecipe/internal/domain"
	"github.com/liorokman/ninjarecipe/internal/httpx"
	"github.com/liorokman/ninjarecipe/internal/i18n"
	"github.com/liorokman/ninjarecipe/internal/oven"
	"github.com/liorokman/ninjarecipe/internal/reqctx"
	"github.com/liorokman/ninjarecipe/internal/service"
	"github.com/liorokman/ninjarecipe/internal/store"
	"github.com/liorokman/ninjarecipe/internal/web"
)

// API holds the dependencies of the JSON handlers.
type API struct {
	Svc     *service.Service
	Cat     *i18n.Catalog
	BaseURL string
	MaxBody int64
	Limiter *httpx.Limiter
	enLoc   *i18n.Loc
	// Version is reported to MCP clients (40 §9.2).
	Version string

	mcpOnce sync.Once
	mcpH    http.Handler
}

// New builds the API.
func New(svc *service.Service, cat *i18n.Catalog, baseURL string, maxBodyMB, ratePerMin int) *API {
	return &API{Svc: svc, Cat: cat, BaseURL: baseURL, MaxBody: int64(maxBodyMB) << 20, Limiter: httpx.NewLimiter(ratePerMin, 20), enLoc: cat.Localizer("en", nil)}
}

type userHandler = func(http.ResponseWriter, *http.Request, *domain.User)

// route is one authenticated /api/v1 endpoint (40 §3); scope "" = any valid token.
type route struct {
	Pattern, Scope string
	H              userHandler
}

// routes is the table behind Register and the MCP dispatcher (40 §9.2). The public routes (meta/oven, openapi.json)
// and /mcp itself are mounted separately.
func (a *API) routes() []route {
	return []route{
		{"POST /api/v1/inbox", domain.ScopeInboxWrite, a.importInbox},
		{"GET /api/v1/inbox", domain.ScopeInboxWrite, a.listInbox},
		{"GET /api/v1/inbox/outcomes", domain.ScopeInboxWrite, a.listOutcomes},
		{"DELETE /api/v1/inbox/{id}", domain.ScopeInboxWrite, a.withdraw},
		{"GET /api/v1/recipes", domain.ScopeRecipesRead, a.listRecipes},
		{"GET /api/v1/recipes/{id}", domain.ScopeRecipesRead, a.getRecipe},
		{"GET /api/v1/categories", "", a.categories},
		{"GET /api/v1/assistant/brief", "", a.assistantBrief},
	}
}

// Register mounts the /api/v1 routes.
func (a *API) Register(mux *http.ServeMux) {
	for _, rt := range a.routes() {
		mux.HandleFunc(rt.Pattern, a.guard(rt.Scope, rt.H))
	}
	mux.HandleFunc("GET /api/v1/meta/oven", a.metaOven)
	mux.HandleFunc("GET /api/v1/openapi.json", a.openapi)
	// GET and DELETE on /mcp get 405 from the mux because only POST is registered (40 §9.2).
	mux.HandleFunc("POST /api/v1/mcp", a.guard("", a.mcp))
}

// internalMux serves the authenticated routes without the bearer/rate-limit guard, for the MCP tools: the MCP request
// itself was already authenticated and rate-limited, and each tool still enforces the scope of its REST twin.
func (a *API) internalMux() *http.ServeMux {
	mux := http.NewServeMux()
	for _, rt := range a.routes() {
		mux.HandleFunc(rt.Pattern, a.scoped(rt.Scope, rt.H))
	}
	mux.HandleFunc("GET /api/v1/meta/oven", a.metaOven) // public over REST, so no scope here either
	return mux
}

// scoped is guard without authentication and rate limiting: user and token come from the request context.
func (a *API) scoped(scope string, h userHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t, u := reqctx.Token(r.Context()), reqctx.User(r.Context())
		if t == nil || u == nil {
			a.problem(w, r, 401, "unauthorized", "Authentication required", "Missing, invalid, expired or revoked token.")
			return
		}
		if scope != "" && !t.HasScope(scope) {
			a.problem(w, r, 403, "forbidden", "Forbidden", "The token lacks the "+scope+" scope.")
			return
		}
		h(w, r, u)
	}
}

// guard authenticates (bearer only), rate-limits per token and checks the scope ("" = any valid token).
func (a *API) guard(scope string, h func(http.ResponseWriter, *http.Request, *domain.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t, u := reqctx.Token(r.Context()), reqctx.User(r.Context())
		if t == nil || u == nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			a.problem(w, r, 401, "unauthorized", "Authentication required", "Missing, invalid, expired or revoked token.")
			return
		}
		if ok, retry := a.Limiter.Allow(t.ID); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			a.problem(w, r, 429, "rate-limited", "Too many requests", "Rate limit exceeded; retry later.")
			return
		}
		if scope != "" && !t.HasScope(scope) {
			a.problem(w, r, 403, "forbidden", "Forbidden", "The token lacks the "+scope+" scope.")
			return
		}
		h(w, r, u)
	}
}

// NotFound is the 404 for unknown /api/ paths.
func (a *API) NotFound(w http.ResponseWriter, r *http.Request) {
	a.problem(w, r, 404, "not-found", "Not found", "No such endpoint.")
}

type invalidParam struct {
	Name   string         `json:"name"`
	Code   string         `json:"code"`
	Params map[string]any `json:"params,omitempty"`
	Reason string         `json:"reason"`
}

func (a *API) reason(is domain.Issue, warn bool) string {
	return web.VB{Loc: a.enLoc}.Issue(is, warn)
}

func (a *API) issues(is []domain.Issue, warn bool) []invalidParam {
	out := make([]invalidParam, 0, len(is))
	for _, x := range is {
		out = append(out, invalidParam{Name: x.Field, Code: x.Code, Params: x.Params, Reason: a.reason(x, warn)})
	}
	return out
}

func (a *API) problem(w http.ResponseWriter, r *http.Request, status int, slug, title, detail string, extra ...any) {
	body := map[string]any{
		"type": a.BaseURL + "/problems/" + slug, "title": title, "status": status, "detail": detail,
		"instance": r.URL.Path, "request_id": httpx.RequestIDFromContext(r.Context()),
	}
	for i := 0; i+1 < len(extra); i += 2 {
		body[extra[i].(string)] = extra[i+1]
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// fail maps service errors to problems.
func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ve *domain.ValidationError
	var dup domain.ErrDuplicate
	var prev domain.ErrPreviouslyReviewed
	var mbe *http.MaxBytesError
	switch {
	case errors.As(err, &ve):
		n := len(ve.Errors)
		detail := "1 field is invalid."
		if n != 1 {
			detail = strconv.Itoa(n) + " fields are invalid."
		}
		a.problem(w, r, 422, "validation", "Validation failed", detail, "invalid_params", a.issues(ve.Errors, false))
	case errors.As(err, &dup):
		a.problem(w, r, 409, "duplicate", "Duplicate", "A recipe with this source URL already exists.", "existing_id", dup.ExistingID)
	case errors.As(err, &prev):
		var reason any
		if prev.Reason != "" {
			reason = string(prev.Reason)
		}
		a.problem(w, r, 409, "previously-reviewed", "Previously reviewed", "This source was already kept or thrown away; it is not offered again.",
			"outcome", prev.Outcome, "decided_at", store.FormatTime(prev.DecidedAt), "reason", reason)
	case errors.Is(err, domain.ErrNotFound):
		a.problem(w, r, 404, "not-found", "Not found", "No such resource.")
	case errors.Is(err, domain.ErrForbidden):
		a.problem(w, r, 403, "forbidden", "Forbidden", "Not allowed.")
	case errors.Is(err, domain.ErrConflict):
		a.problem(w, r, 409, "conflict", "Conflict", "Version conflict.")
	case errors.Is(err, domain.ErrTooLarge), errors.As(err, &mbe):
		a.problem(w, r, 413, "too-large", "Too large", "The request body or a file is too large.")
	case errors.Is(err, domain.ErrUnsupportedMedia):
		a.problem(w, r, 415, "unsupported-media-type", "Unsupported media type", "Unsupported image type.")
	case errors.Is(err, domain.ErrBadRequest):
		a.problem(w, r, 400, "bad-request", "Bad request", "Malformed request.")
	default:
		a.problem(w, r, 500, "internal", "Internal error", "Something went wrong; quote the request_id.")
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ---- handlers ----

func (a *API) metaOven(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, oven.Default()) }

func (a *API) importInbox(w http.ResponseWriter, r *http.Request, u *domain.User) {
	r.Body = http.MaxBytesReader(w, r.Body, a.MaxBody)
	key := r.Header.Get("Idempotency-Key")
	if key != "" {
		if len(key) > 100 {
			a.problem(w, r, 400, "bad-request", "Bad request", "Idempotency-Key must be 1-100 printable ASCII characters.")
			return
		}
		for _, c := range key {
			if c < 0x20 || c > 0x7e {
				a.problem(w, r, 400, "bad-request", "Bad request", "Idempotency-Key must be printable ASCII.")
				return
			}
		}
	}
	var in service.RecipeImport
	if err := decodeStrict(r.Body, &in); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			a.fail(w, r, err)
			return
		}
		a.problem(w, r, 400, "bad-request", "Bad request", jsonProblem(err))
		return
	}
	res, err := a.Svc.ImportToInbox(r.Context(), u, in, key, r.URL.Query().Get("allow_duplicate") == "1")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	for i := range res.Warnings {
		res.Warnings[i].Params = nonNil(res.Warnings[i].Params)
	}
	status := 201
	if !res.Created {
		status = 200
	}
	writeJSON(w, status, res)
}

// decodeStrict decodes one JSON value and rejects fields the schema does not define, so a misnamed field
// (temperature_celsius for temperature_c) is an error instead of silently dropped data.
func decodeStrict(body io.Reader, v any) error {
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// jsonProblem turns a decode error into a message that names the field, so an assistant can fix its payload.
func jsonProblem(err error) string {
	const hint = " The request schema is at /api/v1/openapi.json."
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syn):
		return fmt.Sprintf("Malformed JSON body (syntax error at byte %d).", syn.Offset)
	case errors.As(err, &typ):
		field := typ.Field
		if field == "" {
			field = "(body)"
		}
		return fmt.Sprintf("Field %q must be %s, but a %s was sent.%s", field, jsonKind(typ.Type), typ.Value, hint)
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		return "Unknown field " + strings.TrimPrefix(err.Error(), "json: unknown field ") + "; fields the schema does not define are rejected." + hint
	case errors.Is(err, io.EOF):
		return "Empty request body."
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "Malformed JSON body (the JSON ends too early)."
	}
	return "Malformed JSON body."
}

func jsonKind(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Struct, reflect.Map:
		return "an object"
	case reflect.Slice, reflect.Array:
		return "an array"
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "true or false"
	case reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "a whole number"
	}
	return t.String()
}

func nonNil(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func (a *API) listInbox(w http.ResponseWriter, r *http.Request, u *domain.User) {
	items, err := a.Svc.ListInbox(r.Context(), u)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := []RecipeSummary{}
	for _, s := range items {
		out = append(out, a.summary(s))
	}
	writeJSON(w, 200, out)
}

func (a *API) withdraw(w http.ResponseWriter, r *http.Request, u *domain.User) {
	if err := a.Svc.WithdrawInbox(r.Context(), u, r.PathValue("id")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) listRecipes(w http.ResponseWriter, r *http.Request, u *domain.User) {
	v := r.URL.Query()
	q := domain.SearchQuery{Q: strings.TrimSpace(v.Get("q")), Scope: v.Get("scope"), SourceURL: v.Get("source_url"), Cursor: v.Get("cursor"),
		Categories: v["category"], Sort: "updated"}
	if q.Scope != "" && q.Scope != "all" && q.Scope != "mine" && q.Scope != "family" {
		a.problem(w, r, 400, "bad-request", "Bad request", "scope must be all, mine or family.")
		return
	}
	if q.Q != "" {
		q.Sort = "relevance"
	}
	q.Limit = 50
	if l := v.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > 100 {
			a.problem(w, r, 400, "bad-request", "Bad request", "limit must be between 1 and 100.")
			return
		}
		q.Limit = n
	}
	page, err := a.Svc.Search(r.Context(), u, q)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	items := []RecipeSummary{}
	for _, s := range page.Items {
		items = append(items, a.summary(s))
	}
	var next any
	if page.NextCursor != "" {
		next = page.NextCursor
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}

func (a *API) getRecipe(w http.ResponseWriter, r *http.Request, u *domain.User) {
	d, err := a.Svc.GetRecipe(r.Context(), u, r.PathValue("id"), false)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, 200, a.recipe(d.Recipe))
}

func (a *API) categories(w http.ResponseWriter, r *http.Request, u *domain.User) {
	cats, err := a.Svc.ListCategories(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	tags, err := a.Svc.VisibleTags(r.Context(), u)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	cs := []map[string]string{}
	for _, c := range cats {
		cs = append(cs, map[string]string{"slug": c.Slug, "name": c.Name, "name_he": c.NameHe, "icon": c.Icon})
	}
	if tags == nil {
		tags = []string{}
	}
	writeJSON(w, 200, map[string]any{"categories": cs, "tags": tags})
}

// OutcomeDTO is one item of GET /api/v1/inbox/outcomes (40 §7).
type OutcomeDTO struct {
	Cursor    string  `json:"cursor"`
	ItemID    string  `json:"item_id"`
	RecipeID  *string `json:"recipe_id"`
	Title     string  `json:"title"`
	SourceURL *string `json:"source_url"`
	Outcome   string  `json:"outcome"`
	Reason    *string `json:"reason"`
	Note      string  `json:"note"`
	Edited    bool    `json:"edited"`
	Shared    bool    `json:"shared"`
	DecidedAt string  `json:"decided_at"`
}

func (a *API) listOutcomes(w http.ResponseWriter, r *http.Request, u *domain.User) {
	v := r.URL.Query()
	limit := 50
	if l := v.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > 100 {
			a.problem(w, r, 400, "bad-request", "Bad request", "limit must be between 1 and 100.")
			return
		}
		limit = n
	}
	page, err := a.Svc.ListOutcomes(r.Context(), u, v.Get("after"), limit)
	if err != nil {
		if errors.Is(err, domain.ErrBadRequest) {
			a.problem(w, r, 400, "bad-request", "Bad request", "after must be a cursor from a previous response.")
			return
		}
		a.fail(w, r, err)
		return
	}
	items := []OutcomeDTO{}
	for _, o := range page.Items {
		var reason *string
		if o.Reason != "" {
			s := string(o.Reason)
			reason = &s
		}
		items = append(items, OutcomeDTO{Cursor: strconv.FormatInt(o.ID, 10), ItemID: o.ItemID, RecipeID: sp(o.RecipeID), Title: o.Title,
			SourceURL: sp(o.SourceURL), Outcome: o.Outcome, Reason: reason, Note: o.Note, Edited: o.Edited, Shared: o.Shared,
			DecidedAt: store.FormatTime(o.DecidedAt)})
	}
	var next any
	if page.NextCursor != "" {
		next = page.NextCursor
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}

func (a *API) assistantBrief(w http.ResponseWriter, r *http.Request, u *domain.User) {
	b, err := a.Svc.GetAssistantBrief(r.Context(), u)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var at *string
	if b.UpdatedAt != nil {
		s := store.FormatTime(*b.UpdatedAt)
		at = &s
	}
	writeJSON(w, 200, map[string]any{"text": b.Text, "language": b.Language, "updated_at": at})
}
