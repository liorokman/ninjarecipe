// Command cspcheck fails when any template contains inline script code, on*= attributes, style= attributes,
// external script/style URLs or hard-coded href="/ action="/ paths (20 §8, 60 §2.2).
//
//	go run ./internal/tools/cspcheck [-root .]
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type rule struct {
	name string
	re   *regexp.Regexp
}

var rules = []rule{
	{"inline <script> code", regexp.MustCompile(`(?is)<script\b(?:[^>]*\btype\s*=\s*["']?(?:application/json|importmap)["']?[^>]*>|[^>]*\bsrc\s*=[^>]*>)|<script\b[^>]*>\s*[^<\s]`)},
	{"on*= attribute", regexp.MustCompile(`(?i)<[a-z][^>]*\s+on[a-z]+\s*=`)},
	{"style= attribute", regexp.MustCompile(`(?i)<[a-z][^>]*\sstyle\s*=`)},
	{"external script/style URL", regexp.MustCompile(`(?i)<(?:script|link)\b[^>]*(?:src|href)\s*=\s*["']?https?://`)},
	{"hard-coded path", regexp.MustCompile(`(?i)\b(?:href|action)\s*=\s*["']/`)},
}

// check returns violations for one template source.
func check(src string) []string {
	var out []string
	// remove allowed script forms (external src, JSON data blocks) before looking for inline code
	allowedScript := regexp.MustCompile(`(?is)<script\b[^>]*\bsrc\s*=[^>]*>\s*</script>|<script\b[^>]*\btype\s*=\s*["']?application/json["']?[^>]*>.*?</script>`)
	stripped := allowedScript.ReplaceAllString(src, "")
	if regexp.MustCompile(`(?is)<script\b`).MatchString(stripped) {
		out = append(out, "inline <script> code")
	}
	for _, r := range rules[1:] {
		if r.re.MatchString(src) {
			out = append(out, r.name)
		}
	}
	return out
}

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	bad := 0
	_ = filepath.WalkDir(filepath.Join(*root, "web", "templates"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return nil
		}
		b, _ := os.ReadFile(p)
		for _, v := range check(string(b)) {
			fmt.Printf("%s: %s\n", p, v)
			bad++
		}
		return nil
	})
	if bad > 0 {
		fmt.Fprintf(os.Stderr, "csp:check: %d violation(s)\n", bad)
		os.Exit(1)
	}
	fmt.Println("csp:check: ok")
}
