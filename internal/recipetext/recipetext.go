// Package recipetext holds the pure text parsers of the recipe form contract (40 §2.5-2.6).
package recipetext

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/liorokman/ninjarecipe/internal/domain"
)

var unitAliases = map[string][]string{
	"g":     {"g", "gr", "gram", "grams", "גרם", "ג'"},
	"kg":    {"kg", "kilo", "ק\"ג", "קילו"},
	"ml":    {"ml", "מ\"ל"},
	"l":     {"l", "liter", "litre", "ליטר"},
	"tsp":   {"tsp", "teaspoon", "teaspoons", "כפית", "כפיות"},
	"tbsp":  {"tbsp", "tablespoon", "tablespoons", "כף", "כפות"},
	"pcs":   {"pcs", "pc", "piece", "pieces", "יח'", "יחידות"},
	"pinch": {"pinch", "קורט"},
}

var (
	aliasToUnit map[string]string
	ingRe       *regexp.Regexp
	stepTimerRe = regexp.MustCompile(`\s*\[(\d{1,3}):(\d{2})(?::(\d{2}))?\]\s*$`)
)

func init() {
	aliasToUnit = map[string]string{}
	var alts []string
	for u, as := range unitAliases {
		for _, a := range as {
			aliasToUnit[strings.ToLower(a)] = u
			alts = append(alts, regexp.QuoteMeta(a))
		}
	}
	// Longest alternatives first so "tbsp" wins over "t..." style prefixes (RE2 is leftmost-first).
	for i := range alts {
		for j := i + 1; j < len(alts); j++ {
			if len(alts[j]) > len(alts[i]) {
				alts[i], alts[j] = alts[j], alts[i]
			}
		}
	}
	amount := `\d+\s+\d+/\d+|\d+/\d+|\d+(?:[.,]\d+)?\s*[½¼¾⅓⅔]|\d+(?:[.,]\d+)?|[½¼¾⅓⅔]`
	ingRe = regexp.MustCompile(`(?i)^(` + amount + `)\s*(` + strings.Join(alts, "|") + `)?\s+(.+)$`)
}

var uni = map[rune]float64{'½': 0.5, '¼': 0.25, '¾': 0.75, '⅓': 1.0 / 3, '⅔': 2.0 / 3}

// ParseAmount converts "1,5", "1/2", "1 1/2", "½", "1½" into a number (0 = invalid).
func ParseAmount(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	total := 0.0
	rest := s
	if r, size := utf8.DecodeLastRuneInString(rest); uni[r] != 0 {
		total += uni[r]
		rest = strings.TrimSpace(rest[:len(rest)-size])
		if rest == "" {
			return total
		}
	}
	if i := strings.Index(rest, " "); i > 0 && strings.Contains(rest, "/") {
		w, err := strconv.Atoi(rest[:i])
		if err != nil {
			return 0
		}
		total += float64(w)
		rest = strings.TrimSpace(rest[i:])
	}
	if i := strings.Index(rest, "/"); i > 0 {
		a, e1 := strconv.Atoi(rest[:i])
		b, e2 := strconv.Atoi(rest[i+1:])
		if e1 != nil || e2 != nil || b == 0 {
			return 0
		}
		return total + float64(a)/float64(b)
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(rest, ",", "."), 64)
	if err != nil {
		return 0
	}
	return total + f
}

// ParseIngredientLine parses one non-header line.
func ParseIngredientLine(line string) domain.Ingredient {
	line = strings.TrimSpace(line)
	ing := domain.Ingredient{Item: line, RawText: line}
	m := ingRe.FindStringSubmatch(line)
	if m == nil {
		return ing
	}
	amt := ParseAmount(m[1])
	if amt <= 0 {
		return ing
	}
	ing.Amount = &amt
	if m[2] != "" {
		ing.Unit = aliasToUnit[strings.ToLower(m[2])]
	}
	ing.Item = strings.TrimSpace(m[3])
	return ing
}

// ParseIngredients parses the ingredients textarea: one per line, "# Header" lines start a section.
func ParseIngredients(text string) []domain.Ingredient {
	var out []domain.Ingredient
	section := ""
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			section = strings.TrimSpace(strings.TrimLeft(line, "#"))
			continue
		}
		ing := ParseIngredientLine(line)
		ing.Section = section
		ing.Position = len(out) + 1
		out = append(out, ing)
	}
	return out
}

// FormatIngredients is the inverse for the editor textarea (raw_text lines with "# Section" headers).
func FormatIngredients(ings []domain.Ingredient) string {
	var b strings.Builder
	cur := ""
	for _, i := range ings {
		if i.Section != cur {
			if i.Section != "" {
				fmt.Fprintf(&b, "# %s\n", i.Section)
			}
			cur = i.Section
		}
		b.WriteString(i.RawText)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// ParseSteps parses the steps textarea: one per non-blank line, optional trailing [mm:ss] / [h:mm:ss].
func ParseSteps(text string) []domain.Step {
	var out []domain.Step
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		st := domain.Step{Text: line}
		if m := stepTimerRe.FindStringSubmatch(line); m != nil {
			a, _ := strconv.Atoi(m[1])
			b, _ := strconv.Atoi(m[2])
			var sec int
			if m[3] != "" {
				c, _ := strconv.Atoi(m[3])
				sec = a*3600 + b*60 + c
			} else {
				sec = a*60 + b
			}
			rest := strings.TrimSpace(line[:len(line)-len(m[0])])
			if sec >= 1 && sec <= 86400 && b < 60 && rest != "" {
				st.Text = rest
				st.TimerSeconds = &sec
			}
		}
		if st.Text == "" {
			continue
		}
		st.Position = len(out) + 1
		out = append(out, st)
	}
	return out
}

// FormatTimer renders seconds as mm:ss or h:mm:ss (the [..] syntax body).
func FormatTimer(sec int) string {
	if sec >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", sec/3600, sec%3600/60, sec%60)
	}
	return fmt.Sprintf("%02d:%02d", sec/60, sec%60)
}

// FormatSteps is the inverse of ParseSteps.
func FormatSteps(steps []domain.Step) string {
	var lines []string
	for _, s := range steps {
		l := s.Text
		if s.TimerSeconds != nil {
			l += " [" + FormatTimer(*s.TimerSeconds) + "]"
		}
		lines = append(lines, l)
	}
	return strings.Join(lines, "\n")
}

// NormalizeTag applies the tag rules of 30 §7; ok=false for empty or too-long tags.
func NormalizeTag(s string) (string, bool) {
	s = norm.NFKC.String(s)
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	n := utf8.RuneCountInString(s)
	return s, n >= 1 && n <= domain.MaxTag
}

// SplitTags splits a comma list (also Hebrew/full-width commas) into normalized unique tags.
func SplitTags(s string) (tags []string, invalid bool) {
	seen := map[string]bool{}
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '，' || r == '،' || r == '\n' }) {
		if strings.TrimSpace(p) == "" {
			continue
		}
		t, ok := NormalizeTag(p)
		if !ok {
			invalid = true
			continue
		}
		if !seen[t] {
			seen[t] = true
			tags = append(tags, t)
		}
	}
	return
}
