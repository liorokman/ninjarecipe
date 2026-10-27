package db

import (
	"context"
	"errors"
	"testing"
)

func openMigrated(t *testing.T) *DB {
	t.Helper()
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestFreshMigrateSeedsCategories(t *testing.T) {
	d := openMigrated(t)
	var cats, tags int
	d.Reader.QueryRow(`SELECT COUNT(*) FROM labels WHERE type='category'`).Scan(&cats)
	d.Reader.QueryRow(`SELECT COUNT(*) FROM labels WHERE type='tag'`).Scan(&tags)
	if cats != 12 || tags != 0 {
		t.Fatalf("seed: %d categories, %d tags", cats, tags)
	}
	var v int
	if err := d.Reader.QueryRow(`SELECT MAX(version_id) FROM goose_db_version`).Scan(&v); err != nil || v != 2 {
		t.Fatalf("goose version %d err %v", v, err)
	}
	// idempotent
	if err := d.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestNewerDatabaseRefused(t *testing.T) {
	d := openMigrated(t)
	if _, err := d.Writer.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (99, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := d.Migrate(context.Background()); !errors.Is(err, ErrNewer) {
		t.Fatalf("want ErrNewer, got %v", err)
	}
}

func TestSchemaConstraints(t *testing.T) {
	d := openMigrated(t)
	exec := func(q string, a ...any) error { _, err := d.Writer.Exec(q, a...); return err }
	if err := exec(`INSERT INTO users (id, oidc_issuer, oidc_subject, display_name) VALUES ('u1','i','s','U')`); err != nil {
		t.Fatal(err)
	}
	// inbox items must be private
	if err := exec(`INSERT INTO recipes (id, owner_id, status, visibility, title) VALUES ('r1','u1','inbox','shared','x')`); err == nil {
		t.Fatal("inbox+shared must violate CHECK")
	}
	if err := exec(`INSERT INTO recipes (id, owner_id, status, visibility, title) VALUES ('r1','u1','archived','private','x')`); err == nil {
		t.Fatal("archived status must not exist")
	}
	if err := exec(`INSERT INTO recipes (id, owner_id, title) VALUES ('r1','u1','x')`); err != nil {
		t.Fatal(err)
	}
	// temperature coarse guard 20..400; duration >= 0
	if err := exec(`INSERT INTO oven_stages (recipe_id, position, mode, temperature_c) VALUES ('r1',1,'broil',500)`); err == nil {
		t.Fatal("temperature 500 must violate CHECK")
	}
	if err := exec(`INSERT INTO oven_stages (recipe_id, position, mode, temperature_c, duration_seconds) VALUES ('r1',1,'broil',370,60)`); err != nil {
		t.Fatal(err)
	}
	// email is not unique
	if err := exec(`INSERT INTO users (id, oidc_issuer, oidc_subject, display_name, email) VALUES ('u2','i','s2','U2','a@b')`); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO users (id, oidc_issuer, oidc_subject, display_name, email) VALUES ('u3','i','s3','U3','a@b')`); err != nil {
		t.Fatalf("email must not be unique: %v", err)
	}
	// FTS table with trigram works
	if err := exec(`INSERT INTO recipe_fts (recipe_id, title, description, ingredients, steps, labels, notes) VALUES ('r1','עופות','','','','','')`); err != nil {
		t.Fatal(err)
	}
	var n int
	d.Reader.QueryRow(`SELECT COUNT(*) FROM recipe_fts WHERE recipe_fts MATCH '"עופ"'`).Scan(&n)
	if n != 1 {
		t.Fatalf("trigram match: %d", n)
	}
}

func TestBackup(t *testing.T) {
	d := openMigrated(t)
	dest := t.TempDir() + "/b.db"
	if err := d.Backup(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
}
