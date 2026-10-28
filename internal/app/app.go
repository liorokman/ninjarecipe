// Package app wires the whole server (used by cmd/recipes and the test harnesses).
package app

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/liorokman/ninjarecipe/internal/api"
	"github.com/liorokman/ninjarecipe/internal/auth"
	"github.com/liorokman/ninjarecipe/internal/config"
	"github.com/liorokman/ninjarecipe/internal/db"
	"github.com/liorokman/ninjarecipe/internal/domain"
	"github.com/liorokman/ninjarecipe/internal/events"
	"github.com/liorokman/ninjarecipe/internal/httpx"
	"github.com/liorokman/ninjarecipe/internal/i18n"
	"github.com/liorokman/ninjarecipe/internal/images"
	"github.com/liorokman/ninjarecipe/internal/jobs"
	"github.com/liorokman/ninjarecipe/internal/service"
	"github.com/liorokman/ninjarecipe/internal/store"
	"github.com/liorokman/ninjarecipe/internal/web"
	"github.com/liorokman/ninjarecipe/locales"
	webfs "github.com/liorokman/ninjarecipe/web"
)

// Options tweak the wiring (tests).
type Options struct {
	Version, Commit, Date string
	Clock                 domain.Clock
	// AllowPrivateFetch lets image URL fetching reach loopback/LAN (tests only).
	AllowPrivateFetch bool
}

// Server is the assembled application.
type Server struct {
	Cfg      *config.Config
	DB       *db.DB
	Svc      *service.Service
	Web      *web.App
	OIDC     *auth.OIDC
	Sessions *auth.Sessions
	Bus      *events.Bus
	Jobs     *jobs.Runner
	Handler  http.Handler
	cancel   context.CancelFunc
}

func localesFS(cfg *config.Config) fs.FS {
	if cfg.TemplatesDir != "" {
		p := filepath.Join(cfg.TemplatesDir, "..", "locales")
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return os.DirFS(p)
		}
	}
	return locales.FS
}

// New opens the DB, migrates (errors wrap db.ErrNewer when the DB is newer) and builds the handler chain.
// OIDC discovery is lazy; the server starts without the IdP.
func New(ctx context.Context, cfg *config.Config, o Options) (*Server, error) {
	images.EmptyTmp(cfg.DataDir)
	d, err := db.Open(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	if err := d.Migrate(ctx); err != nil {
		d.Close()
		return nil, err
	}
	st := store.New(d.Reader, d.Writer)
	clock := o.Clock
	if clock == nil {
		clock = domain.SystemClock{}
	}
	svc := service.New(st, cfg.DataDir, cfg.BaseURL, clock)
	svc.MaxImages = cfg.MaxImagesPerRecipe
	svc.Proc = images.NewProcessor(cfg.ImageWorkers, cfg.ImageMaxPixels, int64(cfg.MaxImageMB)<<20)
	ver := o.Version
	if ver == "" {
		ver = "dev"
	}
	svc.Fetcher = &images.Fetcher{AllowHTTP: cfg.ImageFetchAllowHTTP, Timeout: cfg.ImageFetchTimeout, MaxBytes: int64(cfg.MaxImageMB) << 20, UserAgent: "ninja-recipes/" + ver}
	if o.AllowPrivateFetch {
		svc.Fetcher.AllowAll = true
	}
	svc.Bus = events.NewBus(100)

	cat, err := i18n.Load(localesFS(cfg))
	if err != nil {
		d.Close()
		return nil, err
	}
	asset := o.Commit
	if len(asset) > 8 {
		asset = asset[:8]
	}
	if asset == "" || asset == "none" || cfg.Dev() {
		asset = "dev"
	}
	rd := web.NewRenderer(web.RendererOpts{FS: webfs.FS, Dir: cfg.TemplatesDir, Catalog: cat, AssetVersion: asset, BaseURL: cfg.BaseURL,
		TZ: cfg.TZ, DevMode: cfg.Dev(), Clock: clock, DefaultLang: cfg.DefaultLang,
		InboxCount: func(r *http.Request, u *domain.User) int { return svc.InboxCount(r.Context(), u) }})
	oidc := auth.NewOIDC(cfg)
	sessions := &auth.Sessions{Store: st, Clock: clock, Idle: cfg.SessionIdleTTL, Max: cfg.SessionMaxTTL, Secure: cfg.Secure()}
	app := &web.App{Cfg: cfg, Svc: svc, Cat: cat, R: rd, OIDC: oidc, Sessions: sessions, DB: d, Version: ver, Commit: o.Commit, BuildDate: o.Date}
	apiApp := api.New(svc, cat, cfg.BaseURL, cfg.APIMaxBodyMB, cfg.APIRateLimitPerMin)

	mux := http.NewServeMux()
	app.Register(mux)
	apiApp.Register(mux)
	router := web.Router(mux, app.NotFound, apiApp.NotFound)
	ip := httpx.NewIPParser(prefixStrings(cfg))
	h := httpx.Chain(router,
		httpx.Recover,
		httpx.RequestID,
		httpx.AccessLog(ip),
		httpx.HostCheck(cfg.BaseURL),
		httpx.SecurityHeaders(cfg.BaseURL, cfg.OIDCIssuer),
		httpx.CrossOriginProtection(cfg.BaseURL),
		auth.Middleware(sessions, svc),
		web.Locale(cat, cfg.DefaultLang, cfg.TZ),
	)
	rctx, cancel := context.WithCancel(context.Background())
	s := &Server{Cfg: cfg, DB: d, Svc: svc, Web: app, OIDC: oidc, Sessions: sessions, Bus: svc.Bus, Handler: h, cancel: cancel}
	s.Jobs = &jobs.Runner{DB: d, Svc: svc, Clock: clock, TZ: cfg.TZ, BackupAuto: cfg.BackupAuto, BackupAt: cfg.BackupAt, BackupKeep: cfg.BackupKeep}
	s.Jobs.Start(rctx)
	oidc.Start(rctx)
	return s, nil
}

func prefixStrings(cfg *config.Config) []string {
	var out []string
	for _, p := range cfg.TrustedProxies {
		out = append(out, p.String())
	}
	return out
}

// HTTPServer returns an http.Server with the timeouts of 20 §4.
func (s *Server) HTTPServer() *http.Server {
	return &http.Server{Addr: s.Cfg.ListenAddr, Handler: s.Handler, ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 120 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 120 * time.Second}
}

// Close stops background work, drains the event bus, checkpoints the WAL and closes the DB.
func (s *Server) Close() error {
	s.cancel()
	s.Jobs.Wait()
	s.Bus.Close()
	return s.DB.Close()
}

// Shutdown gracefully stops srv within 8 s, then closes the application.
func (s *Server) Shutdown(srv *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	err := srv.Shutdown(ctx)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		slog.Warn("shutdown", "error", err)
	}
	return errors.Join(err, s.Close())
}
