package domain

import (
	"testing"
	"time"
)

func TestHelpers(t *testing.T) {
	if !Contains(OvenModes, "broil") || Contains(OvenModes, "top_heat") || len(OvenModes) != 8 || len(PizzaPresets) != 6 || len(Accessories) != 4 {
		t.Fatal("enums")
	}
	now := time.Now()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	tk := APIToken{Scopes: []string{"inbox:write"}}
	if tk.Status(now) != "active" || !tk.HasScope("inbox:write") || tk.HasScope("recipes:read") {
		t.Fatal("token")
	}
	tk.ExpiresAt = &past
	if tk.Status(now) != "expired" {
		t.Fatal("expired")
	}
	tk.ExpiresAt = &future
	tk.RevokedAt = &past
	if tk.Status(now) != "revoked" {
		t.Fatal("revoked wins")
	}
	l := Label{Type: "category", Name: "Beef", NameHe: "בקר"}
	if l.LocalName("he") != "בקר" || l.LocalName("en") != "Beef" || (Label{Type: "tag", Name: "bbq", NameHe: "x"}).LocalName("he") != "bbq" {
		t.Fatal("LocalName")
	}
	r := &Recipe{Labels: []Label{{Type: "category", Slug: "a"}, {Type: "tag", Slug: "b"}}}
	if len(r.Categories()) != 1 || len(r.Tags()) != 1 {
		t.Fatal("label split")
	}
	if (SystemClock{}).Now().Location() != time.UTC {
		t.Fatal("clock")
	}
	if (&ValidationError{Errors: []Issue{{}}}).Error() == "" || (ErrDuplicate{ExistingID: "x"}).Error() == "" {
		t.Fatal("errors")
	}
}
