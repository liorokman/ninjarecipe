// Package auth implements the OIDC client, server-side sessions and bearer-token authentication (20 §5-6).
package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"cooking/internal/config"
)

// ErrUnavailable means OIDC discovery has not succeeded yet.
var ErrUnavailable = errors.New("oidc provider unavailable")

// Identity is the verified result of a login.
type Identity struct {
	Issuer, Subject, Email, Name, PreferredUsername, Locale string
	Groups                                                  []string
	RawIDToken                                              string
}

// DisplayName: name, else preferred_username, else email.
func (i Identity) DisplayName() string {
	for _, s := range []string{i.Name, i.PreferredUsername, i.Email} {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// OIDC wraps go-oidc with lazy, retried discovery.
type OIDC struct {
	cfg *config.Config

	mu         sync.Mutex
	provider   *oidc.Provider
	oauth      *oauth2.Config
	verifier   *oidc.IDTokenVerifier
	endSession string
	lastTry    time.Time
	backoff    time.Duration
}

// NewOIDC creates the client; discovery happens lazily (and in Start's background loop).
func NewOIDC(cfg *config.Config) *OIDC { return &OIDC{cfg: cfg, backoff: time.Second} }

// Start retries discovery in the background until it succeeds or ctx ends.
func (o *OIDC) Start(ctx context.Context) {
	go func() {
		for ctx.Err() == nil {
			if err := o.ensure(ctx, true); err == nil {
				return
			}
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
	}()
}

// Status is "ok" or "pending" (readyz).
func (o *OIDC) Status() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.provider != nil {
		return "ok"
	}
	return "pending"
}

func (o *OIDC) ensure(ctx context.Context, force bool) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.provider != nil {
		return nil
	}
	if !force && time.Since(o.lastTry) < o.backoff {
		return ErrUnavailable
	}
	o.lastTry = time.Now()
	dctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	p, err := oidc.NewProvider(dctx, o.cfg.OIDCIssuer)
	if err != nil {
		o.backoff = min(o.backoff*2, 30*time.Second)
		slog.Warn("oidc discovery failed", "error", err, "retry_in", o.backoff)
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	var meta struct {
		EndSession string `json:"end_session_endpoint"`
	}
	_ = p.Claims(&meta)
	o.provider = p
	o.endSession = meta.EndSession
	o.verifier = p.Verifier(&oidc.Config{ClientID: o.cfg.OIDCClientID})
	o.oauth = &oauth2.Config{ClientID: o.cfg.OIDCClientID, ClientSecret: o.cfg.OIDCClientSecret, Endpoint: p.Endpoint(),
		RedirectURL: o.cfg.BaseURL + "/auth/callback", Scopes: o.cfg.OIDCScopes}
	o.backoff = time.Second
	slog.Info("oidc discovery ok", "issuer", o.cfg.OIDCIssuer)
	return nil
}

// AuthURL builds the authorization redirect with PKCE S256 and nonce.
func (o *OIDC) AuthURL(ctx context.Context, state, nonce, verifier string) (string, error) {
	if err := o.ensure(ctx, false); err != nil {
		return "", err
	}
	return o.oauth.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce)), nil
}

// Exchange trades the code, verifies the ID token (signature, iss, aud, exp, nonce) and maps claims.
func (o *OIDC) Exchange(ctx context.Context, code, verifier, nonce string) (*Identity, error) {
	if err := o.ensure(ctx, false); err != nil {
		return nil, err
	}
	tok, err := o.oauth.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("code exchange: %w", err)
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return nil, errors.New("no id_token in token response")
	}
	idt, err := o.verifier.Verify(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("id token: %w", err)
	}
	if idt.Nonce != nonce {
		return nil, errors.New("nonce mismatch")
	}
	var claims map[string]any
	if err := idt.Claims(&claims); err != nil {
		return nil, err
	}
	if _, ok := claims[o.cfg.OIDCGroupsClaim]; !ok {
		// some providers only return groups from userinfo
		if ui, err := o.provider.UserInfo(ctx, oauth2.StaticTokenSource(tok)); err == nil {
			var uc map[string]any
			if ui.Claims(&uc) == nil {
				for k, v := range uc {
					if _, have := claims[k]; !have {
						claims[k] = v
					}
				}
			}
		}
	}
	str := func(k string) string { s, _ := claims[k].(string); return s }
	id := &Identity{Issuer: idt.Issuer, Subject: idt.Subject, Email: str("email"), Name: str("name"),
		PreferredUsername: str("preferred_username"), Locale: str("locale"), RawIDToken: raw}
	if gs, ok := claims[o.cfg.OIDCGroupsClaim].([]any); ok {
		for _, g := range gs {
			if s, ok := g.(string); ok {
				id.Groups = append(id.Groups, s)
			}
		}
	}
	return id, nil
}

// EndSessionURL returns the RP-initiated logout URL ("" when the IdP has no end_session_endpoint).
func (o *OIDC) EndSessionURL(idToken string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.endSession == "" {
		return ""
	}
	v := "post_logout_redirect_uri=" + urlEscape(o.cfg.BaseURL+"/auth/logged-out")
	if idToken != "" {
		v += "&id_token_hint=" + urlEscape(idToken)
	}
	sep := "?"
	if strings.Contains(o.endSession, "?") {
		sep = "&"
	}
	return o.endSession + sep + v
}

// Gate applies the group gate (allowed = any overlap, or no restriction) and admin mapping.
func Gate(cfg *config.Config, groups []string) (allowed, admin bool) {
	has := func(g string) bool {
		for _, x := range groups {
			if x == g {
				return true
			}
		}
		return false
	}
	allowed = len(cfg.OIDCAllowedGroups) == 0
	for _, g := range cfg.OIDCAllowedGroups {
		if has(g) {
			allowed = true
		}
	}
	admin = cfg.OIDCAdminGroup != "" && has(cfg.OIDCAdminGroup)
	return
}
