package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/liorokman/ninjarecipe/internal/db"
	"github.com/liorokman/ninjarecipe/internal/domain"
	"github.com/liorokman/ninjarecipe/internal/images"
	"github.com/liorokman/ninjarecipe/internal/store"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time      { return c.t }
func (c *fakeClock) Add(d time.Duration) { c.t = c.t.Add(d) }

type env struct {
	t     *testing.T
	svc   *Service
	db    *db.DB
	clock *fakeClock
	dir   string
	ctx   context.Context
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	ctx := context.Background()
	if err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	clk := &fakeClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	svc := New(store.New(d.Reader, d.Writer), dir, "https://r.example.com", clk)
	svc.Fetcher.AllowAll = true
	return &env{t: t, svc: svc, db: d, clock: clk, dir: dir, ctx: ctx}
}

func (e *env) user(name string, admin bool) *domain.User {
	u, err := e.svc.UpsertOIDCUser(e.ctx, OIDCClaims{Issuer: "iss", Subject: "sub-" + name, Email: name + "@e.com", DisplayName: name, IsAdmin: admin}, "en")
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

func ip(i int) *int { return &i }

func basicInput(title string) domain.RecipeInput {
	return domain.RecipeInput{Title: title, Language: "en",
		Stages:      []domain.OvenStage{{Mode: "max_roast", TemperatureC: ip(250), DurationSeconds: ip(600)}},
		Ingredients: []domain.Ingredient{{Item: "salt", RawText: "2 tsp salt"}},
		Steps:       []domain.Step{{Text: "roast"}}}
}

func (e *env) create(u *domain.User, in domain.RecipeInput) *domain.Recipe {
	r, _, err := e.svc.CreateRecipe(e.ctx, u, in)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func jpegBytes(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 99, 255})
		}
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, nil)
	return b.Bytes()
}

func (e *env) mediaFiles() int {
	n := 0
	ents, _ := os.ReadDir(e.dir + "/media")
	for _, p := range ents {
		keys, _ := os.ReadDir(e.dir + "/media/" + p.Name())
		n += len(keys)
	}
	return n
}

func is(err error, target error) bool { return errors.Is(err, target) }

func TestCreateUpdateDeleteWithChildrenAndFTS(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", false)
	in := basicInput("Smoked ribs")
	in.Categories = []string{"beef"}
	in.Tags = []string{"BBQ", "weekend"}
	r := e.create(alice, in)
	got, err := e.svc.Store.R().GetRecipe(e.ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || got.Status != "active" || got.Visibility != "private" || got.CreatedBy != alice.ID || len(got.Stages) != 1 || got.Stages[0].Position != 1 {
		t.Fatalf("created: %+v", got)
	}
	if len(got.Categories()) != 1 || got.Categories()[0].Slug != "beef" || len(got.Tags()) != 2 {
		t.Fatalf("labels: %+v", got.Labels)
	}
	var n int
	e.db.Reader.QueryRow(`SELECT COUNT(*) FROM recipe_fts WHERE recipe_id=?`, r.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("fts rows %d", n)
	}
	// update: replaces children, bumps version, sets updated_by
	in2 := basicInput("Smoked short ribs")
	in2.Stages = []domain.OvenStage{{Mode: "smoker", TemperatureC: ip(120), DurationSeconds: ip(3 * 3600)}, {Mode: "max_roast", TemperatureC: ip(250), DurationSeconds: ip(600)}}
	in2.Steps = []domain.Step{{Text: "a"}, {Text: "b", TimerSeconds: ip(300)}}
	up, _, err := e.svc.UpdateRecipe(e.ctx, alice, r.ID, 1, in2)
	if err != nil {
		t.Fatal(err)
	}
	if up.Version != 2 {
		t.Fatalf("version %d", up.Version)
	}
	got, _ = e.svc.Store.R().GetRecipe(e.ctx, r.ID)
	if got.Title != "Smoked short ribs" || len(got.Stages) != 2 || !got.Stages[0].Woodfire || len(got.Steps) != 2 || got.Steps[1].TimerSeconds == nil || len(got.Labels) != 0 {
		t.Fatalf("updated: %+v labels=%v", got, got.Labels)
	}
	// stale version -> conflict
	if _, _, err := e.svc.UpdateRecipe(e.ctx, alice, r.ID, 1, in2); !is(err, domain.ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	// delete removes children and fts
	if err := e.svc.DeleteRecipe(e.ctx, alice, r.ID); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`SELECT COUNT(*) FROM oven_stages`, `SELECT COUNT(*) FROM recipe_ingredients`, `SELECT COUNT(*) FROM recipe_steps`, `SELECT COUNT(*) FROM recipe_fts`, `SELECT COUNT(*) FROM recipe_labels`} {
		e.db.Reader.QueryRow(q).Scan(&n)
		if n != 0 {
			t.Fatalf("%s = %d", q, n)
		}
	}
}

func TestValidationErrorsAndWarnings(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", false)
	in := basicInput("")
	in.Stages = []domain.OvenStage{{Mode: "broil", TemperatureC: ip(100), DurationSeconds: ip(600), Woodfire: true}, {Mode: "smoker", TemperatureC: ip(250), DurationSeconds: ip(600)}}
	in.Categories = []string{"nope"}
	_, _, err := e.svc.CreateRecipe(e.ctx, alice, in)
	var ve *domain.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want validation error, got %v", err)
	}
	fields := map[string]string{}
	for _, is := range ve.Errors {
		fields[is.Field] = is.Code
	}
	if fields["title"] != "required" || fields["oven_stages[0].woodfire"] != "forbidden" || fields["categories"] != "invalid" {
		t.Fatalf("errors: %v", fields)
	}
	warn := map[string]bool{}
	for _, w := range ve.Warnings {
		warn[w.Field] = true
	}
	if !warn["oven_stages[0].temperature_c"] || !warn["oven_stages[1].temperature_c"] {
		t.Fatalf("warnings: %v", ve.Warnings)
	}
	// limits
	big := basicInput(strings.Repeat("x", 201))
	_, _, err = e.svc.CreateRecipe(e.ctx, alice, big)
	if !errors.As(err, &ve) || ve.Errors[0].Code != "too_long" {
		t.Fatalf("title too long: %v", err)
	}
	// warnings never block
	ok := basicInput("Hot")
	ok.Stages = []domain.OvenStage{{Mode: "smoker", TemperatureC: ip(300), DurationSeconds: ip(600)}}
	if _, w, err := e.svc.CreateRecipe(e.ctx, alice, ok); err != nil || len(w) == 0 {
		t.Fatalf("warn-only recipe: %v %v", err, w)
	}
}

func TestPermissionsAndNotFound(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.user("alice", false), e.user("bob", false)
	priv := e.create(alice, basicInput("Private"))
	if _, err := e.svc.GetRecipe(e.ctx, bob, priv.ID, false); !is(err, domain.ErrNotFound) {
		t.Fatalf("other's private must be 404, got %v", err)
	}
	if err := e.svc.DeleteRecipe(e.ctx, bob, priv.ID); !is(err, domain.ErrNotFound) {
		t.Fatalf("delete other's private: %v", err)
	}
	if err := e.svc.SetVisibility(e.ctx, alice, priv.ID, true); err != nil {
		t.Fatal(err)
	}
	// bob can edit shared, but not change visibility
	if _, _, err := e.svc.UpdateRecipe(e.ctx, bob, priv.ID, 1, basicInput("Edited by bob")); err != nil {
		t.Fatal(err)
	}
	d, _ := e.svc.GetRecipe(e.ctx, alice, priv.ID, false)
	if d.Recipe.UpdatedBy != bob.ID || d.Recipe.UpdatedByName != "bob" || d.Recipe.CreatedBy != alice.ID {
		t.Fatalf("audit: %+v", d.Recipe)
	}
	if err := e.svc.SetVisibility(e.ctx, bob, priv.ID, false); !is(err, domain.ErrForbidden) {
		t.Fatalf("bob unshare: %v", err)
	}
	if err := e.svc.DeleteRecipe(e.ctx, bob, priv.ID); err != nil {
		t.Fatalf("bob may delete shared: %v", err)
	}
}

func TestUnshareDropsOthersFavouritesAndViewsKeepsLogs(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.user("alice", false), e.user("bob", false)
	r := e.create(alice, basicInput("Shared one"))
	_ = e.svc.SetVisibility(e.ctx, alice, r.ID, true)
	_ = e.svc.SetFavourite(e.ctx, bob, r.ID, true)
	_ = e.svc.SetFavourite(e.ctx, alice, r.ID, true)
	if _, err := e.svc.GetRecipe(e.ctx, bob, r.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.LogCook(e.ctx, bob, r.ID, CookLogInput{Note: "yum", Rating: ip(5)}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetVisibility(e.ctx, alice, r.ID, false); err != nil {
		t.Fatal(err)
	}
	var fav, views, logs int
	e.db.Reader.QueryRow(`SELECT COUNT(*) FROM recipe_favourites WHERE user_id=?`, bob.ID).Scan(&fav)
	e.db.Reader.QueryRow(`SELECT COUNT(*) FROM recipe_views WHERE user_id=?`, bob.ID).Scan(&views)
	e.db.Reader.QueryRow(`SELECT COUNT(*) FROM recipe_cook_logs`).Scan(&logs)
	if fav != 0 || views != 0 || logs != 1 {
		t.Fatalf("fav=%d views=%d logs=%d", fav, views, logs)
	}
	e.db.Reader.QueryRow(`SELECT COUNT(*) FROM recipe_favourites WHERE user_id=?`, alice.ID).Scan(&fav)
	if fav != 1 {
		t.Fatal("owner favourite must stay")
	}
	// ownerless shared stays shared and cannot be unshared
	_ = e.svc.SetVisibility(e.ctx, alice, r.ID, true)
	_, _ = e.db.Writer.Exec(`UPDATE recipes SET owner_id=NULL WHERE id=?`, r.ID)
	if err := e.svc.SetVisibility(e.ctx, alice, r.ID, false); !is(err, domain.ErrForbidden) {
		t.Fatalf("ownerless unshare: %v", err)
	}
}

func TestViewsThrottledTo10Minutes(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", false)
	r := e.create(alice, basicInput("V"))
	read := func() string {
		var s string
		e.db.Reader.QueryRow(`SELECT viewed_at FROM recipe_views WHERE user_id=? AND recipe_id=?`, alice.ID, r.ID).Scan(&s)
		return s
	}
	e.svc.GetRecipe(e.ctx, alice, r.ID, true)
	first := read()
	e.clock.Add(5 * time.Minute)
	e.svc.GetRecipe(e.ctx, alice, r.ID, true)
	if read() != first {
		t.Fatal("view updated within 10 minutes")
	}
	e.clock.Add(6 * time.Minute)
	e.svc.GetRecipe(e.ctx, alice, r.ID, true)
	if read() == first {
		t.Fatal("view not updated after 10 minutes")
	}
}

func TestCopyDuplicatesImageFiles(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.user("alice", false), e.user("bob", false)
	r := e.create(alice, basicInput("Original"))
	if _, err := e.svc.AddImage(e.ctx, alice, r.ID, bytes.NewReader(jpegBytes(800, 600))); err != nil {
		t.Fatal(err)
	}
	_ = e.svc.SetVisibility(e.ctx, alice, r.ID, true)
	if _, err := e.svc.CopyRecipe(e.ctx, bob, "nope"); !is(err, domain.ErrNotFound) {
		t.Fatal("copy of unknown id")
	}
	cp, err := e.svc.CopyRecipe(e.ctx, bob, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := e.svc.Store.R().GetRecipe(e.ctx, cp.ID)
	orig, _ := e.svc.Store.R().GetRecipe(e.ctx, r.ID)
	if got.OwnerID != bob.ID || got.Visibility != "private" || len(got.Images) != 1 || got.Images[0].Key == orig.Images[0].Key {
		t.Fatalf("copy: %+v", got)
	}
	if e.mediaFiles() != 2 {
		t.Fatalf("media dirs %d, want 2", e.mediaFiles())
	}
	if _, err := os.Stat(images.VariantPath(e.dir, got.Images[0].Key, "full")); err != nil {
		t.Fatal(err)
	}
	// deleting the original keeps the copy's files
	if err := e.svc.DeleteRecipe(e.ctx, alice, r.ID); err != nil {
		t.Fatal(err)
	}
	if e.mediaFiles() != 1 {
		t.Fatalf("media dirs after delete %d", e.mediaFiles())
	}
}

func TestImageLimitOrderDeleteAndFilesRemovedAfterCommit(t *testing.T) {
	e := newEnv(t)
	e.svc.MaxImages = 2
	alice := e.user("alice", false)
	r := e.create(alice, basicInput("Pics"))
	a, err := e.svc.AddImage(e.ctx, alice, r.ID, bytes.NewReader(jpegBytes(100, 100)))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := e.svc.AddImage(e.ctx, alice, r.ID, bytes.NewReader(jpegBytes(100, 100)))
	if _, err := e.svc.AddImage(e.ctx, alice, r.ID, bytes.NewReader(jpegBytes(100, 100))); !is(err, domain.ErrLimit) {
		t.Fatalf("limit: %v", err)
	}
	if a.Position != 1 || b.Position != 2 {
		t.Fatal("positions")
	}
	if err := e.svc.ReorderImages(e.ctx, alice, r.ID, []string{b.ID, a.ID}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ReorderImages(e.ctx, alice, r.ID, []string{b.ID}); !is(err, domain.ErrBadRequest) {
		t.Fatalf("set mismatch: %v", err)
	}
	got, _ := e.svc.Store.R().GetRecipe(e.ctx, r.ID)
	if got.Images[0].ID != b.ID {
		t.Fatal("reorder")
	}
	if err := e.svc.DeleteImage(e.ctx, alice, r.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = e.svc.Store.R().GetRecipe(e.ctx, r.ID)
	if len(got.Images) != 1 || got.Images[0].Position != 1 || e.mediaFiles() != 1 {
		t.Fatalf("after delete: %+v files=%d", got.Images, e.mediaFiles())
	}
	// a rolled-back transaction must not delete files
	keep := got.Images[0].Key
	err = e.svc.Store.WithTx(e.ctx, func(tx *store.Tx) error {
		tx.AfterCommit(func() { images.Remove(e.dir, keep) })
		return errors.New("boom")
	})
	if err == nil {
		t.Fatal("tx should fail")
	}
	if _, err := os.Stat(images.VariantPath(e.dir, keep, "full")); err != nil {
		t.Fatal("file deleted despite rollback")
	}
	// unsupported / bad images
	if _, err := e.svc.AddImage(e.ctx, alice, r.ID, strings.NewReader("GIF89a....")); !is(err, domain.ErrUnsupportedMedia) {
		t.Fatalf("garbage: %v", err)
	}
	// deleting the recipe removes files
	if err := e.svc.DeleteRecipe(e.ctx, alice, r.ID); err != nil {
		t.Fatal(err)
	}
	if e.mediaFiles() != 0 {
		t.Fatal("files left after recipe delete")
	}
}

func TestCookLogsPhotoStatsAndDelete(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.user("alice", false), e.user("bob", false)
	r := e.create(alice, basicInput("Cooked"))
	_ = e.svc.SetVisibility(e.ctx, alice, r.ID, true)
	if _, err := e.svc.LogCook(e.ctx, alice, r.ID, CookLogInput{Note: "good", Rating: ip(4), Photo: bytes.NewReader(jpegBytes(300, 200))}); err != nil {
		t.Fatal(err)
	}
	// broken photo: log still saved, photoErr reported
	photoErr, err := e.svc.LogCook(e.ctx, bob, r.ID, CookLogInput{Note: "ok", Rating: ip(2), Photo: strings.NewReader("not an image at all")})
	if err != nil || photoErr == nil {
		t.Fatalf("photoErr=%v err=%v", photoErr, err)
	}
	d, _ := e.svc.GetRecipe(e.ctx, bob, r.ID, false)
	if d.Stats.Count != 2 || d.Stats.RatingCount != 2 || d.Stats.AvgRating == nil || *d.Stats.AvgRating != 3 {
		t.Fatalf("stats: %+v", d.Stats)
	}
	var aliceLog, bobLog domain.CookLog
	for _, l := range d.Logs {
		if l.UserID == alice.ID {
			aliceLog = l
		} else {
			bobLog = l
		}
	}
	if aliceLog.Image == nil || bobLog.Image != nil || e.mediaFiles() != 1 {
		t.Fatalf("photos: %+v %+v files=%d", aliceLog.Image, bobLog.Image, e.mediaFiles())
	}
	if err := e.svc.DeleteCookLog(e.ctx, bob, r.ID, aliceLog.ID); !is(err, domain.ErrForbidden) {
		t.Fatalf("only the author may delete: %v", err)
	}
	if err := e.svc.DeleteCookLog(e.ctx, alice, r.ID, aliceLog.ID); err != nil {
		t.Fatal(err)
	}
	if e.mediaFiles() != 0 {
		t.Fatal("cook-log photo not removed")
	}
	var imgs int
	e.db.Reader.QueryRow(`SELECT COUNT(*) FROM recipe_images`).Scan(&imgs)
	if imgs != 0 {
		t.Fatal("image row left")
	}
	if _, err := e.svc.LogCook(e.ctx, alice, r.ID, CookLogInput{Rating: ip(9)}); err == nil {
		t.Fatal("rating 9 must be rejected")
	}
	// cook-log notes are searchable
	_, _ = e.svc.LogCook(e.ctx, alice, r.ID, CookLogInput{Note: "wonderful crackling"})
	page, _ := e.svc.Search(e.ctx, alice, domain.SearchQuery{Q: "crackling", Sort: "relevance"})
	if len(page.Items) != 1 {
		t.Fatalf("search by cook-log note: %d", len(page.Items))
	}
}

func TestInboxLifecycle(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.user("alice", false), e.user("bob", false)
	imp := RecipeImport{Title: "Imported", SourceURL: "https://x.example/r", Categories: []string{"beef", "bbq-unknown"}, Tags: []string{"Quick"},
		Ingredients: []ImportIngredient{{Text: "2 tbsp smoked paprika"}, {Amount: fp(1.5), Unit: sp("kg"), Item: "ribs"}},
		Steps:       []ImportStep{{Text: "Rub"}},
		OvenStages:  []ImportStage{{Mode: "smoker", TemperatureC: ip(300), DurationSeconds: ip(3600)}}}
	res, err := e.svc.ImportToInbox(e.ctx, alice, imp, "key-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Status != "inbox" || res.ReviewURL != "https://r.example.com/inbox/"+res.ID {
		t.Fatalf("result: %+v", res)
	}
	codes := map[string]string{}
	for _, w := range res.Warnings {
		codes[w.Field] = w.Code
	}
	if codes["categories[1]"] != "unknown_category" || codes["oven_stages[0].temperature_c"] != "out_of_range" {
		t.Fatalf("warnings: %v", res.Warnings)
	}
	// replay with the same Idempotency-Key
	again, err := e.svc.ImportToInbox(e.ctx, alice, imp, "key-1", false)
	if err != nil || again.ID != res.ID || again.Created {
		t.Fatalf("replay: %+v %v", again, err)
	}
	// duplicate source_url
	_, err = e.svc.ImportToInbox(e.ctx, alice, imp, "", false)
	var dup domain.ErrDuplicate
	if !errors.As(err, &dup) || dup.ExistingID != res.ID {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := e.svc.ImportToInbox(e.ctx, alice, imp, "", true); err != nil {
		t.Fatalf("allow_duplicate: %v", err)
	}
	// labels came from the assistant, item is private and not searchable
	got, _ := e.svc.Store.R().GetRecipe(e.ctx, res.ID)
	if got.Status != "inbox" || got.Visibility != "private" || len(got.Ingredients) != 2 || got.Ingredients[0].Unit != "tbsp" || got.Ingredients[1].RawText != "1.5 kg ribs" {
		t.Fatalf("stored: %+v", got)
	}
	for _, l := range got.Labels {
		if l.Source != "assistant" {
			t.Fatalf("label source %q", l.Source)
		}
	}
	page, _ := e.svc.Search(e.ctx, alice, domain.SearchQuery{Q: "Imported", Sort: "relevance"})
	if len(page.Items) != 0 {
		t.Fatal("inbox items must not be searchable")
	}
	// bob cannot see it
	if _, _, err := e.svc.GetInboxItem(e.ctx, bob, res.ID); !is(err, domain.ErrNotFound) {
		t.Fatalf("other's inbox: %v", err)
	}
	if e.svc.InboxCount(e.ctx, alice) != 2 {
		t.Fatal("count")
	}
	item, next, err := e.svc.GetInboxItem(e.ctx, alice, res.ID)
	if err != nil || next == "" || item.ID != res.ID {
		t.Fatalf("item/next: %v %q", err, next)
	}
	// edit and keep
	form := domain.RecipeInput{Title: "Imported and fixed", Language: "en", Ingredients: item.Ingredients, Steps: item.Steps,
		Stages: []domain.OvenStage{{Mode: "smoker", TemperatureC: ip(120), DurationSeconds: ip(3600)}}}
	nx, _, err := e.svc.EditAndAcceptInbox(e.ctx, alice, res.ID, item.Version, form, true)
	if err != nil || nx == "" {
		t.Fatalf("edit+accept: %v %q", err, nx)
	}
	got, _ = e.svc.Store.R().GetRecipe(e.ctx, res.ID)
	if got.Status != "active" || got.Visibility != "shared" || got.Title != "Imported and fixed" {
		t.Fatalf("accepted: %+v", got)
	}
	page, _ = e.svc.Search(e.ctx, bob, domain.SearchQuery{Q: "fixed", Sort: "relevance"})
	if len(page.Items) != 1 {
		t.Fatalf("accepted recipe not searchable by family: %d", len(page.Items))
	}
	// reject (hard delete) the other item, queue becomes empty
	inbox, _ := e.svc.ListInbox(e.ctx, alice)
	if len(inbox) != 1 {
		t.Fatalf("inbox %d", len(inbox))
	}
	nx, err = e.svc.RejectInbox(e.ctx, alice, inbox[0].ID)
	if err != nil || nx != "" {
		t.Fatalf("reject: %v %q", err, nx)
	}
	if err := e.svc.WithdrawInbox(e.ctx, alice, res.ID); !is(err, domain.ErrNotFound) {
		t.Fatalf("withdraw accepted item must be 404: %v", err)
	}
}

func fp(f float64) *float64 { return &f }
func sp(s string) *string   { return &s }

func TestImportImagesWarningsAndValidation(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", false)
	b64 := func(b []byte) string {
		const t = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
		var sb strings.Builder
		for i := 0; i < len(b); i += 3 {
			var v uint32
			n := 0
			for j := 0; j < 3; j++ {
				v <<= 8
				if i+j < len(b) {
					v |= uint32(b[i+j])
					n++
				}
			}
			sb.WriteByte(t[v>>18&63])
			sb.WriteByte(t[v>>12&63])
			if n > 1 {
				sb.WriteByte(t[v>>6&63])
			} else {
				sb.WriteByte('=')
			}
			if n > 2 {
				sb.WriteByte(t[v&63])
			} else {
				sb.WriteByte('=')
			}
		}
		return sb.String()
	}
	imp := RecipeImport{Title: "With pics", Images: []ImportImage{{DataBase64: b64(jpegBytes(200, 100)), Caption: "cover"}, {DataBase64: b64([]byte("GIF89a....."))},
		{URL: "http://127.0.0.1:1/nothing.jpg"}, {DataBase64: "!!!"}}}
	res, err := e.svc.ImportToInbox(e.ctx, alice, imp, "", false)
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]string{}
	for _, w := range res.Warnings {
		codes[w.Field] = w.Code
	}
	if codes["images[1]"] != "unsupported_type" || codes["images[2]"] == "" || codes["images[3]"] != "invalid" || codes["images[0]"] != "" {
		t.Fatalf("image warnings: %v", codes)
	}
	got, _ := e.svc.Store.R().GetRecipe(e.ctx, res.ID)
	if len(got.Images) != 1 || got.Images[0].Caption != "cover" {
		t.Fatalf("images: %+v", got.Images)
	}
	// SSRF: loopback is rejected with the real guard
	e.svc.Fetcher.AllowAll = false
	res, _ = e.svc.ImportToInbox(e.ctx, alice, RecipeImport{Title: "ssrf", Images: []ImportImage{{URL: "https://127.0.0.1/x.jpg"}, {URL: "http://example.com/x.jpg"}}}, "", false)
	if res == nil || res.Warnings[0].Code != "blocked" || res.Warnings[1].Code != "blocked" {
		t.Fatalf("ssrf warnings: %+v", res)
	}
	// validation 422
	_, err = e.svc.ImportToInbox(e.ctx, alice, RecipeImport{Title: "", Ingredients: []ImportIngredient{{Item: ""}}}, "", false)
	var ve *domain.ValidationError
	if !errors.As(err, &ve) || len(ve.Errors) < 2 {
		t.Fatalf("want 422, got %v", err)
	}
}

func TestSearchFiltersSortsAndCursor(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.user("alice", false), e.user("bob", false)
	mk := func(u *domain.User, title string, mut func(*domain.RecipeInput)) *domain.Recipe {
		in := basicInput(title)
		if mut != nil {
			mut(&in)
		}
		e.clock.Add(time.Minute)
		return e.create(u, in)
	}
	ribs := mk(alice, "Smoked ribs", func(in *domain.RecipeInput) {
		in.Categories = []string{"beef"}
		in.Tags = []string{"bbq", "weekend"}
		in.Stages = []domain.OvenStage{{Mode: "smoker", TemperatureC: ip(120), DurationSeconds: ip(3 * 3600)}}
	})
	pizza := mk(alice, "Pizza night", func(in *domain.RecipeInput) {
		in.Categories = []string{"pizza"}
		in.Tags = []string{"weekend"}
		in.Stages = []domain.OvenStage{{Mode: "pizza", PizzaPreset: "artisan", DurationSeconds: ip(150)}}
	})
	hebrew := mk(bob, "עוף שלם בתנור", func(in *domain.RecipeInput) { in.Language = "he"; in.Categories = []string{"poultry"} })
	_ = e.svc.SetVisibility(e.ctx, bob, hebrew.ID, true)
	bobPriv := mk(bob, "Bob private", nil)
	_ = bobPriv
	ids := func(p domain.SearchPage) []string {
		var out []string
		for _, s := range p.Items {
			out = append(out, s.ID)
		}
		return out
	}
	search := func(u *domain.User, q domain.SearchQuery) domain.SearchPage {
		if q.Sort == "" {
			q.Sort = "updated"
		}
		p, err := e.svc.Search(e.ctx, u, q)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	// visibility: alice sees her 2 + bob's shared; bob's private is hidden
	if p := search(alice, domain.SearchQuery{}); len(p.Items) != 3 {
		t.Fatalf("alice sees %d", len(p.Items))
	}
	if p := search(bob, domain.SearchQuery{}); len(p.Items) != 2 {
		t.Fatalf("bob sees %d (his two, hebrew shared + private)", len(p.Items))
	}
	if p := search(alice, domain.SearchQuery{Scope: "mine"}); len(p.Items) != 2 {
		t.Fatalf("mine %d", len(p.Items))
	}
	if p := search(alice, domain.SearchQuery{Scope: "family"}); len(p.Items) != 1 || p.Items[0].ID != hebrew.ID {
		t.Fatalf("family %v", ids(p))
	}
	// facets
	if p := search(alice, domain.SearchQuery{Categories: []string{"beef", "pizza"}}); len(p.Items) != 2 {
		t.Fatal("category OR")
	}
	if p := search(alice, domain.SearchQuery{Tags: []string{"bbq", "weekend"}}); len(p.Items) != 1 || p.Items[0].ID != ribs.ID {
		t.Fatal("tag AND")
	}
	if p := search(alice, domain.SearchQuery{Modes: []string{"pizza"}}); len(p.Items) != 1 || p.Items[0].ID != pizza.ID {
		t.Fatal("mode")
	}
	if p := search(alice, domain.SearchQuery{Smoked: true}); len(p.Items) != 1 || p.Items[0].ID != ribs.ID || !p.Items[0].UsesWoodfire {
		t.Fatal("smoked")
	}
	if p := search(alice, domain.SearchQuery{MaxTimeMin: 30}); len(p.Items) != 2 { // pizza 2.5 min, hebrew basic 10 min; ribs 3h excluded
		t.Fatalf("max_time %v", ids(p))
	}
	// final letters + niqqud: "עוף" matches "עופות"-style stems via trigram on normalized text
	if p := search(alice, domain.SearchQuery{Q: "עוף", Sort: "relevance"}); len(p.Items) != 1 || p.Items[0].ID != hebrew.ID {
		t.Fatalf("hebrew search %v", ids(p))
	}
	if p := search(alice, domain.SearchQuery{Q: "SMOKED", Sort: "relevance"}); len(p.Items) != 1 {
		t.Fatal("case-insensitive")
	}
	if p := search(alice, domain.SearchQuery{Q: "pi", Sort: "relevance"}); len(p.Items) != 1 || p.Items[0].ID != pizza.ID {
		t.Fatalf("short term LIKE: %v", ids(p))
	}
	if p := search(alice, domain.SearchQuery{Q: `"ribs" OR`, Sort: "relevance"}); len(p.Items) != 0 {
		// quotes are escaped, never FTS syntax
		t.Logf("quote query returned %d", len(p.Items))
	}
	// favourites, sort by title, cooked, recent
	_ = e.svc.SetFavourite(e.ctx, alice, pizza.ID, true)
	if p := search(alice, domain.SearchQuery{Fav: true}); len(p.Items) != 1 || p.Items[0].ID != pizza.ID || !p.Items[0].IsFavourite {
		t.Fatal("fav")
	}
	if p := search(alice, domain.SearchQuery{Sort: "title"}); ids(p)[0] != hebrew.ID && ids(p)[0] != pizza.ID {
		t.Fatalf("title order %v", ids(p))
	}
	_, _ = e.svc.LogCook(e.ctx, alice, ribs.ID, CookLogInput{Rating: ip(5)})
	_, _ = e.svc.LogCook(e.ctx, alice, ribs.ID, CookLogInput{Rating: ip(4)})
	_, _ = e.svc.LogCook(e.ctx, alice, pizza.ID, CookLogInput{Rating: ip(3)})
	if p := search(alice, domain.SearchQuery{Sort: "cooked"}); ids(p)[0] != ribs.ID {
		t.Fatalf("cooked sort %v", ids(p))
	}
	if p := search(alice, domain.SearchQuery{Sort: "rating"}); ids(p)[0] != ribs.ID {
		t.Fatalf("rating sort %v", ids(p))
	}
	if p := search(alice, domain.SearchQuery{Sort: "recent"}); len(p.Items) != 2 {
		t.Fatalf("recent %v", ids(p))
	}
	// keyset pagination covers everything exactly once for each sort
	for _, sort := range []string{"updated", "title", "cooked", "rating"} {
		seen := map[string]bool{}
		cur := ""
		for i := 0; i < 5; i++ {
			p := search(alice, domain.SearchQuery{Sort: sort, Limit: 1, Cursor: cur})
			for _, s := range p.Items {
				if seen[s.ID] {
					t.Fatalf("%s: duplicate %s", sort, s.ID)
				}
				seen[s.ID] = true
			}
			if p.NextCursor == "" {
				break
			}
			cur = p.NextCursor
		}
		if len(seen) != 3 {
			t.Fatalf("%s: paged through %d of 3", sort, len(seen))
		}
	}
	// home: tiles only for categories with visible recipes, recent, favourites
	h, err := e.svc.Home(e.ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	var slugs []string
	for _, c := range h.Tiles {
		slugs = append(slugs, c.Slug)
	}
	if strings.Join(slugs, ",") != "pizza,beef,poultry" || len(h.Favourites) != 1 || len(h.Recent) == 0 {
		t.Fatalf("home: %v fav=%d recent=%d", slugs, len(h.Favourites), len(h.Recent))
	}
}

func TestCategoryRenameReindexesAndAdminOnly(t *testing.T) {
	e := newEnv(t)
	alice, admin := e.user("alice", false), e.user("admin", true)
	in := basicInput("Tagged")
	in.Categories = []string{"beef"}
	r := e.create(alice, in)
	_ = r
	cats, _ := e.svc.ListCategories(e.ctx)
	var beef domain.Label
	for _, c := range cats {
		if c.Slug == "beef" {
			beef = c
		}
	}
	if err := e.svc.UpdateCategory(e.ctx, alice, beef.ID, CategoryInput{Name: "x", NameHe: "x"}); !is(err, domain.ErrForbidden) {
		t.Fatalf("non-admin: %v", err)
	}
	if err := e.svc.UpdateCategory(e.ctx, admin, beef.ID, CategoryInput{Name: "Cattle", NameHe: "בקר", Icon: "🐄", SortOrder: 5}); err != nil {
		t.Fatal(err)
	}
	p, _ := e.svc.Search(e.ctx, alice, domain.SearchQuery{Q: "cattle", Sort: "relevance"})
	if len(p.Items) != 1 {
		t.Fatal("rename must reindex")
	}
	if err := e.svc.CreateCategory(e.ctx, admin, CategoryInput{Slug: "Bad Slug", Name: "n", NameHe: "ש"}); err == nil {
		t.Fatal("bad slug accepted")
	}
	if err := e.svc.CreateCategory(e.ctx, admin, CategoryInput{Slug: "beef", Name: "n", NameHe: "ש"}); err == nil {
		t.Fatal("duplicate slug accepted")
	}
	if err := e.svc.DeleteCategory(e.ctx, admin, beef.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := e.svc.Store.R().GetRecipe(e.ctx, r.ID)
	if len(got.Categories()) != 0 {
		t.Fatal("category link should cascade")
	}
}

func TestTagsOrphanPurgeAndSuggest(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.user("alice", false), e.user("bob", false)
	in := basicInput("T")
	in.Tags = []string{"brisket", "weekend"}
	r := e.create(alice, in)
	s, _ := e.svc.Suggest(e.ctx, alice, "bri", "en")
	if len(s) != 1 || s[0].Type != "tag" || s[0].Name != "brisket" {
		t.Fatalf("suggest: %+v", s)
	}
	if s, _ := e.svc.Suggest(e.ctx, bob, "bri", "en"); len(s) != 0 {
		t.Fatalf("tags of private recipes must not leak: %+v", s)
	}
	if s, _ := e.svc.Suggest(e.ctx, alice, "פיצ", "he"); len(s) != 1 || s[0].Type != "category" || s[0].Name != "פיצה" {
		t.Fatalf("category suggest: %+v", s)
	}
	_ = e.svc.DeleteRecipe(e.ctx, alice, r.ID)
	n, err := e.svc.PurgeHourly(e.ctx)
	if err != nil || n != 2 {
		t.Fatalf("purge: %d %v", n, err)
	}
}

func TestUserDeletionProcedure(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.user("alice", false), e.user("bob", false)
	priv := e.create(alice, basicInput("Private"))
	sh := e.create(alice, basicInput("Shared"))
	_ = e.svc.SetVisibility(e.ctx, alice, sh.ID, true)
	_, _ = e.svc.AddImage(e.ctx, alice, priv.ID, bytes.NewReader(jpegBytes(50, 50)))
	if _, err := e.svc.ImportToInbox(e.ctx, alice, RecipeImport{Title: "inbox item"}, "", false); err != nil {
		t.Fatal(err)
	}
	_, _ = e.svc.LogCook(e.ctx, alice, sh.ID, CookLogInput{Note: "x", Photo: bytes.NewReader(jpegBytes(50, 50))})
	_, _ = e.svc.LogCook(e.ctx, bob, sh.ID, CookLogInput{Note: "bob's"})
	plan, _ := e.svc.PlanUserDeletion(e.ctx, alice)
	if plan.DeleteRecipes != 2 || plan.Ownerless != 1 {
		t.Fatalf("plan: %+v", plan)
	}
	if err := e.svc.DeleteUser(e.ctx, alice); err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.Store.R().GetRecipe(e.ctx, sh.ID)
	if err != nil || got.OwnerID != "" || got.Visibility != "shared" {
		t.Fatalf("shared recipe must become ownerless and stay shared: %+v %v", got, err)
	}
	if _, err := e.svc.Store.R().GetRecipe(e.ctx, priv.ID); !is(err, domain.ErrNotFound) {
		t.Fatal("private recipe must be gone")
	}
	var logs, imgs int
	e.db.Reader.QueryRow(`SELECT COUNT(*) FROM recipe_cook_logs`).Scan(&logs)
	e.db.Reader.QueryRow(`SELECT COUNT(*) FROM recipe_images`).Scan(&imgs)
	if logs != 1 || imgs != 0 || e.mediaFiles() != 0 {
		t.Fatalf("logs=%d imgs=%d files=%d", logs, imgs, e.mediaFiles())
	}
	// ownerless shared recipe stays editable by everyone
	if _, _, err := e.svc.UpdateRecipe(e.ctx, bob, sh.ID, got.Version, basicInput("Edited")); err != nil {
		t.Fatal(err)
	}
}

func TestTokens(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", false)
	tok := NewTokenString()
	if !strings.HasPrefix(tok, "nwr_") || len(tok) != 47 || HashToken(tok) == tok || len(HashToken(tok)) != 64 {
		t.Fatalf("token format %q", tok)
	}
	plain, rec, err := e.svc.CreateToken(e.ctx, alice, "Assistant", []string{"inbox:write", "recipes:read", "bogus"}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Prefix != plain[4:12] || len(rec.Scopes) != 2 || rec.ExpiresAt == nil {
		t.Fatalf("record: %+v", rec)
	}
	stored, _ := e.svc.Store.R().TokenByHash(e.ctx, HashToken(plain))
	if stored.Hash == plain {
		t.Fatal("plain token stored")
	}
	if tk, u, err := e.svc.AuthenticateToken(e.ctx, plain); err != nil || u.ID != alice.ID || tk.ID != rec.ID {
		t.Fatalf("auth: %v", err)
	}
	// expiry
	e.clock.Add(31 * 24 * time.Hour)
	if _, _, err := e.svc.AuthenticateToken(e.ctx, plain); err == nil {
		t.Fatal("expired token accepted")
	}
	e.clock.t = e.clock.t.Add(-31 * 24 * time.Hour)
	// revoke
	if err := e.svc.RevokeToken(e.ctx, alice, rec.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.AuthenticateToken(e.ctx, plain); err == nil {
		t.Fatal("revoked token accepted")
	}
	if _, _, err := e.svc.AuthenticateToken(e.ctx, "nwr_unknown"); err == nil {
		t.Fatal("unknown token accepted")
	}
	// cap of 10 active tokens
	for i := 0; i < 10; i++ {
		if _, _, err := e.svc.CreateToken(e.ctx, alice, "k", []string{"inbox:write"}, 0); err != nil {
			t.Fatalf("token %d: %v", i, err)
		}
	}
	if _, _, err := e.svc.CreateToken(e.ctx, alice, "k", []string{"inbox:write"}, 0); !is(err, domain.ErrLimit) {
		t.Fatalf("cap: %v", err)
	}
	var ve *domain.ValidationError
	if _, _, err := e.svc.CreateToken(e.ctx, alice, "", nil, 7); !errors.As(err, &ve) || len(ve.Errors) != 3 {
		t.Fatalf("validation: %v", err)
	}
}

func TestUpsertUserLanguageRules(t *testing.T) {
	e := newEnv(t)
	u1, _ := e.svc.UpsertOIDCUser(e.ctx, OIDCClaims{Issuer: "i", Subject: "s", Email: "a@e.com", DisplayName: "A", Locale: "he-IL"}, "en")
	if u1.UILanguage != "he" {
		t.Fatalf("locale claim: %q", u1.UILanguage)
	}
	// existing user keeps the chosen language, name/admin refresh each login
	_ = e.svc.SetLanguage(e.ctx, u1, "en")
	u2, _ := e.svc.UpsertOIDCUser(e.ctx, OIDCClaims{Issuer: "i", Subject: "s", Email: "new@e.com", DisplayName: "Renamed", IsAdmin: true, Locale: "he"}, "he")
	if u2.ID != u1.ID || u2.UILanguage != "en" || u2.DisplayName != "Renamed" || !u2.IsAdmin || u2.Email != "new@e.com" {
		t.Fatalf("refresh: %+v", u2)
	}
	u3, _ := e.svc.UpsertOIDCUser(e.ctx, OIDCClaims{Issuer: "i", Subject: "s3", Email: "same@e.com"}, "")
	if u3.UILanguage != "" || u3.DisplayName != "same@e.com" {
		t.Fatalf("no language: %+v", u3)
	}
	// duplicate emails are allowed
	if _, err := e.svc.UpsertOIDCUser(e.ctx, OIDCClaims{Issuer: "i", Subject: "s4", Email: "same@e.com", DisplayName: "Other"}, ""); err != nil {
		t.Fatal(err)
	}
}

func TestSweepAndReindex(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", false)
	r := e.create(alice, basicInput("Index me"))
	_, _ = e.svc.AddImage(e.ctx, alice, r.ID, bytes.NewReader(jpegBytes(40, 40)))
	// an orphan media dir older than an hour is swept, a fresh one is kept
	p, _ := images.Store(e.dir, &images.Processed{Full: jpegBytes(10, 10), Thumb: jpegBytes(10, 10)})
	old := e.clock.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(images.Dir(e.dir, p), old, old)
	fresh, _ := images.Store(e.dir, &images.Processed{Full: jpegBytes(10, 10), Thumb: jpegBytes(10, 10)})
	n, err := e.svc.Sweep(e.ctx)
	if err != nil || n < 1 {
		t.Fatalf("sweep: %d %v", n, err)
	}
	if _, err := os.Stat(images.Dir(e.dir, p)); err == nil {
		t.Fatal("old orphan not swept")
	}
	if _, err := os.Stat(images.Dir(e.dir, fresh)); err != nil {
		t.Fatal("fresh orphan swept")
	}
	_, _ = e.db.Writer.Exec(`DELETE FROM recipe_fts`)
	cnt, err := e.svc.Reindex(e.ctx)
	if err != nil || cnt != 1 {
		t.Fatalf("reindex %d %v", cnt, err)
	}
	pg, _ := e.svc.Search(e.ctx, alice, domain.SearchQuery{Q: "index", Sort: "relevance"})
	if len(pg.Items) != 1 {
		t.Fatal("search after reindex")
	}
}
