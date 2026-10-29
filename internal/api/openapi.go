package api

import (
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/liorokman/ninjarecipe/internal/domain"
	"github.com/liorokman/ninjarecipe/internal/mcp"
)

//go:embed openapi.json
var openapiJSON []byte

// openapiDoc returns the embedded document with servers[0].url set to ${APP_BASE_URL}/api/v1 (40 §9.1).
func (a *API) openapiDoc() []byte {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(openapiJSON, &doc); err != nil {
		return openapiJSON
	}
	doc["servers"], _ = json.Marshal([]map[string]string{{"url": a.BaseURL + "/api/v1"}})
	out, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		return openapiJSON
	}
	return out
}

func (a *API) openapi(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(a.openapiDoc())
}

// mcpHandler is built once, lazily: it needs the schemas from openapi.json and the internal mux.
func (a *API) mcpHandler() http.Handler {
	a.mcpOnce.Do(func() {
		var doc struct {
			Components struct {
				Schemas map[string]json.RawMessage `json:"schemas"`
			} `json:"components"`
		}
		_ = json.Unmarshal(openapiJSON, &doc)
		mux := a.internalMux()
		a.mcpH = mcp.NewHandler(func(ctx context.Context, method, target string, header http.Header, body []byte) (int, []byte) {
			req := httptest.NewRequest(method, target, strings.NewReader(string(body))).WithContext(ctx)
			for k, v := range header {
				req.Header[k] = v
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			resp, _ := io.ReadAll(rec.Result().Body)
			return rec.Code, resp
		}, a.Version, doc.Components.Schemas)
	})
	return a.mcpH
}

// mcp serves POST /api/v1/mcp for an authenticated, rate-limited token (guard has run).
func (a *API) mcp(w http.ResponseWriter, r *http.Request, _ *domain.User) {
	r.Body = http.MaxBytesReader(w, r.Body, a.MaxBody)
	a.mcpHandler().ServeHTTP(w, r)
}
