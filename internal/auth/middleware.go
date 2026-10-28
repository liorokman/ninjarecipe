package auth

import (
	"net/http"
	"strings"

	"github.com/liorokman/ninjarecipe/internal/httpx"
	"github.com/liorokman/ninjarecipe/internal/reqctx"
	"github.com/liorokman/ninjarecipe/internal/service"
)

// BearerToken extracts the token from an Authorization header ("" if absent/malformed).
func BearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// Middleware attaches identity to the request context without rejecting anything:
// /api/v1/* uses the bearer token only (never the cookie), /media/* accepts both, everything else the session.
func Middleware(sessions *Sessions, svc *service.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			p := r.URL.Path
			api := strings.HasPrefix(p, "/api/")
			media := strings.HasPrefix(p, "/media/")
			if api || media {
				if raw := BearerToken(r); raw != "" {
					if t, u, err := svc.AuthenticateToken(ctx, raw); err == nil {
						ctx = reqctx.WithToken(ctx, t)
						ctx = reqctx.WithUser(ctx, u)
					}
				}
			}
			if !api && reqctx.User(ctx) == nil {
				if s, u := sessions.Lookup(ctx, r); u != nil {
					ctx = reqctx.WithSession(ctx, s)
					ctx = reqctx.WithUser(ctx, u)
				}
			}
			if u := reqctx.User(ctx); u != nil {
				httpx.SetUserID(ctx, u.ID)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
