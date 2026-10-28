// Package store contains the SQL repositories. All methods hang off Q, which wraps a *sql.DB or *sql.Tx.
package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/liorokman/ninjarecipe/internal/domain"
)

// Querier is satisfied by *sql.DB and *sql.Tx.
type Querier interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

// Q groups the repository methods over a Querier.
type Q struct{ q Querier }

// Store owns the two pools.
type Store struct{ reader, writer *sql.DB }

// New builds a store from the two pools.
func New(reader, writer *sql.DB) *Store { return &Store{reader: reader, writer: writer} }

// R returns the read-only repository (reader pool).
func (s *Store) R() Q { return Q{s.reader} }

// W returns a repository on the writer pool outside a transaction (single statements).
func (s *Store) W() Q { return Q{s.writer} }

// Tx is a write transaction with post-commit hooks (file deletion, events).
type Tx struct {
	Q
	after []func()
}

// AfterCommit queues f to run after a successful commit (never on rollback).
func (t *Tx) AfterCommit(f func()) { t.after = append(t.after, f) }

// WithTx runs fn in BEGIN IMMEDIATE (via _txlock=immediate) on the writer.
func (s *Store) WithTx(ctx context.Context, fn func(tx *Tx) error) error {
	sqlTx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	tx := &Tx{Q: Q{sqlTx}}
	if err := fn(tx); err != nil {
		_ = sqlTx.Rollback()
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return err
	}
	for _, f := range tx.after {
		f()
	}
	return nil
}

// ---- time helpers (30 §1) ----

const timeLayout = "2006-01-02T15:04:05.000Z07:00"

// FormatTime renders a UTC millisecond RFC 3339 timestamp.
func FormatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// ParseTime parses a stored timestamp (tolerant of missing millis).
func ParseTime(s string) time.Time {
	for _, l := range []string{timeLayout, time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func parseNullTime(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	t := ParseTime(s.String)
	return &t
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return FormatTime(*t)
}

func intPtr(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// placeholders returns "?,?,?" for n items.
func placeholders(n int) string {
	if n == 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func strArgs(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// notFound maps sql.ErrNoRows to domain.ErrNotFound.
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}
