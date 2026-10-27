package images

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

// FetchError describes why a remote image could not be fetched; Code is the warning code of the API.
type FetchError struct {
	Code   string // fetch_failed | blocked | too_large | invalid_url
	Status int
	Err    error
}

func (e *FetchError) Error() string { return fmt.Sprintf("%s: %v", e.Code, e.Err) }
func (e *FetchError) Unwrap() error { return e.Err }

// Fetcher downloads images with the SSRF guard of 20 §8.
type Fetcher struct {
	AllowHTTP bool
	Timeout   time.Duration
	MaxBytes  int64
	UserAgent string
	// AllowAll disables the address guard. Tests only.
	AllowAll bool
}

var (
	nat64      = netip.MustParsePrefix("64:ff9b::/96")
	nat64Local = netip.MustParsePrefix("64:ff9b:1::/48")
	cgnat      = netip.MustParsePrefix("100.64.0.0/10")
	ula        = netip.MustParsePrefix("fc00::/7")
	sixToFour  = netip.MustParsePrefix("2002::/16")
)

// IsPublicAddr reports whether an address may be fetched: rejects loopback, private, link-local, CGNAT,
// ULA, multicast, unspecified, and IPv4-mapped / NAT64 / 6to4 forms of those.
func IsPublicAddr(a netip.Addr) bool {
	a = a.Unmap()
	if a.Is6() {
		b := a.As16()
		switch {
		case nat64.Contains(a):
			return IsPublicAddr(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}))
		case nat64Local.Contains(a):
			return false
		case sixToFour.Contains(a):
			return IsPublicAddr(netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}))
		}
	}
	if !a.IsValid() || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() || a.IsMulticast() || a.IsUnspecified() || cgnat.Contains(a) || ula.Contains(a) {
		return false
	}
	if a.Is4() && (a.As4()[0] == 0 || a.As4()[0] >= 240) {
		return false
	}
	return true
}

// IsPublicIP is the net.IP variant.
func IsPublicIP(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	return ok && IsPublicAddr(a)
}

// Fetch downloads rawURL and returns the body (bounded by MaxBytes).
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return nil, &FetchError{Code: "invalid_url", Err: errors.New("invalid url")}
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && f.AllowHTTP) {
		return nil, &FetchError{Code: "blocked", Err: errors.New("only https is allowed")}
	}
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	max := f.MaxBytes
	if max <= 0 {
		max = 15 << 20
	}
	dialer := &net.Dialer{
		Timeout: timeout,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			a, err := netip.ParseAddr(host)
			if err != nil || (!f.AllowAll && !IsPublicAddr(a)) {
				return fmt.Errorf("address %s is not allowed", host)
			}
			return nil
		},
	}
	client := &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{DialContext: dialer.DialContext, Proxy: nil, DisableKeepAlives: true},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 3 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" && !(req.URL.Scheme == "http" && f.AllowHTTP) {
				return errors.New("redirect to non-https url")
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, &FetchError{Code: "invalid_url", Err: err}
	}
	ua := f.UserAgent
	if ua == "" {
		ua = "ninja-recipes/dev"
	}
	req.Header.Set("User-Agent", ua)
	resp, err := client.Do(req)
	if err != nil {
		code := "fetch_failed"
		var ue *url.Error
		if errors.As(err, &ue) && (containsAny(ue.Error(), "is not allowed", "non-https", "too many redirects")) {
			code = "blocked"
		}
		return nil, &FetchError{Code: code, Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &FetchError{Code: "fetch_failed", Status: resp.StatusCode, Err: fmt.Errorf("status %d", resp.StatusCode)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, &FetchError{Code: "fetch_failed", Err: err}
	}
	if int64(len(body)) > max {
		return nil, &FetchError{Code: "too_large", Err: errors.New("image too large")}
	}
	return body, nil
}

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		for i := 0; i+len(x) <= len(s); i++ {
			if s[i:i+len(x)] == x {
				return true
			}
		}
	}
	return false
}
