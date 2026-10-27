// Package reqctx carries request-scoped values (user, token, localizer) between middleware and handlers.
package reqctx

import (
	"context"

	"cooking/internal/domain"
	"cooking/internal/i18n"
)

type key int

const (
	userKey key = iota
	tokenKey
	locKey
	sessionKey
)

func WithUser(ctx context.Context, u *domain.User) context.Context {
	return context.WithValue(ctx, userKey, u)
}
func User(ctx context.Context) *domain.User { u, _ := ctx.Value(userKey).(*domain.User); return u }

func WithToken(ctx context.Context, t *domain.APIToken) context.Context {
	return context.WithValue(ctx, tokenKey, t)
}
func Token(ctx context.Context) *domain.APIToken {
	t, _ := ctx.Value(tokenKey).(*domain.APIToken)
	return t
}

func WithLoc(ctx context.Context, l *i18n.Loc) context.Context {
	return context.WithValue(ctx, locKey, l)
}
func Loc(ctx context.Context) *i18n.Loc { l, _ := ctx.Value(locKey).(*i18n.Loc); return l }

func WithSession(ctx context.Context, s *domain.Session) context.Context {
	return context.WithValue(ctx, sessionKey, s)
}
func Session(ctx context.Context) *domain.Session {
	s, _ := ctx.Value(sessionKey).(*domain.Session)
	return s
}
