package service

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/liorokman/ninjarecipe/internal/domain"
)

func importOK(t *testing.T, e *env, u *domain.User, title, srcURL string) *ImportResult {
	t.Helper()
	res, err := e.svc.ImportToInbox(e.ctx, u, RecipeImport{Title: title, SourceURL: srcURL}, "", false)
	if err != nil {
		t.Fatalf("import %q: %v", srcURL, err)
	}
	return res
}

func wantPrev(t *testing.T, err error, outcome string, reason domain.RejectReason) domain.ErrPreviouslyReviewed {
	t.Helper()
	var prev domain.ErrPreviouslyReviewed
	if !errors.As(err, &prev) {
		t.Fatalf("want ErrPreviouslyReviewed, got %v", err)
	}
	if prev.Outcome != outcome || prev.Reason != reason || prev.DecidedAt.IsZero() {
		t.Fatalf("previously reviewed: %+v", prev)
	}
	return prev
}

func TestThrownAwaySourceIsNotOfferedAgain(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.user("alice", false), e.user("bob", false)

	res := importOK(t, e, alice, "Ribs", "https://www.example.com/ribs/?utm_source=feed")
	e.clock.Add(time.Hour)
	if _, err := e.svc.RejectInbox(e.ctx, alice, res.ID, domain.ReasonTooComplicated, "  two days of prep  "); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Store.R().GetRecipe(e.ctx, res.ID); !is(err, domain.ErrNotFound) {
		t.Fatalf("item must be hard-deleted: %v", err)
	}

	// the same page, written differently, is refused; allow_duplicate does not override
	for _, u := range []string{"https://www.example.com/ribs/?utm_source=feed", "http://example.com/ribs", "https://example.com/ribs?fbclid=zz#top"} {
		for _, allow := range []bool{false, true} {
			_, err := e.svc.ImportToInbox(e.ctx, alice, RecipeImport{Title: "Ribs again", SourceURL: u}, "", allow)
			p := wantPrev(t, err, domain.OutcomeRejected, domain.ReasonTooComplicated)
			if !p.DecidedAt.Equal(time.Date(2026, 10, 27, 13, 0, 0, 0, time.UTC)) {
				t.Fatalf("decided_at %v", p.DecidedAt)
			}
		}
	}
	// another page and another user are unaffected
	importOK(t, e, alice, "Other", "https://example.com/other")
	importOK(t, e, bob, "Ribs for bob", "https://example.com/ribs")
	// no URL: nothing to match
	importOK(t, e, alice, "No url", "")
	importOK(t, e, alice, "No url either", "")
}

func TestKeptSourceIsNotOfferedAgainEvenAfterDelete(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", false)

	res := importOK(t, e, alice, "Pizza", "https://example.com/pizza")
	if _, err := e.svc.AcceptInbox(e.ctx, alice, res.ID, true); err != nil {
		t.Fatal(err)
	}
	// while the recipe exists: rule 2 (duplicate), and allow_duplicate falls through to rule 3 (kept)
	_, err := e.svc.ImportToInbox(e.ctx, alice, RecipeImport{Title: "Pizza", SourceURL: "https://example.com/pizza/"}, "", false)
	var dup domain.ErrDuplicate
	if !errors.As(err, &dup) || dup.ExistingID != res.ID {
		t.Fatalf("duplicate: %v", err)
	}
	_, err = e.svc.ImportToInbox(e.ctx, alice, RecipeImport{Title: "Pizza", SourceURL: "https://example.com/pizza/"}, "", true)
	wantPrev(t, err, domain.OutcomeKept, "")

	// deleting the recipe afterwards leaves the decision behind
	if err := e.svc.DeleteRecipe(e.ctx, alice, res.ID); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.ImportToInbox(e.ctx, alice, RecipeImport{Title: "Pizza", SourceURL: "https://example.com/pizza"}, "", false)
	wantPrev(t, err, domain.OutcomeKept, "")
}

func TestEditedSourceURLStillBlocksTheOriginal(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", false)
	res := importOK(t, e, alice, "Lamb", "https://example.com/lamb?id=7")
	item, _, err := e.svc.GetInboxItem(e.ctx, alice, res.ID)
	if err != nil {
		t.Fatal(err)
	}
	in := basicInput("Lamb")
	in.SourceURL = "https://other.example/my-lamb" // the user corrected the source while keeping
	if _, _, err := e.svc.EditAndAcceptInbox(e.ctx, alice, res.ID, item.Version, in, false); err != nil {
		t.Fatal(err)
	}
	// the edited URL is blocked by the recipe itself, the submitted one by the decision
	_, err = e.svc.ImportToInbox(e.ctx, alice, RecipeImport{Title: "x", SourceURL: "https://other.example/my-lamb"}, "", false)
	if !errors.As(err, new(domain.ErrDuplicate)) {
		t.Fatalf("edited url: %v", err)
	}
	_, err = e.svc.ImportToInbox(e.ctx, alice, RecipeImport{Title: "x", SourceURL: "https://example.com/lamb?id=7"}, "", true)
	wantPrev(t, err, domain.OutcomeKept, "")
	// a different query is a different page
	importOK(t, e, alice, "Lamb 8", "https://example.com/lamb?id=8")
}

func TestOutcomesFeed(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.user("alice", false), e.user("bob", false)

	a := importOK(t, e, alice, "A", "https://example.com/a")
	b := importOK(t, e, alice, "B", "https://example.com/b")
	c := importOK(t, e, alice, "C", "https://example.com/c")
	d := importOK(t, e, alice, "D", "") // withdrawn: no outcome
	x := importOK(t, e, bob, "X", "https://example.com/x")

	e.clock.Add(time.Minute)
	if _, err := e.svc.RejectInbox(e.ctx, alice, a.ID, "bogus-reason", strings.Repeat("é", 300)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AcceptInbox(e.ctx, alice, b.ID, true); err != nil {
		t.Fatal(err)
	}
	item, _, _ := e.svc.GetInboxItem(e.ctx, alice, c.ID)
	if _, _, err := e.svc.EditAndAcceptInbox(e.ctx, alice, c.ID, item.Version, basicInput("C fixed"), false); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.WithdrawInbox(e.ctx, alice, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RejectInbox(e.ctx, bob, x.ID, domain.ReasonNotOurTaste, ""); err != nil {
		t.Fatal(err)
	}
	// alice cannot throw away bob's item (404) and that records nothing
	y := importOK(t, e, bob, "Y", "https://example.com/y")
	if _, err := e.svc.RejectInbox(e.ctx, alice, y.ID, domain.ReasonNotOurTaste, ""); !is(err, domain.ErrNotFound) {
		t.Fatalf("foreign reject: %v", err)
	}

	page, err := e.svc.ListOutcomes(e.ctx, alice, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("want 3 outcomes (withdraw records none, bob's are bob's), got %+v", page.Items)
	}
	o := page.Items
	if o[0].Outcome != domain.OutcomeRejected || o[0].Reason != "" || o[0].ItemID != a.ID || o[0].RecipeID != "" || len([]rune(o[0].Note)) != domain.MaxRejectNote || o[0].Title != "A" {
		t.Fatalf("rejected with unknown reason, long note: %+v", o[0])
	}
	if o[1].Outcome != domain.OutcomeKept || !o[1].Shared || o[1].Edited || o[1].RecipeID != b.ID || o[1].SourceURL != "https://example.com/b" {
		t.Fatalf("kept shared: %+v", o[1])
	}
	if o[2].Outcome != domain.OutcomeKept || !o[2].Edited || o[2].Shared || o[2].Title != "C fixed" || o[2].RecipeID != c.ID {
		t.Fatalf("kept edited: %+v", o[2])
	}

	// cursor paging
	p1, _ := e.svc.ListOutcomes(e.ctx, alice, "", 2)
	if len(p1.Items) != 2 || p1.NextCursor != strconv.FormatInt(p1.Items[1].ID, 10) {
		t.Fatalf("page 1: %+v", p1)
	}
	p2, _ := e.svc.ListOutcomes(e.ctx, alice, p1.NextCursor, 2)
	if len(p2.Items) != 1 || p2.Items[0].ID != o[2].ID {
		t.Fatalf("page 2: %+v", p2)
	}
	p3, _ := e.svc.ListOutcomes(e.ctx, alice, p2.NextCursor, 2)
	if len(p3.Items) != 0 || p3.NextCursor != p2.NextCursor {
		t.Fatalf("empty page must echo the cursor: %+v", p3)
	}
	if _, err := e.svc.ListOutcomes(e.ctx, alice, "nope", 2); !is(err, domain.ErrBadRequest) {
		t.Fatalf("bad cursor: %v", err)
	}

	// deleting a kept recipe clears recipe_id but keeps the row
	if err := e.svc.DeleteRecipe(e.ctx, alice, b.ID); err != nil {
		t.Fatal(err)
	}
	all, _ := e.svc.ListOutcomes(e.ctx, alice, "", 50)
	if len(all.Items) != 3 || all.Items[1].RecipeID != "" {
		t.Fatalf("after delete: %+v", all.Items)
	}
}

func TestAssistantBrief(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", false)
	b, err := e.svc.GetAssistantBrief(e.ctx, alice)
	if err != nil || b.Text != "" || b.UpdatedAt != nil || b.Language != "en" {
		t.Fatalf("empty brief: %+v %v", b, err)
	}
	e.svc.DefaultLang = "he"
	alice.UILanguage = ""
	if b, _ = e.svc.GetAssistantBrief(e.ctx, alice); b.Language != "he" {
		t.Fatalf("falls back to the default language: %q", b.Language)
	}
	alice.UILanguage = "en"
	if b, _ = e.svc.GetAssistantBrief(e.ctx, alice); b.Language != "en" {
		t.Fatalf("user language wins: %q", b.Language)
	}

	if err := e.svc.SetAssistantBrief(e.ctx, alice, "  More smoked fish.  "); err != nil {
		t.Fatal(err)
	}
	b, _ = e.svc.GetAssistantBrief(e.ctx, alice)
	if b.Text != "More smoked fish." || b.UpdatedAt == nil || !b.UpdatedAt.Equal(e.clock.Now()) {
		t.Fatalf("saved: %+v", b)
	}
	if err := e.svc.SetAssistantBrief(e.ctx, alice, strings.Repeat("x", domain.MaxAssistantBrief+1)); err == nil {
		t.Fatal("over-long brief must be rejected")
	} else {
		var ve *domain.ValidationError
		if !errors.As(err, &ve) || ve.Errors[0].Code != "too_long" {
			t.Fatalf("validation: %v", err)
		}
	}
	if b, _ = e.svc.GetAssistantBrief(e.ctx, alice); b.Text != "More smoked fish." {
		t.Fatal("a rejected brief must not change the stored one")
	}
	if err := e.svc.SetAssistantBrief(e.ctx, alice, strings.Repeat("é", domain.MaxAssistantBrief)); err != nil {
		t.Fatalf("exactly the limit in runes is fine: %v", err)
	}
	if err := e.svc.SetAssistantBrief(e.ctx, alice, "  "); err != nil {
		t.Fatal(err)
	}
	if b, _ = e.svc.GetAssistantBrief(e.ctx, alice); b.Text != "" || b.UpdatedAt != nil {
		t.Fatalf("cleared: %+v", b)
	}
	bob := e.user("bob", false)
	if b, _ = e.svc.GetAssistantBrief(e.ctx, bob); b.Text != "" {
		t.Fatal("briefs are per user")
	}
}

func TestSearchBySourceURLUsesSourceKey(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", false)
	in := basicInput("Ribs")
	in.SourceURL = "https://www.example.com/ribs/?utm_campaign=z"
	r := e.create(alice, in)
	for _, u := range []string{"http://example.com/ribs", "https://example.com/ribs?fbclid=1"} {
		page, err := e.svc.Search(e.ctx, alice, domain.SearchQuery{SourceURL: u, Sort: "updated", Limit: 10})
		if err != nil || len(page.Items) != 1 || page.Items[0].ID != r.ID {
			t.Fatalf("search %q: %v %+v", u, err, page.Items)
		}
	}
	for _, u := range []string{"https://example.com/other", "not a url"} {
		page, err := e.svc.Search(e.ctx, alice, domain.SearchQuery{SourceURL: u, Sort: "updated", Limit: 10})
		if err != nil || len(page.Items) != 0 {
			t.Fatalf("search %q must match nothing: %v %+v", u, err, page.Items)
		}
	}
	// editing the URL moves the key
	in.SourceURL = "https://example.com/ribs-v2"
	got, _ := e.svc.Store.R().GetRecipe(e.ctx, r.ID)
	if _, _, err := e.svc.UpdateRecipe(e.ctx, alice, r.ID, got.Version, in); err != nil {
		t.Fatal(err)
	}
	if page, _ := e.svc.Search(e.ctx, alice, domain.SearchQuery{SourceURL: "https://example.com/ribs", Sort: "updated", Limit: 10}); len(page.Items) != 0 {
		t.Fatal("old key must no longer match")
	}
	if page, _ := e.svc.Search(e.ctx, alice, domain.SearchQuery{SourceURL: "https://example.com/ribs-v2", Sort: "updated", Limit: 10}); len(page.Items) != 1 {
		t.Fatal("new key must match")
	}
}

func TestBackfillSourceKeys(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", false)
	r := e.create(alice, basicInput("Old"))
	// simulate a row that predates migration 0003
	if _, err := e.db.Writer.Exec(`UPDATE recipes SET source_url='https://WWW.Example.com/old/?utm_x=1', source_key=NULL WHERE id=?`, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Writer.Exec(`UPDATE recipes SET source_url='notaurl' WHERE id<>?`, r.ID); err != nil {
		t.Fatal(err)
	}
	n, err := e.svc.Store.W().BackfillSourceKeys(e.ctx)
	if err != nil || n != 1 {
		t.Fatalf("backfill: %d %v", n, err)
	}
	var key string
	if err := e.db.Reader.QueryRow(`SELECT source_key FROM recipes WHERE id=?`, r.ID).Scan(&key); err != nil || key != "example.com/old" {
		t.Fatalf("key %q %v", key, err)
	}
	if n, _ := e.svc.Store.W().BackfillSourceKeys(e.ctx); n != 0 {
		t.Fatalf("second run must be a no-op: %d", n)
	}
}
