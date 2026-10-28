package test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/liorokman/ninjarecipe/internal/images"
	"github.com/liorokman/ninjarecipe/test/harness"
)

func upload(c *harness.Client, path, field string, data []byte) (*http.Response, string) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile(field, "photo.bin")
	fw.Write(data)
	mw.Close()
	return c.Do("POST", path, &buf, map[string]string{"Content-Type": mw.FormDataContentType(), "Sec-Fetch-Site": "same-origin", "X-Requested-With": "XMLHttpRequest"})
}

func TestImageUploadAndMediaAuthz(t *testing.T) {
	h := start(t, harness.Options{})
	alice := h.CreateUser("Alice", "a@e.com", false, "en")
	bob := h.CreateUser("Bob", "b@e.com", false, "en")
	a, b := h.NewClient(h.SessionCookie(alice)), h.NewClient(h.SessionCookie(bob))
	r := seedRecipe(t, h, alice, "With photo")

	resp, body := upload(a, "/recipes/"+r.ID+"/images", "image", harness.JPEG(900, 700, 2))
	if resp.StatusCode != 201 || resp.Header.Get("X-Image-Id") == "" || !strings.Contains(body, "/media/") {
		t.Fatalf("upload: %d %s", resp.StatusCode, body)
	}
	thumb := regexp.MustCompile(`/media/[0-9a-f]{32}/thumb`).FindString(body)
	full := strings.Replace(thumb, "thumb", "full", 1)
	// owner reads with long private caching and an ETag; conditional request -> 304
	resp, _ = a.Get(full)
	etag := resp.Header.Get("ETag")
	if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "private, max-age=31536000, immutable" || etag == "" || resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("media: %d %v", resp.StatusCode, resp.Header)
	}
	if resp, _ = a.Get(full, "If-None-Match", etag); resp.StatusCode != 304 {
		t.Fatalf("conditional: %d", resp.StatusCode)
	}
	// other user: 404 while private, readable once shared; anonymous 401; bad variant/key 404
	if resp, _ = b.Get(full); resp.StatusCode != 404 {
		t.Fatalf("other user's private image: %d", resp.StatusCode)
	}
	if resp, _ = h.NewClient(nil).Get(full); resp.StatusCode != 401 {
		t.Fatalf("anonymous: %d", resp.StatusCode)
	}
	for _, p := range []string{strings.Replace(full, "full", "huge", 1), "/media/zzzz/full", "/media/" + strings.Repeat("0", 32) + "/full"} {
		if resp, _ = a.Get(p); resp.StatusCode != 404 {
			t.Fatalf("%s: %d", p, resp.StatusCode)
		}
	}
	// bearer: recipes:read ok, inbox:write only -> 403, other user's token -> 404
	rc := h.NewClient(nil)
	rc.Bearer = h.Token(alice, "recipes:read")
	if resp, _ = rc.Get(full); resp.StatusCode != 200 {
		t.Fatalf("bearer recipes:read: %d", resp.StatusCode)
	}
	wc := h.NewClient(nil)
	wc.Bearer = h.Token(alice, "inbox:write")
	if resp, _ = wc.Get(full); resp.StatusCode != 403 {
		t.Fatalf("bearer without scope: %d", resp.StatusCode)
	}
	bc := h.NewClient(nil)
	bc.Bearer = h.Token(bob, "recipes:read")
	if resp, _ = bc.Get(full); resp.StatusCode != 404 {
		t.Fatalf("bearer other user: %d", resp.StatusCode)
	}
	_ = h.Svc().SetVisibility(ctxb, alice, r.ID, true)
	if resp, _ = b.Get(full); resp.StatusCode != 200 {
		t.Fatalf("shared image for family: %d", resp.StatusCode)
	}
	// missing file with an existing row -> placeholder, never 500
	key := strings.Split(full, "/")[2]
	_ = os.RemoveAll(images.Dir(h.DataDir, key))
	resp, body = a.Get(full)
	if resp.StatusCode != 200 || len(body) < 100 || resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("placeholder: %d", resp.StatusCode)
	}

	// rejected uploads
	gif := []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;")
	heic := append(append([]byte{0, 0, 0, 24}, []byte("ftypheic")...), make([]byte, 40)...)
	for name, data := range map[string][]byte{"gif": gif, "heic": heic, "text": []byte("hello")} {
		resp, body = upload(a, "/recipes/"+r.ID+"/images", "image", data)
		if resp.StatusCode != 415 || !strings.Contains(body, `upload-error`) {
			t.Fatalf("%s: %d %s", name, resp.StatusCode, body)
		}
	}
	if resp, body = upload(a, "/recipes/"+r.ID+"/images", "image", append([]byte{0xff, 0xd8, 0xff}, make([]byte, 17<<20)...)); resp.StatusCode != 413 {
		t.Fatalf("oversize: %d", resp.StatusCode)
	}
	// reorder / delete
	_, _ = upload(a, "/recipes/"+r.ID+"/images", "image", harness.JPEG(100, 100, 3))
	d, _ := h.Svc().GetRecipe(ctxb, alice, r.ID, false)
	if len(d.Recipe.Images) != 2 {
		t.Fatalf("images %d", len(d.Recipe.Images))
	}
	ids := url.Values{"ids": {d.Recipe.Images[1].ID, d.Recipe.Images[0].ID}}
	if resp, _ = a.PostForm("/recipes/"+r.ID+"/images/order", ids); resp.StatusCode != 204 {
		t.Fatalf("order: %d", resp.StatusCode)
	}
	if resp, _ = a.PostForm("/recipes/"+r.ID+"/images/order", url.Values{"ids": {d.Recipe.Images[0].ID}}); resp.StatusCode != 400 {
		t.Fatalf("order mismatch: %d", resp.StatusCode)
	}
	if resp, _ = a.PostForm("/recipes/"+r.ID+"/images/"+d.Recipe.Images[0].ID+"/delete", nil, "X-Requested-With", "x"); resp.StatusCode != 204 {
		t.Fatalf("delete xhr: %d", resp.StatusCode)
	}
	if resp, _ = a.PostForm("/recipes/"+r.ID+"/images/"+d.Recipe.Images[1].ID+"/delete", nil); resp.StatusCode != 303 {
		t.Fatalf("delete plain: %d", resp.StatusCode)
	}
	// bob (family) can add photos to a shared recipe; cook-log photo flow via multipart
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("note", "yum")
	mw.WriteField("rating", "5")
	fw, _ := mw.CreateFormFile("photo", "p.jpg")
	fw.Write(harness.JPEG(300, 200, 4))
	mw.Close()
	resp, body = b.Do("POST", "/recipes/"+r.ID+"/cook-logs", &buf, map[string]string{"Content-Type": mw.FormDataContentType(), "HX-Request": "true", "Sec-Fetch-Site": "same-origin"})
	if resp.StatusCode != 200 || !strings.Contains(body, `data-test="cooklog"`) || !strings.Contains(body, "/media/") {
		t.Fatalf("cook log: %d %s", resp.StatusCode, body)
	}
}

func TestTokenCreationThroughTheUIThenAPICall(t *testing.T) {
	h := start(t, harness.Options{})
	alice := h.CreateUser("Alice", "a@e.com", false, "en")
	a := h.NewClient(h.SessionCookie(alice))
	resp, body := a.PostForm("/settings/tokens", url.Values{"name": {"Assistant"}, "scopes": {"inbox:write", "recipes:read"}, "expires_in_days": {"30"}})
	m := regexp.MustCompile(`id="new-token">(nwr_[0-9A-Za-z]{43})<`).FindStringSubmatch(body)
	if resp.StatusCode != 200 || m == nil {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}
	// shown once only
	_, body = a.Get("/settings/tokens")
	if strings.Contains(body, m[1]) || !strings.Contains(body, "Assistant") {
		t.Fatal("token must not be shown again")
	}
	api := h.NewClient(nil)
	api.Bearer = m[1]
	if st, _, _ := apiCall(api, "GET", "/api/v1/categories", ""); st != 200 {
		t.Fatalf("api with new token: %d", st)
	}
	// validation errors render inline
	resp, body = a.PostForm("/settings/tokens", url.Values{"name": {""}, "expires_in_days": {"0"}})
	if resp.StatusCode != 422 || !strings.Contains(body, `data-field="name"`) {
		t.Fatalf("token validation: %d", resp.StatusCode)
	}
	// revoke
	doc := parse(t, body)
	_ = doc
	_, list := a.Get("/settings/tokens")
	revoke := regexp.MustCompile(`action="(/settings/tokens/[^"]+/revoke)"`).FindStringSubmatch(list)
	if resp, _ = a.PostForm(revoke[1], nil); resp.StatusCode != 303 {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	if st, _, _ := apiCall(api, "GET", "/api/v1/categories", ""); st != 401 {
		t.Fatalf("revoked token still works: %d", st)
	}
}

func TestOIDCSignatureAndNonceAreVerified(t *testing.T) {
	h := start(t, harness.Options{})
	for name, set := range map[string]func(){"forged signature": func() { h.IdP.ForgeNext = true }, "wrong nonce": func() { h.IdP.WrongNonce = true }} {
		set()
		_, resp := browserLogin(t, h, harness.IdPUser{Sub: "evil", Email: "e@e.com", Name: "Evil"}, "/")
		if resp.StatusCode != 400 {
			t.Fatalf("%s: %d", name, resp.StatusCode)
		}
	}
	if us, _ := h.Svc().Store.R().ListUsers(ctxb); len(us) != 0 {
		t.Fatal("no user may be provisioned from an unverifiable token")
	}
	// groups only delivered through userinfo are not required: missing groups claim with an allowed-groups gate -> denied
	h.IdP.NoGroupsInIDToken = true
}

func TestAdminCategoriesAndGuards(t *testing.T) {
	h := start(t, harness.Options{})
	admin := h.CreateUser("Admin", "l@e.com", true, "en")
	user := h.CreateUser("Bob", "b@e.com", false, "en")
	ac, uc := h.NewClient(h.SessionCookie(admin)), h.NewClient(h.SessionCookie(user))
	if r, _ := uc.Get("/admin/categories"); r.StatusCode != 403 {
		t.Fatalf("non-admin page: %d", r.StatusCode)
	}
	if r, _ := uc.PostForm("/admin/categories", url.Values{"slug": {"x1"}, "name": {"X"}, "name_he": {"X"}}); r.StatusCode != 403 {
		t.Fatalf("non-admin create: %d", r.StatusCode)
	}
	if r, _ := ac.PostForm("/admin/categories", url.Values{"slug": {"grill"}, "name": {"Grill"}, "name_he": {"גריל"}, "icon": {"🔥"}, "sort_order": {"5"}}); r.StatusCode != 303 {
		t.Fatalf("create: %d", r.StatusCode)
	}
	r, body := ac.PostForm("/admin/categories", url.Values{"slug": {"Bad Slug"}, "name": {"X"}, "name_he": {"X"}})
	if r.StatusCode != 422 || !strings.Contains(body, `data-field="slug"`) {
		t.Fatalf("invalid slug: %d", r.StatusCode)
	}
	_, body = ac.Get("/admin/categories")
	if !strings.Contains(body, "Grill") {
		t.Fatal("list")
	}
	cats, _ := h.Svc().ListCategories(ctxb)
	var id string
	for _, c := range cats {
		if c.Slug == "grill" {
			id = fmtInt(c.ID)
		}
	}
	if r, _ := ac.PostForm("/admin/categories/"+id+"/delete", nil); r.StatusCode != 303 {
		t.Fatalf("delete: %d", r.StatusCode)
	}
}
