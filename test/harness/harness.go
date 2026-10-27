package harness

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"

	"cooking/internal/app"
	"cooking/internal/auth"
	"cooking/internal/config"
	"cooking/internal/domain"
	"cooking/internal/service"
)

// Options configure Start.
type Options struct {
	DataDir       string // reused across restarts when set; temp dir otherwise
	WebDir        string // optional: read templates/static from disk
	Clock         domain.Clock
	AllowedGroups []string
	AdminGroup    string
	IdP           *FakeIdP // reuse a running IdP (restart tests)
	ListenAddr    string   // fixed address for restarts, e.g. 127.0.0.1:port
	Debug         bool
}

// Harness is one running server.
type Harness struct {
	Srv     *app.Server
	HTTP    *httptest.Server
	IdP     *FakeIdP
	Cfg     *config.Config
	URL     string
	DataDir string
	ownIdP  bool
	ownDir  bool
	once    sync.Once
}

// Start launches the real server in-process.
func Start(o Options) (*Harness, error) {
	h := &Harness{DataDir: o.DataDir}
	if h.DataDir == "" {
		d, err := os.MkdirTemp("", "recipes-harness-*")
		if err != nil {
			return nil, err
		}
		h.DataDir, h.ownDir = d, true
	}
	h.IdP = o.IdP
	if h.IdP == nil {
		p, err := NewFakeIdP("recipes", "s3cret")
		if err != nil {
			return nil, err
		}
		h.IdP, h.ownIdP = p, true
	}
	addr := o.ListenAddr
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	h.URL = "http://" + ln.Addr().String()
	allowed := o.AllowedGroups
	admin := o.AdminGroup
	if admin == "" {
		admin = "recipes-admins"
	}
	cfg, _, err := config.Load(func(k string) (string, bool) {
		m := map[string]string{
			"APP_BASE_URL": h.URL, "APP_DATA_DIR": h.DataDir, "APP_LISTEN_ADDR": ln.Addr().String(), "OIDC_ISSUER_URL": h.IdP.Issuer,
			"OIDC_CLIENT_ID": "recipes", "OIDC_CLIENT_SECRET": "s3cret", "OIDC_ADMIN_GROUP": admin, "BACKUP_AUTO": "false", "APP_ENV": "production",
		}
		if len(allowed) > 0 {
			m["OIDC_ALLOWED_GROUPS"] = join(allowed)
		}
		if o.WebDir != "" {
			m["APP_TEMPLATES_DIR"] = o.WebDir
			m["APP_ENV"] = "development"
			m["LOG_FORMAT"] = "text"
		}
		v, ok := m[k]
		return v, ok
	}, nil)
	if err != nil {
		ln.Close()
		return nil, err
	}
	h.Cfg = cfg
	srv, err := app.New(context.Background(), cfg, app.Options{Version: "test", Commit: "testcommit", Date: "now", Clock: o.Clock, AllowPrivateFetch: true})
	if err != nil {
		ln.Close()
		return nil, err
	}
	h.Srv = srv
	h.HTTP = &httptest.Server{Listener: ln, Config: &http.Server{Handler: srv.Handler}}
	h.HTTP.Start()
	return h, nil
}

func join(s []string) string {
	out := ""
	for i, x := range s {
		if i > 0 {
			out += ","
		}
		out += x
	}
	return out
}

// Stop shuts the server down (keeping the data dir and IdP unless owned).
func (h *Harness) Stop() {
	h.once.Do(func() {
		h.HTTP.Close()
		_ = h.Srv.Close()
	})
}

// Close stops everything and removes owned resources.
func (h *Harness) Close() {
	h.Stop()
	if h.ownIdP {
		h.IdP.Close()
	}
	if h.ownDir {
		os.RemoveAll(h.DataDir)
	}
}

// CreateUser inserts a user directly in the DB.
func (h *Harness) CreateUser(name, email string, admin bool, lang string) *domain.User {
	u, err := h.Srv.Svc.Store.W().UpsertUser(context.Background(), uuid.NewString(), domain.User{
		OIDCIssuer: h.IdP.Issuer, OIDCSubject: "sub-" + name, Email: email, DisplayName: name, IsAdmin: admin, UILanguage: lang}, time.Now())
	if err != nil {
		panic(err)
	}
	return u
}

// SessionCookie creates a session row directly and returns the cookie (no login bypass exists in the binary).
func (h *Harness) SessionCookie(u *domain.User) *http.Cookie {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	tok := base64.RawURLEncoding.EncodeToString(b)
	now := time.Now().UTC()
	err := h.Srv.Svc.Store.W().CreateSession(context.Background(), domain.Session{IDHash: auth.HashToken(tok), UserID: u.ID,
		IDToken: "test-id-token", CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(24 * time.Hour)})
	if err != nil {
		panic(err)
	}
	return &http.Cookie{Name: auth.CookieNameFor(h.Cfg.Secure()), Value: tok, Path: "/"}
}

// Token creates an API token for u and returns the plain value.
func (h *Harness) Token(u *domain.User, scopes ...string) string {
	if len(scopes) == 0 {
		scopes = []string{domain.ScopeInboxWrite, domain.ScopeRecipesRead}
	}
	plain, _, err := h.Srv.Svc.CreateToken(context.Background(), u, "harness", scopes, 0)
	if err != nil {
		panic(err)
	}
	return plain
}

// Client is an HTTP client for one identity that does not follow redirects.
type Client struct {
	HTTP   *http.Client
	Base   string
	Bearer string
}

// NewClient builds a client; cookie may be nil (anonymous).
func (h *Harness) NewClient(cookie *http.Cookie) *Client {
	jar, _ := cookiejar.New(nil)
	c := &Client{Base: h.URL, HTTP: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Timeout: 30 * time.Second}}
	if cookie != nil {
		jar.SetCookies(mustURL(h.URL), []*http.Cookie{cookie})
	}
	return c
}

// Seed is a convenience wrapper around service calls used by tests.
func (h *Harness) Svc() *service.Service { return h.Srv.Svc }

func mustURL(s string) *urlT {
	u, err := parseURL(s)
	if err != nil {
		panic(fmt.Sprint(err))
	}
	return u
}

func (h *Harness) nowFn() time.Time { return time.Now().UTC() }
