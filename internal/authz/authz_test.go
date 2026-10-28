package authz

import (
	"testing"

	"github.com/liorokman/ninjarecipe/internal/domain"
)

func TestPredicateMatrix(t *testing.T) {
	owner := &domain.User{ID: "o"}
	other := &domain.User{ID: "x"}
	admin := &domain.User{ID: "a", IsAdmin: true}
	inbox := &domain.Recipe{OwnerID: "o", Status: "inbox", Visibility: "private"}
	priv := &domain.Recipe{OwnerID: "o", Status: "active", Visibility: "private"}
	shared := &domain.Recipe{OwnerID: "o", Status: "active", Visibility: "shared"}
	ownerless := &domain.Recipe{OwnerID: "", Status: "active", Visibility: "shared"}

	type want struct{ view, edit, del, vis, acc, cp, fav, log bool }
	cases := []struct {
		name string
		u    *domain.User
		r    *domain.Recipe
		w    want
	}{
		{"owner/inbox", owner, inbox, want{true, true, true, false, true, false, false, false}},
		{"other/inbox", other, inbox, want{}},
		{"admin/inbox", admin, inbox, want{}},
		{"owner/private", owner, priv, want{true, true, true, true, false, true, true, true}},
		{"other/private", other, priv, want{}},
		{"admin/private", admin, priv, want{}},
		{"owner/shared", owner, shared, want{true, true, true, true, false, true, true, true}},
		{"other/shared", other, shared, want{true, true, true, false, false, true, true, true}},
		{"admin/shared", admin, shared, want{true, true, true, false, false, true, true, true}},
		{"anyone/ownerless", other, ownerless, want{true, true, true, false, false, true, true, true}},
		{"admin/ownerless", admin, ownerless, want{true, true, true, false, false, true, true, true}},
		{"anonymous/shared", nil, shared, want{}},
	}
	for _, c := range cases {
		got := want{CanView(c.u, c.r), CanEdit(c.u, c.r), CanDelete(c.u, c.r), CanChangeVisibility(c.u, c.r), CanAcceptReject(c.u, c.r),
			CanCopy(c.u, c.r), CanFavourite(c.u, c.r), CanLogCook(c.u, c.r)}
		if got != c.w {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.w)
		}
	}
	// ownerless can never change visibility, even for the admin
	if CanChangeVisibility(admin, ownerless) {
		t.Fatal("ownerless visibility")
	}
	if !IsOwner(owner, shared) || IsOwner(other, shared) || IsOwner(owner, ownerless) {
		t.Fatal("IsOwner")
	}
}

func TestCookLogAndAdmin(t *testing.T) {
	l := &domain.CookLog{UserID: "u1"}
	if !CanDeleteCookLog(&domain.User{ID: "u1"}, l) || CanDeleteCookLog(&domain.User{ID: "u2", IsAdmin: true}, l) || CanDeleteCookLog(nil, l) {
		t.Fatal("only the author may delete a cook log")
	}
	if !CanAdmin(&domain.User{IsAdmin: true}) || CanAdmin(&domain.User{}) || CanAdmin(nil) {
		t.Fatal("admin")
	}
}
