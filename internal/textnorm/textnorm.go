// Package textnorm implements the search normalization of 20 §11 (identical for indexing and querying).
package textnorm

import (
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/idna"
	"golang.org/x/text/unicode/norm"
)

var finals = strings.NewReplacer("ך", "כ", "ם", "מ", "ן", "נ", "ף", "פ", "ץ", "צ")

// Normalize: NFKC, lower-case, drop Hebrew niqqud/cantillation (U+0591-U+05C7), map final letters, collapse whitespace.
func Normalize(s string) string {
	s = norm.NFKC.String(s)
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if r >= 0x0591 && r <= 0x05C7 {
			continue
		}
		b.WriteRune(r)
	}
	return strings.Join(strings.FieldsFunc(finals.Replace(b.String()), unicode.IsSpace), " ")
}

// Terms splits a normalized query into FTS phrases (>= 3 runes) and short terms (1-2 runes).
func Terms(q string) (long, short []string) {
	for _, t := range strings.Fields(Normalize(q)) {
		n := len([]rune(t))
		if n >= 3 {
			long = append(long, t)
		} else if n > 0 {
			short = append(short, t)
		}
	}
	return
}

// MatchExpr builds the FTS5 MATCH expression: quoted phrases AND-ed ("" when no long terms).
func MatchExpr(long []string) string {
	parts := make([]string, len(long))
	for i, t := range long {
		parts[i] = `"` + strings.ReplaceAll(t, `"`, `""`) + `"`
	}
	return strings.Join(parts, " AND ")
}

// EscapeLike escapes %, _ and \ for LIKE ... ESCAPE '\'.
func EscapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// trackingParams are query parameters that never identify a page (40 §4.3).
var trackingParams = map[string]bool{"fbclid": true, "gclid": true, "mc_cid": true, "mc_eid": true, "igshid": true, "ref": true}

// SourceKey normalizes a recipe URL for duplicate detection (40 §4.3). It returns "" when raw is empty or is
// not an absolute http(s) URL.
func SourceKey(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return ""
	}
	host, err := idna.Lookup.ToASCII(strings.ToLower(u.Hostname()))
	if err != nil || host == "" {
		return ""
	}
	host = strings.TrimPrefix(host, "www.")
	if port := u.Port(); port != "" && !(u.Scheme == "http" && port == "80") && !(u.Scheme == "https" && port == "443") {
		host += ":" + port
	}

	p := path.Clean("/" + u.EscapedPath())
	p = unescapeUnreserved(p)
	p = strings.TrimSuffix(p, "/")

	var pairs []string
	for _, kv := range strings.Split(u.RawQuery, "&") {
		if kv == "" {
			continue
		}
		name, _, _ := strings.Cut(kv, "=")
		if n, err := url.QueryUnescape(name); err == nil {
			name = n
		}
		name = strings.ToLower(name)
		if strings.HasPrefix(name, "utm_") || trackingParams[name] {
			continue
		}
		pairs = append(pairs, kv)
	}
	sort.Strings(pairs)
	key := host + p
	if len(pairs) > 0 {
		key += "?" + strings.Join(pairs, "&")
	}
	return key
}

// unescapeUnreserved decodes %XX sequences that encode RFC 3986 unreserved characters and upper-cases the rest.
func unescapeUnreserved(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				c := byte(v)
				if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~' {
					b.WriteByte(c)
				} else {
					b.WriteString("%" + strings.ToUpper(s[i+1:i+3]))
				}
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
