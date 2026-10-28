// Package authz holds the pure authorization predicates of spec 20 §7.
package authz

import "github.com/liorokman/ninjarecipe/internal/domain"

func isOwner(u *domain.User, r *domain.Recipe) bool {
	return u != nil && r.OwnerID != "" && r.OwnerID == u.ID
}

func shared(r *domain.Recipe) bool {
	return r.Status == domain.StatusActive && r.Visibility == domain.VisShared
}

// IsOwner reports whether u owns r.
func IsOwner(u *domain.User, r *domain.Recipe) bool { return isOwner(u, r) }

// CanView: owner, or anyone for active shared recipes.
func CanView(u *domain.User, r *domain.Recipe) bool {
	if u == nil || r == nil {
		return false
	}
	return isOwner(u, r) || shared(r)
}

// CanEdit has the same shape as CanView (decision 1.1).
func CanEdit(u *domain.User, r *domain.Recipe) bool { return CanView(u, r) }

// CanDelete (= reject for inbox items).
func CanDelete(u *domain.User, r *domain.Recipe) bool { return CanView(u, r) }

// CanChangeVisibility: owner only, never for inbox items or ownerless recipes.
func CanChangeVisibility(u *domain.User, r *domain.Recipe) bool {
	return r != nil && r.Status == domain.StatusActive && isOwner(u, r)
}

// CanAcceptReject: owner of an inbox item.
func CanAcceptReject(u *domain.User, r *domain.Recipe) bool {
	return r != nil && r.Status == domain.StatusInbox && isOwner(u, r)
}

// CanCopy: any active recipe the actor can view.
func CanCopy(u *domain.User, r *domain.Recipe) bool {
	return r != nil && r.Status == domain.StatusActive && CanView(u, r)
}

// CanFavourite and CanLogCook: active recipes the actor can view.
func CanFavourite(u *domain.User, r *domain.Recipe) bool { return CanCopy(u, r) }
func CanLogCook(u *domain.User, r *domain.Recipe) bool   { return CanCopy(u, r) }

// CanDeleteCookLog: only the author of the entry.
func CanDeleteCookLog(u *domain.User, l *domain.CookLog) bool {
	return u != nil && l != nil && l.UserID == u.ID
}

// CanAdmin: category management.
func CanAdmin(u *domain.User) bool { return u != nil && u.IsAdmin }
