// Package db opens the SQLite pools (20 §14), runs goose migrations and guards against newer databases.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrNewer means the database has a higher migration version than this binary (exit code 3).
var ErrNewer = errors.New("database is newer than this binary")

// DB holds the writer (1 conn) and reader (4 conns) pools.
type DB struct {
	Writer *sql.DB
	Reader *sql.DB
	Path   string
	Dir    string
}

func dsn(path string) string {
	return "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_txlock=immediate"
}

// Open opens recipes.db in dir (creating dir) with both pools. It does not migrate.
func Open(dir string) (*DB, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	p := filepath.Join(dir, "recipes.db")
	w, err := sql.Open("sqlite", dsn(p))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	r, err := sql.Open("sqlite", dsn(p))
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(4)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := w.PingContext(ctx); err != nil {
		w.Close()
		r.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	return &DB{Writer: w, Reader: r, Path: p, Dir: dir}, nil
}

func (d *DB) provider() (*goose.Provider, error) {
	sub, err := fsSub()
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(goose.DialectSQLite3, d.Writer, sub)
}

// Migrate applies all pending migrations after checking that the DB is not newer than the binary.
func (d *DB) Migrate(ctx context.Context) error {
	if err := d.CheckVersion(ctx); err != nil {
		return err
	}
	p, err := d.provider()
	if err != nil {
		return err
	}
	_, err = p.Up(ctx)
	return err
}

// CheckVersion returns ErrNewer when goose_db_version is ahead of the embedded migrations.
func (d *DB) CheckVersion(ctx context.Context) error {
	p, err := d.provider()
	if err != nil {
		return err
	}
	cur, err := p.GetDBVersion(ctx)
	if err != nil {
		// no goose table yet -> fresh DB
		return nil
	}
	srcs := p.ListSources()
	var latest int64
	for _, s := range srcs {
		if s.Version > latest {
			latest = s.Version
		}
	}
	if cur > latest {
		return fmt.Errorf("%w (db version %d, binary %d)", ErrNewer, cur, latest)
	}
	return nil
}

// Status describes migration state for `migrate status`.
func (d *DB) Status(ctx context.Context) (string, error) {
	p, err := d.provider()
	if err != nil {
		return "", err
	}
	st, err := p.Status(ctx)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, s := range st {
		fmt.Fprintf(&b, "%04d %-40s %s\n", s.Source.Version, filepath.Base(s.Source.Path), s.State)
	}
	return b.String(), nil
}

// Ping checks both pools.
func (d *DB) Ping(ctx context.Context) error {
	if err := d.Writer.PingContext(ctx); err != nil {
		return err
	}
	var one int
	return d.Reader.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

// Close checkpoints the WAL and closes both pools.
func (d *DB) Close() error {
	_, _ = d.Writer.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	e1 := d.Reader.Close()
	e2 := d.Writer.Close()
	return errors.Join(e1, e2)
}

// Checkpoint truncates the WAL (shutdown).
func (d *DB) Checkpoint() { _, _ = d.Writer.Exec("PRAGMA wal_checkpoint(TRUNCATE)") }

// Backup writes a consistent snapshot with VACUUM INTO and verifies it with integrity_check.
func (d *DB) Backup(ctx context.Context, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	_ = os.Remove(dest)
	if _, err := d.Reader.ExecContext(ctx, "VACUUM INTO '"+strings.ReplaceAll(dest, "'", "''")+"'"); err != nil {
		return fmt.Errorf("vacuum into: %w", err)
	}
	snap, err := sql.Open("sqlite", "file:"+dest+"?mode=ro")
	if err != nil {
		return err
	}
	defer snap.Close()
	var res string
	if err := snap.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&res); err != nil || res != "ok" {
		_ = os.Remove(dest)
		return fmt.Errorf("integrity_check failed: %v %s", err, res)
	}
	return nil
}
