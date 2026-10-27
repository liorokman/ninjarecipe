// Package textnorm implements the search normalization of 20 §11 (identical for indexing and querying).
package textnorm

import (
	"strings"
	"unicode"

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
