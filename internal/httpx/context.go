package httpx

import (
	"context"
)

type contextKey string

const (
	requestIDKey contextKey = "httpx_request_id"
	clientIPKey  contextKey = "httpx_client_ip"
)

// WithRequestID returns a new context with the request ID.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}

// RequestIDFromContext extracts the request ID from context.
func RequestIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok && id != "" {
		return id
	}
	return ""
}

// WithClientIP returns a new context with the client IP.
func WithClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, clientIPKey, ip)
}

// ClientIPFromContext extracts the client IP from context.
func ClientIPFromContext(ctx context.Context) string {
	if ip, ok := ctx.Value(clientIPKey).(string); ok && ip != "" {
		return ip
	}
	return ""
}

type userSlotKey struct{}

// WithUserSlot installs a slot the auth layer fills with the user id so the access log can read it.
func WithUserSlot(ctx context.Context) (context.Context, *string) {
	s := new(string)
	return context.WithValue(ctx, userSlotKey{}, s), s
}

// SetUserID records the authenticated user id for the access log.
func SetUserID(ctx context.Context, id string) {
	if s, ok := ctx.Value(userSlotKey{}).(*string); ok {
		*s = id
	}
}
