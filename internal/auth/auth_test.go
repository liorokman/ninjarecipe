package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/liorokman/ninjarecipe/internal/config"
	"github.com/liorokman/ninjarecipe/internal/db"
	"github.com/liorokman/ninjarecipe/internal/domain"
	"github.com/liorokman/ninjarecipe/internal/store"
)

func TestBearerToken(t *testing.T) {
	for in, want := range map[string]string{"Bearer abc": "abc", "bearer  abc ": "abc", "Basic abc": "", "": "", "Bearer": ""} {
		r := httptest.NewRequest("GET", "/", nil)
		if in != "" {
			r.Header.Set("Authorization", in)
		}
		if got := BearerToken(r); got != want {
			t.Errorf("%q -> %q", in, got)
		}
	}
}

func TestGate(t *testing.T) {
	cfg := &config.Config{OIDCAllowedGroups: []string{"family"}, OIDCAdminGroup: "admins"}
	for _, c := range []struct {
		groups         []string
		allowed, admin bool
	}{{nil, false, false}, {[]string{"other"}, false, false}, {[]string{"family"}, true, false}, {[]string{"family", "admins"}, true, true}, {[]string{"admins"}, false, true}} {
		if a, ad := Gate(cfg, c.groups); a != c.allowed || ad != c.admin {
			t.Errorf("%v: %v %v", c.groups, a, ad)
		}
	}
	open := &config.Config{OIDCAdminGroup: ""}
	if a, ad := Gate(open, nil); !a || ad {
		t.Fatal("no restriction, no admin group")
	}
}

func TestIdentityDisplayName(t *testing.T) {
	if (Identity{Name: "N", PreferredUsername: "p", Email: "e"}).DisplayName() != "N" || (Identity{PreferredUsername: "p", Email: "e"}).DisplayName() != "p" ||
		(Identity{Email: "e"}).DisplayName() != "e" || (Identity{}).DisplayName() != "" {
		t.Fatal("fallback order")
	}
}

func TestEndSessionURL(t *testing.T) {
	o := NewOIDC(&config.Config{BaseURL: "https://r.example.com"})
	if o.EndSessionURL("tok") != "" {
		t.Fatal("no endpoint -> empty")
	}
	o.endSession = "https://auth.example.com/end/"
	u := o.EndSessionURL("a b")
	if !strings.HasPrefix(u, "https://auth.example.com/end/?") || !strings.Contains(u, "id_token_hint=a+b") || !strings.Contains(u, "post_logout_redirect_uri=https%3A%2F%2Fr.example.com%2Fauth%2Flogged-out") {
		t.Fatal(u)
	}
	if o.Status() != "pending" {
		t.Fatal("status")
	}
	if _, err := o.AuthURL(context.Background(), "s", "n", "v"); err == nil {
		t.Fatal("discovery against an unreachable issuer must fail (ErrUnavailable)")
	}
}

type clk struct{ t time.Time }

func (c *clk) Now() time.Time { return c.t }

func TestSessionLifecycle(t *testing.T) {
	d, _ := db.Open(t.TempDir())
	defer d.Close()
	_ = d.Migrate(context.Background())
	st := store.New(d.Reader, d.Writer)
	ctx := context.Background()
	c := &clk{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	u, _ := st.W().UpsertUser(ctx, "u1", domain.User{OIDCIssuer: "i", OIDCSubject: "s", DisplayName: "U"}, c.t)
	m := &Sessions{Store: st, Clock: c, Idle: 30 * 24 * time.Hour, Max: 100 * 24 * time.Hour, Secure: true}
	if m.CookieName() != "__Host-nwr_session" || CookieNameFor(false) != "nwr_session" {
		t.Fatal("cookie names")
	}
	w := httptest.NewRecorder()
	if err := m.Create(ctx, w, httptest.NewRequest("GET", "/", nil), u.ID, "idt", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	ck := w.Result().Cookies()[0]
	if !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/" || len(ck.Value) != 43 {
		t.Fatalf("cookie flags: %+v", ck)
	}
	var stored string
	d.Reader.QueryRow(`SELECT id_hash FROM sessions`).Scan(&stored)
	if stored == ck.Value || stored != HashToken(ck.Value) {
		t.Fatal("raw token must not be stored")
	}
	req := func() *http.Request { r := httptest.NewRequest("GET", "/", nil); r.AddCookie(ck); return r }
	if s, usr := m.Lookup(ctx, req()); s == nil || usr.ID != u.ID || s.IDToken != "idt" {
		t.Fatal("lookup")
	}
	// sliding: after >10 min last_seen and expiry advance, capped at created+max
	c.t = c.t.Add(11 * time.Minute)
	s, _ := m.Lookup(ctx, req())
	if !s.LastSeenAt.Equal(c.t) || !s.ExpiresAt.Equal(c.t.Add(m.Idle)) {
		t.Fatalf("slide: %+v", s)
	}
	for i := 0; i < 3; i++ {
		c.t = c.t.Add(25 * 24 * time.Hour)
		s, _ = m.Lookup(ctx, req())
	}
	c.t = c.t.Add(20 * 24 * time.Hour)
	s, _ = m.Lookup(ctx, req())
	if s == nil || !s.ExpiresAt.Equal(s.CreatedAt.Add(m.Max)) {
		t.Fatalf("absolute cap: %+v", s)
	}
	c.t = c.t.Add(10 * 24 * time.Hour)
	if s, _ := m.Lookup(ctx, req()); s != nil {
		t.Fatal("past the absolute lifetime")
	}
	if s, _ := m.Lookup(ctx, httptest.NewRequest("GET", "/", nil)); s != nil {
		t.Fatal("no cookie")
	}
	m.Destroy(ctx, httptest.NewRecorder(), req())
	var n int
	d.Reader.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n)
	if n != 0 {
		t.Fatal("destroy")
	}
}
