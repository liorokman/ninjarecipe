package web

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"cooking/internal/auth"
	"cooking/internal/config"
	"cooking/internal/domain"
	"cooking/internal/httpx"
	"cooking/internal/i18n"
	"cooking/internal/images"
	"cooking/internal/reqctx"
	"cooking/internal/service"
)

// Pinger is the DB readiness probe.
type Pinger interface {
	Ping(ctx context.Context) error
}

// App bundles the dependencies of the browser handlers.
type App struct {
	Cfg      *config.Config
	Svc      *service.Service
	Cat      *i18n.Catalog
	R        *Renderer
	OIDC     *auth.OIDC
	Sessions *auth.Sessions
	DB       Pinger

	Version, Commit, BuildDate string

	mediaMu   sync.Mutex
	mediaOKAt time.Time
	mediaOK   bool
}

// SafeReturnTo validates a return_to value: must start with "/" and not "//" or "/\\" (40 §1).
func SafeReturnTo(s string) string {
	if s == "" || s[0] != '/' || strings.HasPrefix(s, "//") || strings.HasPrefix(s, "/\\") {
		return "/"
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "/"
		}
	}
	return s
}

func (a *App) user(r *http.Request) *domain.User { return reqctx.User(r.Context()) }

func (a *App) vb(r *http.Request) VB {
	l := reqctx.Loc(r.Context())
	if l == nil {
		l = a.Cat.Localizer(a.Cfg.DefaultLang, a.Cfg.TZ)
	}
	return VB{Loc: l, Now: a.Svc.Clock.Now(), BaseURL: a.Cfg.BaseURL}
}

func isFetch(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" || strings.Contains(r.Header.Get("Accept"), "application/json")
}

func isHTMX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

// Locale resolves the UI language per request (user, cookie, Accept-Language, default) and stores a Loc.
func Locale(cat *i18n.Catalog, def string, tz *time.Location) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ul := ""
			if u := reqctx.User(r.Context()); u != nil {
				ul = u.UILanguage
			}
			lang := i18n.ResolveLang(ul, r, def)
			next.ServeHTTP(w, r.WithContext(reqctx.WithLoc(r.Context(), cat.Localizer(lang, tz))))
		})
	}
}

// Wrap enforces Route.Access with the unauthenticated rules of 40 §1.
func (a *App) Wrap(rt Route, h http.Handler) http.Handler {
	if rt.Access == Public {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := a.user(r)
		if u == nil || reqctx.Token(r.Context()) != nil {
			a.unauthenticated(w, r)
			return
		}
		if rt.Access == Admin && !u.IsAdmin {
			a.R.Error(w, r, "forbidden", 0)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func (a *App) unauthenticated(w http.ResponseWriter, r *http.Request) {
	signin := URL("signin")
	switch {
	case isFetch(r):
		back := "/"
		if cur := r.Header.Get("HX-Current-URL"); cur != "" {
			if u, err := url.Parse(cur); err == nil {
				back = SafeReturnTo(u.RequestURI())
			}
		} else if r.Method == http.MethodGet {
			back = SafeReturnTo(r.URL.RequestURI())
		}
		w.Header().Set("HX-Redirect", signin+"?return_to="+url.QueryEscape(back))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		http.Redirect(w, r, signin+"?return_to="+url.QueryEscape(SafeReturnTo(r.URL.RequestURI())), http.StatusSeeOther)
	default:
		back := "/"
		if ref := r.Header.Get("Referer"); ref != "" {
			if u, err := url.Parse(ref); err == nil && (u.Host == "" || strings.EqualFold(u.Host, hostOf(a.Cfg.BaseURL))) {
				back = SafeReturnTo(u.RequestURI())
			}
		}
		http.Redirect(w, r, signin+"?return_to="+url.QueryEscape(back), http.StatusSeeOther)
	}
}

func hostOf(base string) string {
	u, _ := url.Parse(base)
	if u == nil {
		return ""
	}
	return u.Host
}

// fail maps a service error to an error page (or bare status for fetch callers).
func (a *App) fail(w http.ResponseWriter, r *http.Request, err error) {
	var maxErr *http.MaxBytesError
	switch {
	case errors.Is(err, domain.ErrNotFound):
		a.R.Error(w, r, "not_found", 0)
	case errors.Is(err, domain.ErrForbidden):
		a.R.Error(w, r, "forbidden", 0)
	case errors.Is(err, domain.ErrConflict):
		a.R.Error(w, r, "conflict", 0)
	case errors.Is(err, domain.ErrTooLarge), errors.As(err, &maxErr):
		a.R.Error(w, r, "too_large", 0)
	case errors.Is(err, domain.ErrUnsupportedMedia):
		a.R.Error(w, r, "unsupported_media", 0)
	case errors.Is(err, domain.ErrBusy):
		a.R.Error(w, r, "busy", 0)
	case errors.Is(err, domain.ErrBadRequest):
		a.R.Error(w, r, "bad_request", 0)
	default:
		var ve *domain.ValidationError
		if errors.As(err, &ve) {
			a.R.Error(w, r, "bad_request", 0)
			return
		}
		slog.Error("request failed", "error", err, "path", r.URL.Path, "request_id", httpx.RequestIDFromContext(r.Context()))
		a.R.Error(w, r, "internal", 0)
	}
}

// parseForm reads urlencoded or multipart bodies with a size cap.
func (a *App) parseForm(w http.ResponseWriter, r *http.Request, max int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, max)
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		return r.ParseMultipartForm(max)
	}
	return r.ParseForm()
}

const formLimit = 1 << 20

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *App) imageLimit() int64 { return int64(a.Cfg.MaxImageMB) << 20 }

var _ = images.ValidKey
