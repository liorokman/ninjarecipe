package web_test

import (
	"io/fs"
	"testing"

	"cooking/web"
)

func TestEmbeddedWebAssets(t *testing.T) {
	for _, f := range []string{"templates/layout.html", "static/vendor/htmx.min.js", "static/manifest.webmanifest", "static/sw.js", "static/icons/icon-192.png", "static/icons/icon-512.png"} {
		if _, err := fs.Stat(web.FS, f); err != nil {
			t.Errorf("missing embedded %s: %v", f, err)
		}
	}
	for _, p := range []string{"home", "recipes_list", "recipe_view", "cook", "recipe_edit", "inbox_list", "inbox_item", "settings", "tokens", "admin_categories", "signin", "logged_out", "error"} {
		if _, err := fs.Stat(web.FS, "templates/pages/"+p+".html"); err != nil {
			t.Errorf("missing page template %s: %v", p, err)
		}
	}
}
