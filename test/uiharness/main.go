// Command uiharness serves the UI for development and browser tests.
//
//	go run ./test/uiharness -fixtures      # canned data, no DB, any session (Phase 0)
//
// It prints one JSON line on stdout describing the base URL and test sessions, then serves until killed.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"cooking/internal/domain"
	"cooking/internal/httpx"
	"cooking/internal/i18n"
	"cooking/internal/web"
	webfs "cooking/web"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "listen address")
	fixtures := flag.Bool("fixtures", false, "serve canned fixture data without a database")
	webDir := flag.String("web", "", "directory of ./web to read templates and static files from (live reload); default: embedded")
	flag.Parse()
	if !*fixtures {
		runReal(*addr, *webDir)
		return
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	base := "http://" + ln.Addr().String()
	cat, err := i18n.Load(localesFS(*webDir))
	if err != nil {
		log.Fatal(err)
	}
	rd := web.NewRenderer(web.RendererOpts{FS: webfs.FS, Dir: *webDir, Catalog: cat, AssetVersion: "dev", BaseURL: base, DevMode: true,
		InboxCount: func(r *http.Request, u *domain.User) int { return 3 }})
	h := newFixtureHandler(rd, cat, base)
	h = httpx.Chain(h, httpx.Recover, httpx.RequestID, httpx.SecurityHeaders(base, ""))
	info := map[string]any{
		"baseURL": base,
		"mode":    "fixtures",
		"users": map[string]any{
			"alice": map[string]string{"id": "u-alice", "cookieName": "nwr_session", "cookieValue": "fx-alice"},
			"bob":   map[string]string{"id": "u-bob", "cookieName": "nwr_session", "cookieValue": "fx-bob"},
			"admin": map[string]string{"id": "u-admin", "cookieName": "nwr_session", "cookieValue": "fx-admin"},
		},
		"recipes": map[string]string{"ribs": "fx-ribs", "chicken": "fx-chicken", "hebrew": "fx-pizza-he", "ownerless": "fx-ownerless",
			"bread": "fx-bread", "noStages": "fx-nostage", "bobPrivate": "fx-bob-private", "inbox1": "fx-inbox-1", "inbox2": "fx-inbox-2"},
	}
	b, _ := json.Marshal(info)
	fmt.Println(string(b))
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	fmt.Fprintln(os.Stderr, "uiharness (fixtures) listening on", base)
	log.Fatal(srv.Serve(ln))
}
