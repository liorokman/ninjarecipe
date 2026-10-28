// Package jobs runs the background maintenance goroutines: hourly purge, nightly sweeper, daily backup (20 §16).
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/liorokman/ninjarecipe/internal/db"
	"github.com/liorokman/ninjarecipe/internal/domain"
	"github.com/liorokman/ninjarecipe/internal/service"
)

// Backup writes one snapshot (VACUUM INTO + integrity_check + media key list) and prunes beyond keep.
// out is the target directory. Returns the snapshot path.
func Backup(ctx context.Context, d *db.DB, svc *service.Service, out string, keep int, now time.Time) (string, error) {
	ts := now.UTC().Format("20060102T150405Z")
	path := filepath.Join(out, "recipes-"+ts+".db")
	if err := d.Backup(ctx, path); err != nil {
		return "", err
	}
	keys, err := svc.Store.R().AllImageKeys(ctx)
	if err != nil {
		return path, err
	}
	var ks []string
	for k := range keys {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	if err := os.WriteFile(filepath.Join(out, "recipes-"+ts+".media.txt"), []byte(strings.Join(ks, "\n")+"\n"), 0o644); err != nil {
		return path, err
	}
	Prune(out, keep)
	return path, nil
}

// Prune keeps the newest `keep` snapshots (by file name) and deletes older ones with their media lists.
func Prune(dir string, keep int) {
	ents, _ := os.ReadDir(dir)
	var snaps []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "recipes-") && strings.HasSuffix(e.Name(), ".db") {
			snaps = append(snaps, e.Name())
		}
	}
	sort.Strings(snaps)
	for len(snaps) > keep {
		old := snaps[0]
		snaps = snaps[1:]
		_ = os.Remove(filepath.Join(dir, old))
		_ = os.Remove(filepath.Join(dir, strings.TrimSuffix(old, ".db")+".media.txt"))
	}
}

// Runner owns the background goroutines.
type Runner struct {
	DB         *db.DB
	Svc        *service.Service
	Clock      domain.Clock
	TZ         *time.Location
	BackupAuto bool
	BackupAt   string
	BackupKeep int
	wg         sync.WaitGroup
}

func (r *Runner) logRun(name string, start time.Time, err error, kv ...any) {
	args := append([]any{"job", name, "duration_ms", time.Since(start).Milliseconds()}, kv...)
	if err != nil {
		slog.Error("job failed", append(args, "error", err)...)
		return
	}
	slog.Info("job done", args...)
}

// Start launches the jobs; they stop when ctx is cancelled (use Wait to join).
func (r *Runner) Start(ctx context.Context) {
	r.loop(ctx, time.Hour, "hourly-purge", func() {
		st := time.Now()
		n, err := r.Svc.PurgeHourly(ctx)
		r.logRun("hourly-purge", st, err, "purged", n)
	})
	r.loop(ctx, 24*time.Hour, "nightly-sweeper", func() {
		st := time.Now()
		n, err := r.Svc.Sweep(ctx)
		r.logRun("nightly-sweeper", st, err, "removed", n)
	})
	if r.BackupAuto {
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			for {
				wait := NextRun(r.Clock.Now().In(r.TZ), r.BackupAt)
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
				st := time.Now()
				_, err := Backup(ctx, r.DB, r.Svc, filepath.Join(r.Svc.DataDir, "backups"), r.BackupKeep, r.Clock.Now())
				r.logRun("backup", st, err)
			}
		}()
	}
}

// Wait blocks until all jobs returned.
func (r *Runner) Wait() { r.wg.Wait() }

func (r *Runner) loop(ctx context.Context, every time.Duration, name string, f func()) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				f()
			}
		}
	}()
}

// NextRun returns the duration until the next HH:MM in now's location.
func NextRun(now time.Time, hhmm string) time.Duration {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		return 24 * time.Hour
	}
	next := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next.Sub(now)
}

var _ = fmt.Sprint
