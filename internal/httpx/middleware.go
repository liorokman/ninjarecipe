// Package httpx contains generic HTTP middleware (20 §4, §8, §13).
package httpx

import (
	"crypto/rand"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"runtime/debug"
	"strings"
	"time"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewULID returns a 26-char ULID (48-bit ms time + 80 random bits).
func NewULID(now time.Time) string {
	var b [16]byte
	ms := uint64(now.UnixMilli())
	for i := 5; i >= 0; i-- {
		b[i] = byte(ms)
		ms >>= 8
	}
	_, _ = rand.Read(b[6:])
	// encode 128 bits as 26 base32 chars (first char holds 3 bits)
	var out [26]byte
	var acc uint
	bits := 0
	idx := 25
	for i := 15; i >= 0; i-- {
		acc |= uint(b[i]) << bits
		bits += 8
		for bits >= 5 && idx >= 0 {
			out[idx] = crockford[acc&31]
			acc >>= 5
			bits -= 5
			idx--
		}
	}
	if idx >= 0 {
		out[idx] = crockford[acc&31]
	}
	return string(out[:])
}

var reqIDRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// Recover turns panics into 500s.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				slog.Error("panic", "panic", fmt.Sprint(rec), "stack", string(debug.Stack()),
					"request_id", RequestIDFromContext(r.Context()), "path", r.URL.Path)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// RequestID sets X-Request-Id (kept when valid, else a new ULID) and the context value.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if !reqIDRe.MatchString(id) {
			id = NewULID(time.Now())
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(WithRequestID(r.Context(), id)))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (s *statusWriter) WriteHeader(c int) {
	if s.status == 0 {
		s.status = c
	}
	s.ResponseWriter.WriteHeader(c)
}
func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = 200
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += int64(n)
	return n, err
}
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// AccessLog logs one line per request. /healthz, /readyz and /static/ are skipped at info level; the query
// string of /auth/* is never logged.
func AccessLog(ip *IPParser) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w}
			cip := ip.ClientIP(r)
			ctx, slot := WithUserSlot(WithClientIP(r.Context(), cip))
			next.ServeHTTP(sw, r.WithContext(ctx))
			if sw.status == 0 {
				sw.status = 200
			}
			lvl := slog.LevelInfo
			p := r.URL.Path
			if p == "/healthz" || p == "/readyz" || strings.HasPrefix(p, "/static/") {
				lvl = slog.LevelDebug
			}
			path := p
			if !strings.HasPrefix(p, "/auth/") && r.URL.RawQuery != "" {
				path = p + "?" + r.URL.RawQuery
			}
			uid := *slot
			slog.Log(r.Context(), lvl, "request", "request_id", RequestIDFromContext(r.Context()), "method", r.Method, "path", path,
				"status", sw.status, "duration_ms", time.Since(start).Milliseconds(), "bytes", sw.bytes, "client_ip", cip, "user_id", uid)
		})
	}
}

// HostCheck rejects requests whose Host differs from the APP_BASE_URL host (health endpoints exempt).
func HostCheck(baseURL string) func(http.Handler) http.Handler {
	u, _ := url.Parse(baseURL)
	want := ""
	if u != nil {
		want = strings.ToLower(u.Host)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" || want == "" || strings.ToLower(r.Host) == want {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "bad host", http.StatusBadRequest)
		})
	}
}

// Origin returns scheme://host of a URL ("" on error).
func Origin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// CSP builds the Content-Security-Policy of 20 §8 exactly.
func CSP(issuerOrigin string) string {
	fa := "'self'"
	if issuerOrigin != "" {
		fa += " " + issuerOrigin
	}
	return "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; connect-src 'self'; " +
		"font-src 'self'; worker-src 'self'; manifest-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action " + fa
}

// SecurityHeaders sets the static security headers (20 §8).
func SecurityHeaders(baseURL, issuerURL string) func(http.Handler) http.Handler {
	csp := CSP(Origin(issuerURL))
	https := strings.HasPrefix(baseURL, "https://")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", csp)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "same-origin")
			h.Set("Permissions-Policy", "camera=(self), geolocation=(), microphone=(), screen-wake-lock=(self)")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			if https {
				h.Set("Strict-Transport-Security", "max-age=31536000")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CrossOriginProtection applies the stdlib CSRF defence to non-/api/ paths with APP_BASE_URL trusted.
func CrossOriginProtection(baseURL string) func(http.Handler) http.Handler {
	cop := http.NewCrossOriginProtection()
	if o := Origin(baseURL); o != "" {
		_ = cop.AddTrustedOrigin(o)
	}
	return func(next http.Handler) http.Handler {
		protected := cop.Handler(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				next.ServeHTTP(w, r)
				return
			}
			protected.ServeHTTP(w, r)
		})
	}
}

// Chain applies middleware so that the first argument is outermost.
func Chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
