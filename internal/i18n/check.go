package i18n

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Problem is one i18ncheck finding.
type Problem struct{ Kind, Key, Detail string }

func (p Problem) String() string { return fmt.Sprintf("%s: %s %s", p.Kind, p.Key, p.Detail) }

var (
	placeholderRe = regexp.MustCompile(`\{\{\s*\.(\w+)\s*\}\}`)
	// T "key" in templates; T("key") / .T("key") / Loc.T("key") in Go.
	tmplKeyRe = regexp.MustCompile(`\{\{[^}]*?\bT\s+"([a-z0-9_.]+)"`)
	goKeyRe   = regexp.MustCompile(`\bT\(\s*"([a-z0-9_]+(?:\.[a-z0-9_]+)+)"`)
	// forbidden words in UI copy (70 §9); matched on English values.
	forbidden         = regexp.MustCompile(`(?i)\b(oidc|tokens?|labels?|tags?|entity|entities|sync|synchron\w*|404|accept\w*|reject\w*)\b`)
	stripPlaceholders = regexp.MustCompile(`\{\{[^}]*\}\}|\{[a-zA-Z_]+\}`)
	// dynamic key prefixes: a key referenced as T (printf "oven.mode.%s" ...) is checked via these prefixes.
	dynRe = regexp.MustCompile(`printf\s+"([a-z0-9_.]+)\.%s"`)
)

// Check validates the catalog union: parity between languages, placeholder parity, forbidden words,
// and references from templates and Go code (root is the repository root; "" skips reference scanning).
func Check(c *Catalog, root string) []Problem {
	var ps []Problem
	en, he := c.raw["en"], c.raw["he"]
	for k := range en {
		if _, ok := he[k]; !ok {
			ps = append(ps, Problem{"missing-he", k, "defined in en but not in he"})
		}
	}
	for k := range he {
		if _, ok := en[k]; !ok {
			ps = append(ps, Problem{"missing-en", k, "defined in he but not in en"})
		}
	}
	for k, ef := range en {
		hf, ok := he[k]
		if !ok {
			continue
		}
		if a, b := placeholders(ef), placeholders(hf); a != b {
			ps = append(ps, Problem{"placeholders", k, fmt.Sprintf("en {%s} vs he {%s}", a, b)})
		}
		if strings.HasPrefix(k, "time.ago.") || strings.HasSuffix(k, ".plural") {
			if _, ok := hf["many"]; !ok {
				ps = append(ps, Problem{"plural", k, "he needs one/two/many/other"})
			}
		}
		for form, txt := range ef {
			if strings.TrimSpace(txt) == "" {
				ps = append(ps, Problem{"empty", k, form})
			}
			if m := forbidden.FindString(stripPlaceholders.ReplaceAllString(txt, "")); m != "" && !strings.HasPrefix(k, "issue.") {
				ps = append(ps, Problem{"forbidden-word", k, fmt.Sprintf("%q in %q", m, txt)})
			}
		}
	}
	if root != "" {
		used, dynamic := references(root)
		for k, where := range used {
			if _, ok := en[k]; !ok {
				ps = append(ps, Problem{"undefined", k, "used in " + where})
			}
		}
		for _, pre := range dynamic {
			found := false
			for k := range en {
				if strings.HasPrefix(k, pre+".") {
					found = true
					break
				}
			}
			if !found {
				ps = append(ps, Problem{"undefined-prefix", pre, "dynamic key prefix has no messages"})
			}
		}
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].String() < ps[j].String() })
	return ps
}

func placeholders(forms map[string]string) string {
	set := map[string]bool{}
	for _, t := range forms {
		for _, m := range placeholderRe.FindAllStringSubmatch(t, -1) {
			set[m[1]] = true
		}
	}
	var ks []string
	for k := range set {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ",")
}

func references(root string) (map[string]string, []string) {
	used := map[string]string{}
	var dyn []string
	walk := func(dir string, re *regexp.Regexp, ext string, handleDyn bool) {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ext) || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			b, _ := os.ReadFile(p)
			s := string(b)
			for _, m := range re.FindAllStringSubmatch(s, -1) {
				if _, ok := used[m[1]]; !ok {
					rel, _ := filepath.Rel(root, p)
					used[m[1]] = rel
				}
			}
			if handleDyn {
				for _, m := range dynRe.FindAllStringSubmatch(s, -1) {
					dyn = append(dyn, m[1])
				}
			}
			return nil
		})
	}
	walk("web/templates", tmplKeyRe, ".html", true)
	walk("internal", goKeyRe, ".go", false)
	return used, dyn
}
