package web

import (
	"encoding/json"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"sort"
	"strings"
)

func init() {
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
	_ = mime.AddExtensionType(".js", "text/javascript; charset=utf-8")
	_ = mime.AddExtensionType(".css", "text/css; charset=utf-8")
	_ = mime.AddExtensionType(".svg", "image/svg+xml")
}

func (rd *Renderer) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	data, err := fs.ReadFile(rd.base, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ct := mime.TypeByExtension(path.Ext(name))
	if ct == "" {
		ct = http.DetectContentType(data)
	}
	w.Header().Set("Content-Type", ct)
	_, _ = w.Write(data)
}

// ServeStatic handles GET /static/{version}/{path...}. Any version segment is accepted (D9).
func (rd *Renderer) ServeStatic(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("path")
	if !fs.ValidPath(p) || p == "." || strings.HasPrefix(p, ".") {
		http.NotFound(w, r)
		return
	}
	if rd.o.DevMode {
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	rd.serveFile(w, r, "static/"+p)
}

// ServeManifest handles GET /manifest.webmanifest.
func (rd *Renderer) ServeManifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")
	rd.serveFile(w, r, "static/manifest.webmanifest")
}

// ServeIcon serves a PNG app icon at a stable root path.
func (rd *Renderer) ServeIcon(file string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		rd.serveFile(w, r, "static/icons/"+file)
	}
}

// PrecacheList lists the URLs the service worker should precache (D11).
func (rd *Renderer) PrecacheList() []string {
	var out []string
	_ = fs.WalkDir(rd.base, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(p, "static/")
		if rel == "sw.js" || strings.HasSuffix(rel, ".map") {
			return nil
		}
		out = append(out, "/static/"+rd.o.AssetVersion+"/"+rel)
		return nil
	})
	sort.Strings(out)
	return out
}

// ServeSW serves /sw.js with __ASSET_VERSION__ and __PRECACHE__ substituted.
func (rd *Renderer) ServeSW(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(rd.base, "static/sw.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	list, _ := json.Marshal(rd.PrecacheList())
	s := strings.ReplaceAll(string(data), "__PRECACHE__", string(list))
	s = strings.ReplaceAll(s, "__ASSET_VERSION__", rd.o.AssetVersion)
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(s))
}
