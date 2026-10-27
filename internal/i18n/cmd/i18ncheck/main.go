// Command i18ncheck validates the EN/HE catalogs (union of backend and UI files).
//
//	go run ./internal/i18n/cmd/i18ncheck [-root .]
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"cooking/internal/i18n"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	cat, err := i18n.Load(os.DirFS(filepath.Join(*root, "locales")))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ps := i18n.Check(cat, *root)
	for _, p := range ps {
		fmt.Println(p)
	}
	if len(ps) > 0 {
		fmt.Fprintf(os.Stderr, "i18ncheck: %d problem(s)\n", len(ps))
		os.Exit(1)
	}
	fmt.Printf("i18ncheck: ok (%d keys per language)\n", len(cat.Keys("en")))
}
