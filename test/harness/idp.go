// Package harness runs the real application in-process against a fake OIDC provider and a real SQLite file.
package harness

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"
)

// IdPUser is what the fake IdP puts into the ID token.
type IdPUser struct {
	Sub, Email, Name string
	Groups           []string
	Locale           string
}

// FakeIdP implements discovery, JWKS, authorize (auto-approve), token (PKCE + secret checked) and end-session.
type FakeIdP struct {
	Server   *httptest.Server
	Issuer   string
	ClientID string
	Secret   string

	mu                sync.Mutex
	key               *rsa.PrivateKey
	Next              *IdPUser // user for the next authorization (consumed)
	Deny              bool     // answer the next authorization with error=access_denied
	codes             map[string]codeInfo
	LoggedOut         []url.Values
	NoGroupsInIDToken bool
	ForgeNext         bool // sign the next ID token with an unknown key
	WrongNonce        bool // put a wrong nonce into the next ID token
}

type codeInfo struct {
	user      IdPUser
	challenge string
	nonce     string
	redirect  string
}

// NewFakeIdP starts the provider on 127.0.0.1.
func NewFakeIdP(clientID, secret string) (*FakeIdP, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &FakeIdP{ClientID: clientID, Secret: secret, key: key, codes: map[string]codeInfo{}}
	mux := http.NewServeMux()
	p.Server = &httptest.Server{Listener: ln, Config: &http.Server{Handler: mux}}
	p.Server.Start()
	p.Issuer = p.Server.URL + "/"
	mux.HandleFunc("GET /.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("GET /jwks", p.jwks)
	mux.HandleFunc("GET /authorize", p.authorize)
	mux.HandleFunc("POST /token", p.token)
	mux.HandleFunc("GET /end-session", p.endSession)
	mux.HandleFunc("GET /control/next", p.controlNext)
	return p, nil
}

// Close stops the provider.
func (p *FakeIdP) Close() { p.Server.Close() }

func (p *FakeIdP) discovery(w http.ResponseWriter, r *http.Request) {
	b := p.Server.URL
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer": p.Issuer, "authorization_endpoint": b + "/authorize", "token_endpoint": b + "/token", "jwks_uri": b + "/jwks",
		"end_session_endpoint": b + "/end-session", "response_types_supported": []string{"code"},
		"subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"},
		"code_challenge_methods_supported": []string{"S256"},
	})
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (p *FakeIdP) jwks(w http.ResponseWriter, r *http.Request) {
	pub := p.key.PublicKey
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "k1", "n": b64(pub.N.Bytes()), "e": b64(big.NewInt(int64(pub.E)).Bytes())}}})
}

func (p *FakeIdP) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("client_id") != p.ClientID || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		http.Error(w, "bad authorization request", http.StatusBadRequest)
		return
	}
	redirect := q.Get("redirect_uri")
	p.mu.Lock()
	defer p.mu.Unlock()
	u, _ := url.Parse(redirect)
	rq := u.Query()
	rq.Set("state", q.Get("state"))
	if p.Deny {
		p.Deny = false
		rq.Set("error", "access_denied")
		u.RawQuery = rq.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
		return
	}
	user := IdPUser{Sub: "sub-default", Email: "default@example.com", Name: "Default User", Groups: []string{"family"}}
	if p.Next != nil {
		user, p.Next = *p.Next, nil
	}
	code := b64(randBytes(16))
	p.codes[code] = codeInfo{user: user, challenge: q.Get("code_challenge"), nonce: q.Get("nonce"), redirect: redirect}
	rq.Set("code", code)
	u.RawQuery = rq.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func randBytes(n int) []byte { b := make([]byte, n); _, _ = rand.Read(b); return b }

func (p *FakeIdP) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id, sec, _ := r.BasicAuth()
	if id == "" {
		id, sec = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if unesc, err := url.QueryUnescape(id); err == nil {
		id = unesc
	}
	if unesc, err := url.QueryUnescape(sec); err == nil {
		sec = unesc
	}
	p.mu.Lock()
	ci, ok := p.codes[r.PostForm.Get("code")]
	delete(p.codes, r.PostForm.Get("code"))
	p.mu.Unlock()
	fail := func(msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		fmt.Fprintf(w, `{"error":"invalid_grant","error_description":%q}`, msg)
	}
	if !ok || id != p.ClientID || sec != p.Secret || r.PostForm.Get("redirect_uri") != ci.redirect {
		fail("bad code, client or redirect")
		return
	}
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if b64(sum[:]) != ci.challenge {
		fail("pkce mismatch")
		return
	}
	now := time.Now()
	claims := map[string]any{"iss": p.Issuer, "sub": ci.user.Sub, "aud": p.ClientID, "exp": now.Add(time.Hour).Unix(), "iat": now.Unix(),
		"nonce": ci.nonce, "email": ci.user.Email, "name": ci.user.Name}
	if !p.NoGroupsInIDToken {
		claims["groups"] = ci.user.Groups
	}
	if ci.user.Locale != "" {
		claims["locale"] = ci.user.Locale
	}
	if p.WrongNonce {
		claims["nonce"] = "wrong"
		p.WrongNonce = false
	}
	tok := p.Sign(claims)
	if p.ForgeNext {
		p.ForgeNext = false
		tok = SignWith(NewKey(), claims)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-" + b64(randBytes(8)), "token_type": "Bearer", "expires_in": 3600, "id_token": tok})
}

// Sign creates an RS256 JWT (exported so tests can forge tokens with other keys/claims via SignWith).
func (p *FakeIdP) Sign(claims map[string]any) string { return SignWith(p.key, claims) }

// SignWith signs claims with an arbitrary key (to test signature verification).
func SignWith(key *rsa.PrivateKey, claims map[string]any) string {
	hb, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "k1"})
	pb, _ := json.Marshal(claims)
	in := b64(hb) + "." + b64(pb)
	sum := sha256.Sum256([]byte(in))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	return in + "." + b64(sig)
}

// NewKey generates a throwaway RSA key.
func NewKey() *rsa.PrivateKey { k, _ := rsa.GenerateKey(rand.Reader, 2048); return k }

func (p *FakeIdP) endSession(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.LoggedOut = append(p.LoggedOut, r.URL.Query())
	p.mu.Unlock()
	if to := r.URL.Query().Get("post_logout_redirect_uri"); to != "" {
		http.Redirect(w, r, to, http.StatusFound)
		return
	}
	fmt.Fprint(w, "signed out")
}

// controlNext lets browser tests choose the identity of the next login:
// /control/next?sub=sub-alice&name=Alice&email=a@e.com&groups=family,recipes-admins&locale=he
func (p *FakeIdP) controlNext(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	u := IdPUser{Sub: q.Get("sub"), Name: q.Get("name"), Email: q.Get("email"), Locale: q.Get("locale")}
	for _, g := range strings.Split(q.Get("groups"), ",") {
		if g != "" {
			u.Groups = append(u.Groups, g)
		}
	}
	p.mu.Lock()
	p.Next = &u
	p.mu.Unlock()
	fmt.Fprint(w, "ok")
}
