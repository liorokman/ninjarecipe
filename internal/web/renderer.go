package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"sync"
	"time"

	"cooking/internal/domain"
	"cooking/internal/httpx"
	"cooking/internal/i18n"
	"cooking/internal/reqctx"
)

// RendererOpts configures the template renderer.
type RendererOpts struct {
	FS           fs.FS  // must contain templates/ (e.g. web.FS)
	Dir          string // APP_TEMPLATES_DIR: when set, templates are read from <Dir>/templates and reloaded on each render
	Catalog      *i18n.Catalog
	AssetVersion string
	BaseURL      string
	TZ           *time.Location
	DevMode      bool
	Clock        domain.Clock
	DefaultLang  string
	// InboxCount returns the pending inbox count of the signed-in user (nil = always 0).
	InboxCount func(r *http.Request, u *domain.User) int
}

// Renderer renders pages and fragments with typed view models.
type Renderer struct {
	o     RendererOpts
	mu    sync.Mutex
	cache map[string]*template.Template
	base  fs.FS
}

// NewRenderer builds a renderer.
func NewRenderer(o RendererOpts) *Renderer {
	if o.Clock == nil {
		o.Clock = domain.SystemClock{}
	}
	if o.TZ == nil {
		o.TZ = time.UTC
	}
	if o.AssetVersion == "" {
		o.AssetVersion = "dev"
	}
	rd := &Renderer{o: o, cache: map[string]*template.Template{}}
	rd.base = o.FS
	if o.Dir != "" {
		rd.base = os.DirFS(o.Dir)
	}
	return rd
}

// Catalog exposes the i18n catalog.
func (rd *Renderer) Catalog() *i18n.Catalog { return rd.o.Catalog }

// AssetVersion of static URLs.
func (rd *Renderer) AssetVersion() string { return rd.o.AssetVersion }

// TZ is the display time zone.
func (rd *Renderer) TZ() *time.Location { return rd.o.TZ }

// Now is the renderer clock time.
func (rd *Renderer) Now() time.Time { return rd.o.Clock.Now() }

// load parses layout + partials + page (page "" = fragments only: layout + partials).
func (rd *Renderer) load(page string) (*template.Template, error) {
	key := page
	if rd.o.Dir == "" {
		rd.mu.Lock()
		t, ok := rd.cache[key]
		rd.mu.Unlock()
		if ok {
			return t, nil
		}
	}
	// stub funcs only need the right names; real ones are bound per request via Clone+Funcs.
	t := template.New("layout.html").Funcs(Funcs(FuncCtx{Loc: rd.o.Catalog.Localizer("en", nil), AssetVersion: "x"}))
	read := func(p string) (string, error) {
		b, err := fs.ReadFile(rd.base, p)
		return string(b), err
	}
	src, err := read("templates/layout.html")
	if err != nil {
		return nil, fmt.Errorf("layout: %w", err)
	}
	if _, err := t.Parse(src); err != nil {
		return nil, fmt.Errorf("layout.html: %w", err)
	}
	parts, _ := fs.Glob(rd.base, "templates/partials/*.html")
	for _, p := range parts {
		s, err := read(p)
		if err != nil {
			return nil, err
		}
		if _, err := t.New(path.Base(p)).Parse(s); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
	}
	if page != "" {
		s, err := read("templates/pages/" + page)
		if err != nil {
			return nil, fmt.Errorf("page %s: %w", page, err)
		}
		if _, err := t.New(page).Parse(s); err != nil {
			return nil, fmt.Errorf("%s: %w", page, err)
		}
	}
	if rd.o.Dir == "" {
		rd.mu.Lock()
		rd.cache[key] = t
		rd.mu.Unlock()
	}
	return t, nil
}

func (rd *Renderer) bound(page string, r *http.Request) (*template.Template, *i18n.Loc, error) {
	t, err := rd.load(page)
	if err != nil {
		return nil, nil, err
	}
	loc := reqctx.Loc(r.Context())
	if loc == nil {
		loc = rd.o.Catalog.Localizer(i18n.ResolveLang("", r, rd.o.DefaultLang), rd.o.TZ)
	}
	c, err := t.Clone()
	if err != nil {
		return nil, nil, err
	}
	c.Funcs(Funcs(FuncCtx{Loc: loc, AssetVersion: rd.o.AssetVersion, Now: rd.Now()}))
	return c, loc, nil
}

// FillLayout completes the Layout of a view model (language, user, flash, client strings...).
func (rd *Renderer) FillLayout(w http.ResponseWriter, r *http.Request, loc *i18n.Loc, data any) {
	lh, ok := data.(layouter)
	if !ok {
		return
	}
	l := lh.layout()
	l.Lang, l.Dir = loc.Lang, loc.Dir()
	l.AssetVersion = rd.o.AssetVersion
	l.CSRFOrigin = rd.o.BaseURL
	l.DevMode = rd.o.DevMode
	if l.Path == "" {
		l.Path = r.URL.RequestURI()
	}
	if loc.Lang == "he" {
		l.OtherLang, l.OtherLangNative = "en", "English"
	} else {
		l.OtherLang, l.OtherLangNative = "he", "עברית"
	}
	if u := reqctx.User(r.Context()); u != nil && l.User == nil {
		l.User = &UserView{ID: u.ID, DisplayName: u.DisplayName, IsAdmin: u.IsAdmin}
		if rd.o.InboxCount != nil && l.InboxCount == 0 {
			l.InboxCount = rd.o.InboxCount(r, u)
		}
	}
	if l.Flash == nil && w != nil {
		l.Flash = rd.TakeFlash(w, r, loc)
	}
	l.ClientI18n = rd.o.Catalog.Client(loc.Lang, "js.", "oven.mode.")
}

// Page renders a full page: layout + partials + pages/<name>.
func (rd *Renderer) Page(w http.ResponseWriter, r *http.Request, status int, name string, data any) {
	t, loc, err := rd.bound(name, r)
	if err != nil {
		rd.fail(w, r, err)
		return
	}
	rd.FillLayout(w, r, loc, data)
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout.html", data); err != nil {
		rd.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// Fragment renders one named template (a partial or fragment) without the layout.
func (rd *Renderer) Fragment(w http.ResponseWriter, r *http.Request, status int, tmpl string, data any) {
	t, _, err := rd.bound("", r)
	if err != nil {
		rd.fail(w, r, err)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, tmpl, data); err != nil {
		rd.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func (rd *Renderer) fail(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("render failed", "error", err, "path", r.URL.Path, "request_id", httpx.RequestIDFromContext(r.Context()))
	msg := "internal error"
	if rd.o.DevMode {
		msg = "template error: " + err.Error()
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte(msg))
}

// ---- flash (D12) ----

const flashCookie = "nwr_flash"

type flashPayload struct {
	Kind   string         `json:"kind"`
	Key    string         `json:"key"`
	Params map[string]any `json:"params,omitempty"`
}

// SetFlash stores a localized-at-render flash message in a short-lived cookie.
func SetFlash(w http.ResponseWriter, kind, key string, kv ...any) {
	p := flashPayload{Kind: kind, Key: key}
	if len(kv) > 0 {
		p.Params = map[string]any{}
		for i := 0; i+1 < len(kv); i += 2 {
			p.Params[fmt.Sprint(kv[i])] = kv[i+1]
		}
	}
	b, _ := json.Marshal(p)
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: base64.RawURLEncoding.EncodeToString(b), Path: "/",
		MaxAge: 60, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

// TakeFlash reads and clears the flash cookie.
func (rd *Renderer) TakeFlash(w http.ResponseWriter, r *http.Request, loc *i18n.Loc) *FlashView {
	ck, err := r.Cookie(flashCookie)
	if err != nil {
		return nil
	}
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	raw, err := base64.RawURLEncoding.DecodeString(ck.Value)
	if err != nil {
		return nil
	}
	var p flashPayload
	if json.Unmarshal(raw, &p) != nil || len(p.Key) > 64 {
		return nil
	}
	switch p.Kind {
	case "success", "error", "warning":
	default:
		p.Kind = "success"
	}
	var kv []any
	for k, v := range p.Params {
		kv = append(kv, k, v)
	}
	return &FlashView{Kind: p.Kind, Text: loc.T(p.Key, kv...)}
}

// ---- errors (D16) ----

// ErrorStatus maps an error code to its HTTP status.
var ErrorStatus = map[string]int{
	"not_found": 404, "forbidden": 403, "bad_request": 400, "conflict": 409, "too_large": 413,
	"unsupported_media": 415, "internal": 500, "unavailable": 503, "not_allowed": 403,
	"signin_failed": 400, "signin_expired": 400, "busy": 503,
}

// Error renders error.html for a code (status derived from the code unless override > 0).
func (rd *Renderer) Error(w http.ResponseWriter, r *http.Request, code string, override int) {
	st := ErrorStatus[code]
	if st == 0 {
		st = 500
		code = "internal"
	}
	if override > 0 {
		st = override
	}
	// fetch/htmx callers get a bare status, not a page.
	if r.Header.Get("HX-Request") == "true" || r.Header.Get("Accept") == "application/json" {
		http.Error(w, http.StatusText(st), st)
		return
	}
	back := "/"
	v := &ErrorView{Layout: Layout{Title: ""}, Status: st, Code: code, RequestID: httpx.RequestIDFromContext(r.Context()), BackURL: back}
	rd.Page(w, r, st, "error.html", v)
}
