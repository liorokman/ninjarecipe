package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/url"
	"time"

	"cooking/internal/domain"
	"cooking/internal/store"
)

func urlEscape(s string) string { return url.QueryEscape(s) }

// Sessions manages opaque server-side sessions (20 §5.3).
type Sessions struct {
	Store  *store.Store
	Clock  domain.Clock
	Idle   time.Duration
	Max    time.Duration
	Secure bool
}

// CookieName is __Host-nwr_session over https, nwr_session over http.
func (m *Sessions) CookieName() string {
	if m.Secure {
		return "__Host-nwr_session"
	}
	return "nwr_session"
}

// CookieNameFor returns the cookie name for a scheme.
func CookieNameFor(secure bool) string {
	if secure {
		return "__Host-nwr_session"
	}
	return "nwr_session"
}

// HashToken is hex(SHA-256(token)).
func HashToken(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

func (m *Sessions) expiry(created, now time.Time) time.Time {
	e := now.Add(m.Idle)
	if abs := created.Add(m.Max); abs.Before(e) {
		return abs
	}
	return e
}

// Create stores a session and sets the cookie.
func (m *Sessions) Create(ctx context.Context, w http.ResponseWriter, r *http.Request, userID, idToken, ip string) error {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	tok := base64.RawURLEncoding.EncodeToString(b)
	now := m.Clock.Now()
	s := domain.Session{IDHash: HashToken(tok), UserID: userID, IDToken: idToken, UserAgent: r.UserAgent(), IP: ip,
		CreatedAt: now, LastSeenAt: now, ExpiresAt: m.expiry(now, now)}
	if len(s.UserAgent) > 300 {
		s.UserAgent = s.UserAgent[:300]
	}
	if err := m.Store.W().CreateSession(ctx, s); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: m.CookieName(), Value: tok, Path: "/", HttpOnly: true, Secure: m.Secure,
		SameSite: http.SameSiteLaxMode, MaxAge: int(m.Max.Seconds())})
	return nil
}

// Lookup resolves the cookie of r into a live session; it slides the expiry when last_seen is older than 10 min.
func (m *Sessions) Lookup(ctx context.Context, r *http.Request) (*domain.Session, *domain.User) {
	ck, err := r.Cookie(m.CookieName())
	if err != nil || ck.Value == "" {
		return nil, nil
	}
	s, u, err := m.Store.R().GetSession(ctx, HashToken(ck.Value))
	if err != nil {
		return nil, nil
	}
	now := m.Clock.Now()
	if !s.ExpiresAt.After(now) || !s.CreatedAt.Add(m.Max).After(now) {
		return nil, nil
	}
	if now.Sub(s.LastSeenAt) > 10*time.Minute {
		exp := m.expiry(s.CreatedAt, now)
		if m.Store.W().TouchSession(ctx, s.IDHash, now, exp) == nil {
			s.LastSeenAt, s.ExpiresAt = now, exp
		}
	}
	return s, u
}

// Destroy deletes the session of r (if any) and clears the cookie.
func (m *Sessions) Destroy(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if ck, err := r.Cookie(m.CookieName()); err == nil {
		_ = m.Store.W().DeleteSession(ctx, HashToken(ck.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: m.CookieName(), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: m.Secure, SameSite: http.SameSiteLaxMode})
}
