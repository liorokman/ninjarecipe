package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/liorokman/ninjarecipe/internal/db"
	"github.com/liorokman/ninjarecipe/internal/domain"
)

func open(t *testing.T) (*Store, *db.DB) {
	t.Helper()
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return New(d.Reader, d.Writer), d
}

func TestTimeFormat(t *testing.T) {
	ts := time.Date(2026, 10, 27, 13, 30, 0, 123_000_000, time.FixedZone("x", 3*3600))
	if got := FormatTime(ts); got != "2026-10-27T10:30:00.123Z" {
		t.Fatal(got)
	}
	if !ParseTime("2026-10-27T10:30:00.123Z").Equal(ts) || ParseTime("garbage") != (time.Time{}) {
		t.Fatal("parse")
	}
	if !ParseTime("2026-10-27T10:30:00Z").Equal(ts.Truncate(time.Second)) {
		t.Fatal("tolerant parse")
	}
}

func TestWithTxCommitRollbackAndHooks(t *testing.T) {
	s, d := open(t)
	ctx := context.Background()
	var ran []string
	err := s.WithTx(ctx, func(tx *Tx) error {
		if _, err := tx.ExecRaw(ctx, `INSERT INTO users (id, oidc_issuer, oidc_subject, display_name) VALUES ('u','i','s','U')`); err != nil {
			return err
		}
		tx.AfterCommit(func() { ran = append(ran, "commit") })
		return nil
	})
	if err != nil || len(ran) != 1 {
		t.Fatalf("%v %v", err, ran)
	}
	err = s.WithTx(ctx, func(tx *Tx) error {
		_, _ = tx.ExecRaw(ctx, `INSERT INTO users (id, oidc_issuer, oidc_subject, display_name) VALUES ('u2','i','s2','U2')`)
		tx.AfterCommit(func() { ran = append(ran, "rollback-hook") })
		return errors.New("fail")
	})
	var n int
	d.Reader.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	if err == nil || n != 1 || len(ran) != 1 {
		t.Fatalf("rollback: n=%d ran=%v", n, ran)
	}
}

func TestSessionsStatesAndTokens(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	u, err := s.W().UpsertUser(ctx, "uid", domain.User{OIDCIssuer: "i", OIDCSubject: "s", Email: "e", DisplayName: "N", UILanguage: "he"}, now)
	if err != nil || u.ID != "uid" || u.UILanguage != "he" {
		t.Fatal(err, u)
	}
	// sessions
	sess := domain.Session{IDHash: "h1", UserID: u.ID, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := s.W().CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	got, gu, err := s.R().GetSession(ctx, "h1")
	if err != nil || gu.ID != u.ID || !got.ExpiresAt.Equal(sess.ExpiresAt) {
		t.Fatal(err)
	}
	_ = s.W().TouchSession(ctx, "h1", now.Add(time.Minute), now.Add(2*time.Hour))
	got, _, _ = s.R().GetSession(ctx, "h1")
	if !got.ExpiresAt.Equal(now.Add(2 * time.Hour)) {
		t.Fatal("touch")
	}
	if _, _, err := s.R().GetSession(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("missing session")
	}
	// login states are single use
	_ = s.W().PutLoginState(ctx, domain.OIDCState{State: "st", Verifier: "v", Nonce: "n", ReturnTo: "/x", ExpiresAt: now.Add(10 * time.Minute)})
	if st, err := s.W().TakeLoginState(ctx, "st"); err != nil || st.ReturnTo != "/x" {
		t.Fatal(err)
	}
	if _, err := s.W().TakeLoginState(ctx, "st"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("state reuse")
	}
	// purge
	_ = s.W().PutLoginState(ctx, domain.OIDCState{State: "old", ExpiresAt: now.Add(-time.Hour)})
	n, err := s.W().PurgeExpired(ctx, now.Add(3*time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("purge %d %v", n, err)
	}
	// tokens: touch at most once a minute
	tk := domain.APIToken{ID: "t1", UserID: u.ID, Name: "n", Hash: "hh", Prefix: "pppppppp", Scopes: []string{"inbox:write", "recipes:read"}, CreatedAt: now}
	if err := s.W().CreateToken(ctx, tk); err != nil {
		t.Fatal(err)
	}
	_ = s.W().TouchToken(ctx, "t1", now)
	_ = s.W().TouchToken(ctx, "t1", now.Add(30*time.Second))
	g, _ := s.R().TokenByHash(ctx, "hh")
	if g.LastUsedAt == nil || !g.LastUsedAt.Equal(now) || len(g.Scopes) != 2 {
		t.Fatalf("touch throttle: %+v", g)
	}
	_ = s.W().TouchToken(ctx, "t1", now.Add(2*time.Minute))
	g, _ = s.R().TokenByHash(ctx, "hh")
	if !g.LastUsedAt.Equal(now.Add(2 * time.Minute)) {
		t.Fatal("touch after a minute")
	}
	if err := s.W().RevokeToken(ctx, "t1", "someone-else", now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("only the owner may revoke")
	}
	if c, _ := s.R().CountActiveTokens(ctx, u.ID, now); c != 1 {
		t.Fatal("active count")
	}
	// users
	if _, err := s.R().FindUser(ctx, "E"); err != nil {
		t.Fatal("email lookup is case-insensitive")
	}
	if n, _ := s.W().DeleteUserSessions(ctx, u.ID); n != 0 { // already purged above
		t.Fatal("delete sessions")
	}
}

func TestVersionedUpdateAndMissing(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	_, _ = s.W().UpsertUser(ctx, "u", domain.User{OIDCIssuer: "i", OIDCSubject: "s", DisplayName: "N"}, now)
	r := &domain.Recipe{ID: "r1", OwnerID: "u", Status: "active", Visibility: "private", Title: "A", Language: "und", Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := s.W().InsertRecipe(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.Title = "B"
	if err := s.W().UpdateRecipe(ctx, r); err != nil || r.Version != 2 {
		t.Fatalf("%v %d", err, r.Version)
	}
	stale := *r
	stale.Version = 1
	if err := s.W().UpdateRecipe(ctx, &stale); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("stale")
	}
	ghost := *r
	ghost.ID = "nope"
	if err := s.W().UpdateRecipe(ctx, &ghost); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("ghost")
	}
	if _, err := s.W().DeleteRecipe(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("delete ghost")
	}
}
