// Command recipes is the Ninja Woodfire recipe server and its admin CLI (20 §17).
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"cooking/internal/app"
	"cooking/internal/config"
	"cooking/internal/db"
	"cooking/internal/domain"
	"cooking/internal/jobs"
	"cooking/internal/service"
	"cooking/internal/store"
)

// Set by -ldflags "-X main.version=... -X main.commit=... -X main.date=...".
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const (
	exitConfig = 2
	exitNewer  = 3
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv, os.Environ())) }

func run(args []string, stdout, stderr io.Writer, getenv func(string) string, environ []string) int {
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		return serve(stdout, stderr, environ)
	case "healthcheck":
		return healthcheck(getenv("APP_LISTEN_ADDR"), stderr)
	case "version":
		fmt.Fprintf(stdout, "recipes %s (commit %s, built %s)\n", version, commit, date)
		return 0
	case "migrate", "reindex", "backup", "user":
		return cli(cmd, args, stdout, stderr, getenv)
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, "usage: recipes [serve|healthcheck|migrate status|migrate up|reindex|backup [--out DIR]|user list|user revoke-sessions <id|email>|user delete <id> [--yes]|version]")
		return 0
	}
	fmt.Fprintf(stderr, "unknown command %q\n", cmd)
	return 1
}

func setupLogging(cfg *config.Config, w io.Writer) {
	var lvl slog.Level
	_ = lvl.UnmarshalText([]byte(cfg.LogLevel))
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler = slog.NewJSONHandler(w, opts)
	if cfg.LogFormat == "text" {
		h = slog.NewTextHandler(w, opts)
	}
	slog.SetDefault(slog.New(h))
}

func serve(stdout, stderr io.Writer, environ []string) int {
	cfg, warns, err := config.Load(config.OSLookup, environ)
	if err != nil {
		fmt.Fprintf(stderr, "configuration error:\n%v\n", err)
		return exitConfig
	}
	setupLogging(cfg, stdout)
	for _, w := range warns {
		slog.Warn(w)
	}
	slog.Info("starting", "version", version, "commit", commit, "config", cfg)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	srv, err := app.New(ctx, cfg, app.Options{Version: version, Commit: commit, Date: date})
	if err != nil {
		if errors.Is(err, db.ErrNewer) {
			fmt.Fprintf(stderr, "refusing to start: %v\n", err)
			return exitNewer
		}
		fmt.Fprintf(stderr, "startup failed: %v\n", err)
		return 1
	}
	hs := srv.HTTPServer()
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	slog.Info("listening", "addr", cfg.ListenAddr)
	select {
	case err := <-errc:
		slog.Error("server stopped", "error", err)
		_ = srv.Close()
		return 1
	case <-ctx.Done():
		slog.Info("shutting down")
		if err := srv.Shutdown(hs); err != nil {
			slog.Warn("shutdown", "error", err)
		}
		return 0
	}
}

func healthcheck(addr string, stderr io.Writer) int {
	if addr == "" {
		addr = ":8080"
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Fprintf(stderr, "bad APP_LISTEN_ADDR %q\n", addr)
		return 1
	}
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 1
	}
	return 0
}

// ---- CLI against the same DB ----

func cli(cmd string, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	dir := getenv("APP_DATA_DIR")
	if dir == "" {
		dir = "/data"
	}
	ctx := context.Background()
	d, err := db.Open(dir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer d.Close()
	if cmd == "migrate" {
		return migrate(ctx, d, args, stdout, stderr)
	}
	if err := d.Migrate(ctx); err != nil {
		fmt.Fprintln(stderr, err)
		if errors.Is(err, db.ErrNewer) {
			return exitNewer
		}
		return 1
	}
	svc := service.New(store.New(d.Reader, d.Writer), dir, "", domain.SystemClock{})
	switch cmd {
	case "reindex":
		n, err := svc.Reindex(ctx)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "reindexed %d recipes\n", n)
	case "backup":
		fs := flag.NewFlagSet("backup", flag.ContinueOnError)
		fs.SetOutput(stderr)
		out := fs.String("out", filepath.Join(dir, "backups"), "output directory")
		if fs.Parse(args) != nil {
			return 1
		}
		keep := 7
		if n, err := strconv.Atoi(getenv("BACKUP_KEEP")); err == nil && n > 0 {
			keep = n
		}
		p, err := jobs.Backup(ctx, d, svc, *out, keep, time.Now())
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, p)
	case "user":
		return userCmd(ctx, svc, args, stdout, stderr)
	}
	return 0
}

func migrate(ctx context.Context, d *db.DB, args []string, stdout, stderr io.Writer) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "status":
		s, err := d.Status(ctx)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprint(stdout, s)
	case "up":
		if err := d.Migrate(ctx); err != nil {
			fmt.Fprintln(stderr, err)
			if errors.Is(err, db.ErrNewer) {
				return exitNewer
			}
			return 1
		}
		fmt.Fprintln(stdout, "ok")
	default:
		fmt.Fprintln(stderr, "usage: recipes migrate status|up")
		return 1
	}
	return 0
}

func userCmd(ctx context.Context, svc *service.Service, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: recipes user list|revoke-sessions <id|email>|delete <id> [--yes]")
		return 1
	}
	q := svc.Store.R()
	switch args[0] {
	case "list":
		us, err := q.ListUsers(ctx)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tEMAIL\tADMIN\tLAST LOGIN")
		for _, u := range us {
			ll := "-"
			if u.LastLoginAt != nil {
				ll = store.FormatTime(*u.LastLoginAt)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%v\t%s\n", u.ID, u.DisplayName, u.Email, u.IsAdmin, ll)
		}
		tw.Flush()
	case "revoke-sessions", "delete":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "missing user id or email")
			return 1
		}
		u, err := q.FindUser(ctx, args[1])
		if err != nil {
			fmt.Fprintln(stderr, "no such user")
			return 1
		}
		if args[0] == "revoke-sessions" {
			n, err := svc.Store.W().DeleteUserSessions(ctx, u.ID)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			fmt.Fprintf(stdout, "revoked %d sessions of %s\n", n, u.DisplayName)
			return 0
		}
		plan, err := svc.PlanUserDeletion(ctx, u)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "Will delete user %s (%s):\n  - %d private/inbox recipes (with photos)\n  - %d shared recipes become ownerless\n  - sessions, access keys, cook logs, favourites\n",
			u.DisplayName, u.ID, plan.DeleteRecipes, plan.Ownerless)
		yes := len(args) > 2 && args[2] == "--yes"
		if !yes {
			fmt.Fprint(stdout, "Type 'yes' to continue: ")
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			if strings.TrimSpace(line) != "yes" {
				fmt.Fprintln(stdout, "aborted")
				return 1
			}
		}
		if err := svc.DeleteUser(ctx, u); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, "deleted")
	default:
		fmt.Fprintln(stderr, "unknown user command")
		return 1
	}
	return 0
}
