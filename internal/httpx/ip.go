package httpx

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// IPParser handles IP resolution based on trusted proxy configuration.
type IPParser struct {
	trustedPrefixes []netip.Prefix
}

// NewIPParser creates a new IPParser.
func NewIPParser(trustedProxies []string) *IPParser {
	var prefixes []netip.Prefix
	for _, s := range trustedProxies {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !strings.Contains(s, "/") {
			// Bare IP
			if ip, err := netip.ParseAddr(s); err == nil {
				prefixes = append(prefixes, netip.PrefixFrom(ip, ip.BitLen()))
			}
		} else {
			if prefix, err := netip.ParsePrefix(s); err == nil {
				prefixes = append(prefixes, prefix)
			}
		}
	}
	return &IPParser{trustedPrefixes: prefixes}
}

// ClientIP extracts the real client IP from the request.
func (p *IPParser) ClientIP(r *http.Request) string {
	remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remoteHost = r.RemoteAddr
	}
	remoteHost = strings.TrimSpace(remoteHost)

	if len(p.trustedPrefixes) == 0 {
		return remoteHost
	}

	remoteAddr, err := netip.ParseAddr(remoteHost)
	if err != nil {
		return remoteHost
	}

	// Check if direct peer is trusted
	if len(p.trustedPrefixes) > 0 {
		peerTrusted := false
		for _, prefix := range p.trustedPrefixes {
			if prefix.Contains(remoteAddr) {
				peerTrusted = true
				break
			}
		}
		if !peerTrusted {
			return remoteHost
		}
	}

	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return remoteHost
	}

	// Walk from right to left to find right-most untrusted address
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		part := strings.TrimSpace(parts[i])
		if part == "" {
			continue
		}
		addr, err := netip.ParseAddr(part)
		if err != nil {
			// Non-valid IP, skip or return
			continue
		}

		if len(p.trustedPrefixes) == 0 {
			// If trustProxy is true and no specific list provided, return first from left or right
			return addr.String()
		}

		trusted := false
		for _, prefix := range p.trustedPrefixes {
			if prefix.Contains(addr) {
				trusted = true
				break
			}
		}
		if !trusted {
			return addr.String()
		}
	}

	return remoteHost
}
