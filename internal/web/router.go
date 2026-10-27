package web

import (
	"net/http"
	"strings"
)

type probe struct {
	h      http.Header
	status int
	body   []byte
}

func (p *probe) Header() http.Header { return p.h }
func (p *probe) WriteHeader(c int)   { p.status = c }
func (p *probe) Write(b []byte) (int, error) {
	p.body = append(p.body, b...)
	return len(b), nil
}

// Router wraps the mux so unmatched paths get the friendly 404 page (405 and /api/ handling stay with the mux
// and notFoundAPI respectively).
func Router(mux *http.ServeMux, notFound http.HandlerFunc, apiNotFound http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, pat := mux.Handler(r)
		if pat != "" {
			mux.ServeHTTP(w, r) // ServeHTTP (not h.ServeHTTP) so path values are populated
			return
		}
		p := &probe{h: http.Header{}, status: 200}
		h.ServeHTTP(p, r)
		isAPI := strings.HasPrefix(r.URL.Path, "/api/")
		switch {
		case p.status == http.StatusNotFound && isAPI && apiNotFound != nil:
			apiNotFound(w, r)
		case p.status == http.StatusNotFound:
			notFound(w, r)
		default:
			for k, v := range p.h {
				w.Header()[k] = v
			}
			w.WriteHeader(p.status)
			_, _ = w.Write(p.body)
		}
	})
}
