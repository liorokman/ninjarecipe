package store

import (
	"context"
	"database/sql"
	"time"

	"cooking/internal/domain"
)

const userCols = `id, oidc_issuer, oidc_subject, email, display_name, is_admin, COALESCE(ui_language,''), last_login_at, created_at, updated_at`

func scanUser(sc interface{ Scan(...any) error }) (*domain.User, error) {
	var u domain.User
	var admin int
	var ll sql.NullString
	var ca, ua string
	if err := sc.Scan(&u.ID, &u.OIDCIssuer, &u.OIDCSubject, &u.Email, &u.DisplayName, &admin, &u.UILanguage, &ll, &ca, &ua); err != nil {
		return nil, err
	}
	u.IsAdmin = admin == 1
	u.LastLoginAt = parseNullTime(ll)
	u.CreatedAt, u.UpdatedAt = ParseTime(ca), ParseTime(ua)
	return &u, nil
}

// GetUser loads a user by id.
func (q Q) GetUser(ctx context.Context, id string) (*domain.User, error) {
	u, err := scanUser(q.q.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id=?`, id))
	return u, notFound(err)
}

// FindUser finds a user by id or (case-insensitive) email for the CLI.
func (q Q) FindUser(ctx context.Context, idOrEmail string) (*domain.User, error) {
	u, err := scanUser(q.q.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id=? OR lower(email)=lower(?) ORDER BY created_at LIMIT 1`, idOrEmail, idOrEmail))
	return u, notFound(err)
}

// ListUsers lists all users.
func (q Q) ListUsers(ctx context.Context) ([]*domain.User, error) {
	rows, err := q.q.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpsertUser creates or refreshes a user keyed by (issuer, subject). id is used only for new users.
// ui_language is only set when currently NULL.
func (q Q) UpsertUser(ctx context.Context, id string, u domain.User, now time.Time) (*domain.User, error) {
	var existing string
	err := q.q.QueryRowContext(ctx, `SELECT id FROM users WHERE oidc_issuer=? AND oidc_subject=?`, u.OIDCIssuer, u.OIDCSubject).Scan(&existing)
	ts := FormatTime(now)
	switch {
	case err == sql.ErrNoRows:
		if _, err := q.q.ExecContext(ctx, `INSERT INTO users (id, oidc_issuer, oidc_subject, email, display_name, is_admin, ui_language, last_login_at, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?)`, id, u.OIDCIssuer, u.OIDCSubject, u.Email, u.DisplayName, boolInt(u.IsAdmin), nullStr(u.UILanguage), ts, ts, ts); err != nil {
			return nil, err
		}
		existing = id
	case err != nil:
		return nil, err
	default:
		if _, err := q.q.ExecContext(ctx, `UPDATE users SET email=?, display_name=?, is_admin=?, last_login_at=?, updated_at=?,
			ui_language=COALESCE(ui_language, ?) WHERE id=?`, u.Email, u.DisplayName, boolInt(u.IsAdmin), ts, ts, nullStr(u.UILanguage), existing); err != nil {
			return nil, err
		}
	}
	return q.GetUser(ctx, existing)
}

// SetUILanguage stores the chosen language.
func (q Q) SetUILanguage(ctx context.Context, id, lang string, now time.Time) error {
	_, err := q.q.ExecContext(ctx, `UPDATE users SET ui_language=?, updated_at=? WHERE id=?`, lang, FormatTime(now), id)
	return err
}

// DeleteUserRow removes the user row (cascades sessions, tokens, logs, favourites, views).
func (q Q) DeleteUserRow(ctx context.Context, id string) error {
	_, err := q.q.ExecContext(ctx, `DELETE FROM users WHERE id=?`, id)
	return err
}

// ---- sessions ----

// CreateSession inserts a session.
func (q Q) CreateSession(ctx context.Context, s domain.Session) error {
	_, err := q.q.ExecContext(ctx, `INSERT INTO sessions (id_hash, user_id, id_token, user_agent, ip_address, created_at, last_seen_at, expires_at)
		VALUES (?,?,?,?,?,?,?,?)`, s.IDHash, s.UserID, nullStr(s.IDToken), nullStr(s.UserAgent), nullStr(s.IP),
		FormatTime(s.CreatedAt), FormatTime(s.LastSeenAt), FormatTime(s.ExpiresAt))
	return err
}

// GetSession returns the session and its user for an id hash (expiry is checked by the caller).
func (q Q) GetSession(ctx context.Context, idHash string) (*domain.Session, *domain.User, error) {
	var s domain.Session
	var tok, ua, ip sql.NullString
	var ca, ls, ex string
	err := q.q.QueryRowContext(ctx, `SELECT id_hash, user_id, id_token, user_agent, ip_address, created_at, last_seen_at, expires_at FROM sessions WHERE id_hash=?`, idHash).
		Scan(&s.IDHash, &s.UserID, &tok, &ua, &ip, &ca, &ls, &ex)
	if err != nil {
		return nil, nil, notFound(err)
	}
	s.IDToken, s.UserAgent, s.IP = tok.String, ua.String, ip.String
	s.CreatedAt, s.LastSeenAt, s.ExpiresAt = ParseTime(ca), ParseTime(ls), ParseTime(ex)
	u, err := q.GetUser(ctx, s.UserID)
	if err != nil {
		return nil, nil, err
	}
	return &s, u, nil
}

// TouchSession updates last_seen_at and expires_at.
func (q Q) TouchSession(ctx context.Context, idHash string, seen, expires time.Time) error {
	_, err := q.q.ExecContext(ctx, `UPDATE sessions SET last_seen_at=?, expires_at=? WHERE id_hash=?`, FormatTime(seen), FormatTime(expires), idHash)
	return err
}

// DeleteSession removes one session.
func (q Q) DeleteSession(ctx context.Context, idHash string) error {
	_, err := q.q.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash=?`, idHash)
	return err
}

// DeleteUserSessions removes all sessions of a user and returns how many.
func (q Q) DeleteUserSessions(ctx context.Context, userID string) (int64, error) {
	r, err := q.q.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, userID)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

// PurgeExpired deletes expired sessions and login states.
func (q Q) PurgeExpired(ctx context.Context, now time.Time) (int64, error) {
	var total int64
	for _, stmt := range []string{`DELETE FROM sessions WHERE expires_at < ?`, `DELETE FROM oidc_login_states WHERE expires_at < ?`} {
		r, err := q.q.ExecContext(ctx, stmt, FormatTime(now))
		if err != nil {
			return total, err
		}
		n, _ := r.RowsAffected()
		total += n
	}
	return total, nil
}

// ---- OIDC login states ----

// PutLoginState stores a login state.
func (q Q) PutLoginState(ctx context.Context, s domain.OIDCState) error {
	_, err := q.q.ExecContext(ctx, `INSERT INTO oidc_login_states (state, verifier, nonce, return_to, expires_at) VALUES (?,?,?,?,?)`,
		s.State, s.Verifier, s.Nonce, s.ReturnTo, FormatTime(s.ExpiresAt))
	return err
}

// TakeLoginState returns and deletes a state (single use).
func (q Q) TakeLoginState(ctx context.Context, state string) (*domain.OIDCState, error) {
	var s domain.OIDCState
	var ex string
	err := q.q.QueryRowContext(ctx, `DELETE FROM oidc_login_states WHERE state=? RETURNING state, verifier, nonce, return_to, expires_at`, state).
		Scan(&s.State, &s.Verifier, &s.Nonce, &s.ReturnTo, &ex)
	if err != nil {
		return nil, notFound(err)
	}
	s.ExpiresAt = ParseTime(ex)
	return &s, nil
}

// ---- API tokens ----

const tokCols = `id, user_id, name, token_hash, token_prefix, scopes, last_used_at, expires_at, revoked_at, created_at`

func scanToken(sc interface{ Scan(...any) error }) (*domain.APIToken, error) {
	var t domain.APIToken
	var scopes, ca string
	var lu, ex, rv sql.NullString
	if err := sc.Scan(&t.ID, &t.UserID, &t.Name, &t.Hash, &t.Prefix, &scopes, &lu, &ex, &rv, &ca); err != nil {
		return nil, err
	}
	for _, s := range splitFields(scopes) {
		t.Scopes = append(t.Scopes, s)
	}
	t.LastUsedAt, t.ExpiresAt, t.RevokedAt = parseNullTime(lu), parseNullTime(ex), parseNullTime(rv)
	t.CreatedAt = ParseTime(ca)
	return &t, nil
}

func splitFields(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ' ' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// CreateToken inserts a token.
func (q Q) CreateToken(ctx context.Context, t domain.APIToken) error {
	scopes := ""
	for i, s := range t.Scopes {
		if i > 0 {
			scopes += " "
		}
		scopes += s
	}
	_, err := q.q.ExecContext(ctx, `INSERT INTO api_tokens (id, user_id, name, token_hash, token_prefix, scopes, expires_at, created_at) VALUES (?,?,?,?,?,?,?,?)`,
		t.ID, t.UserID, t.Name, t.Hash, t.Prefix, scopes, nullTime(t.ExpiresAt), FormatTime(t.CreatedAt))
	return err
}

// TokenByHash looks up a token by its SHA-256 hex.
func (q Q) TokenByHash(ctx context.Context, hash string) (*domain.APIToken, error) {
	t, err := scanToken(q.q.QueryRowContext(ctx, `SELECT `+tokCols+` FROM api_tokens WHERE token_hash=?`, hash))
	return t, notFound(err)
}

// ListTokens lists the tokens of a user, newest first.
func (q Q) ListTokens(ctx context.Context, userID string) ([]domain.APIToken, error) {
	rows, err := q.q.QueryContext(ctx, `SELECT `+tokCols+` FROM api_tokens WHERE user_id=? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.APIToken
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// CountActiveTokens counts non-revoked, non-expired tokens.
func (q Q) CountActiveTokens(ctx context.Context, userID string, now time.Time) (int, error) {
	var n int
	err := q.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE user_id=? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`, userID, FormatTime(now)).Scan(&n)
	return n, err
}

// RevokeToken revokes a token owned by userID.
func (q Q) RevokeToken(ctx context.Context, id, userID string, now time.Time) error {
	r, err := q.q.ExecContext(ctx, `UPDATE api_tokens SET revoked_at=? WHERE id=? AND user_id=? AND revoked_at IS NULL`, FormatTime(now), id, userID)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// TouchToken sets last_used_at if it is older than 1 minute.
func (q Q) TouchToken(ctx context.Context, id string, now time.Time) error {
	_, err := q.q.ExecContext(ctx, `UPDATE api_tokens SET last_used_at=? WHERE id=? AND (last_used_at IS NULL OR last_used_at < ?)`,
		FormatTime(now), id, FormatTime(now.Add(-time.Minute)))
	return err
}
