package httpx

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestULID(t *testing.T) {
	re := regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)
	a, b := NewULID(time.Now()), NewULID(time.Now())
	if !re.MatchString(a) || a == b {
		t.Fatalf("%q %q", a, b)
	}
	early, late := NewULID(time.Unix(1000, 0)), NewULID(time.Unix(2000, 0))
	if early >= late {
		t.Fatal("ULIDs must sort by time")
	}
}

func TestRequestID(t *testing.T) {
	var seen string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = RequestIDFromContext(r.Context()) }))
	for in, keep := range map[string]bool{"abc-123": true, "bad id!": false, "": false, strings.Repeat("a", 65): false} {
		r := httptest.NewRequest("GET", "/", nil)
		if in != "" {
			r.Header.Set("X-Request-Id", in)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if keep && (seen != in || w.Header().Get("X-Request-Id") != in) {
			t.Errorf("%q should be kept, got %q", in, seen)
		}
		if !keep && (seen == in || len(seen) != 26) {
			t.Errorf("%q should be replaced by a ULID, got %q", in, seen)
		}
	}
}

func TestRecover(t *testing.T) {
	h := Recover(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("boom") }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 500 {
		t.Fatal(w.Code)
	}
}

func TestCSPExact(t *testing.T) {
	want := "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; connect-src 'self'; font-src 'self'; worker-src 'self'; manifest-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self' https://auth.example.com"
	if got := CSP("https://auth.example.com"); got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
	if strings.Contains(CSP(""), "unsafe") || !strings.HasSuffix(CSP(""), "form-action 'self'") {
		t.Fatal("no issuer")
	}
	if Origin("https://auth.example.com/application/o/x/") != "https://auth.example.com" || Origin("nonsense") != "" {
		t.Fatal("Origin")
	}
}

func TestSecurityHeadersHSTS(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	w := httptest.NewRecorder()
	SecurityHeaders("https://r.example.com", "https://a.example.com/x/")(ok).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Header().Get("Strict-Transport-Security") != "max-age=31536000" || w.Header().Get("Content-Security-Policy") == "" {
		t.Fatal(w.Header())
	}
	w = httptest.NewRecorder()
	SecurityHeaders("http://localhost:8080", "")(ok).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Header().Get("Strict-Transport-Security") != "" {
		t.Fatal("no HSTS over http")
	}
}

func TestHostCheck(t *testing.T) {
	h := HostCheck("https://recipes.example.com")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for host, path := range map[string]string{"recipes.example.com": "/", "RECIPES.example.com": "/x"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 204 {
			t.Errorf("%s: %d", host, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "evil.example.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("foreign host must be 400")
	}
	for _, p := range []string{"/healthz", "/readyz"} {
		r = httptest.NewRequest("GET", p, nil)
		r.Host = "10.1.2.3:8080"
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 204 {
			t.Errorf("%s must be exempt", p)
		}
	}
}

func TestCrossOriginProtection(t *testing.T) {
	h := CrossOriginProtection("https://recipes.example.com")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	do := func(method, path string, hdr map[string]string) int {
		r := httptest.NewRequest(method, path, nil)
		r.Host = "recipes.example.com"
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if do("POST", "/recipes", map[string]string{"Sec-Fetch-Site": "cross-site"}) != 403 {
		t.Fatal("cross-site POST must be blocked")
	}
	if do("POST", "/recipes", map[string]string{"Origin": "https://evil.example.com"}) != 403 {
		t.Fatal("foreign Origin must be blocked")
	}
	if do("POST", "/recipes", map[string]string{"Sec-Fetch-Site": "same-origin"}) != 204 {
		t.Fatal("same-origin POST allowed")
	}
	if do("POST", "/recipes", map[string]string{"Origin": "https://recipes.example.com"}) != 204 {
		t.Fatal("trusted origin allowed")
	}
	if do("GET", "/recipes", map[string]string{"Sec-Fetch-Site": "cross-site"}) != 204 {
		t.Fatal("GET is safe")
	}
	if do("POST", "/api/v1/inbox", map[string]string{"Sec-Fetch-Site": "cross-site"}) != 204 {
		t.Fatal("/api/ is exempt")
	}
	if do("POST", "/recipes", nil) != 204 {
		t.Fatal("non-browser (no Origin/Sec-Fetch-Site) allowed")
	}
}

func TestClientIP(t *testing.T) {
	p := NewIPParser([]string{"10.0.0.0/8", "192.168.1.1"})
	mk := func(remote, xff string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return r
	}
	cases := []struct{ remote, xff, want string }{
		{"203.0.113.7:5000", "1.2.3.4", "203.0.113.7"},    // untrusted peer: header ignored
		{"10.0.0.5:5000", "1.2.3.4", "1.2.3.4"},           // trusted proxy
		{"10.0.0.5:5000", "9.9.9.9, 1.2.3.4", "1.2.3.4"},  // right-most
		{"10.0.0.5:5000", "1.2.3.4, 10.0.0.9", "1.2.3.4"}, // skip trusted hops
		{"10.0.0.5:5000", "", "10.0.0.5"},                 // no header
		{"10.0.0.5:5000", "garbage", "10.0.0.5"},          // unparsable
		{"192.168.1.1:80", "8.8.8.8", "8.8.8.8"},          // single-address trust
	}
	for _, c := range cases {
		if got := p.ClientIP(mk(c.remote, c.xff)); got != c.want {
			t.Errorf("%s xff=%q: %s want %s", c.remote, c.xff, got, c.want)
		}
	}
	if NewIPParser(nil).ClientIP(mk("10.0.0.5:5000", "1.2.3.4")) != "10.0.0.5" {
		t.Fatal("no trusted proxies: never honour the header")
	}
}

func TestLimiter(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewLimiter(60, 20)
	l.SetClock(func() time.Time { return now })
	for i := 0; i < 20; i++ {
		if ok, _ := l.Allow("tok"); !ok {
			t.Fatalf("burst %d denied", i)
		}
	}
	ok, retry := l.Allow("tok")
	if ok || retry != 1 {
		t.Fatalf("21st: ok=%v retry=%d", ok, retry)
	}
	if ok, _ := l.Allow("other"); !ok {
		t.Fatal("buckets are per key")
	}
	now = now.Add(3 * time.Second)
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("tok"); !ok {
			t.Fatalf("refill %d", i)
		}
	}
	if ok, _ := l.Allow("tok"); ok {
		t.Fatal("only 3 refilled")
	}
}

func TestAccessLogSkipsHealthAndHidesAuthQuery(t *testing.T) {
	h := AccessLog(NewIPParser(nil))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { SetUserID(r.Context(), "u1"); w.WriteHeader(201) }))
	for _, p := range []string{"/healthz", "/static/dev/app.js", "/auth/callback?code=SECRET&state=S", "/recipes?q=x"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 201 {
			t.Fatal(p, w.Code)
		}
	}
}
