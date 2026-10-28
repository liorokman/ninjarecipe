package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/liorokman/ninjarecipe/locales"
	"github.com/liorokman/ninjarecipe/test/harness"
)

// runReal starts the real application (SQLite in a temp dir, fake OIDC provider, seeded data) and blocks.
func runReal(addr, webDir string) {
	h, err := harness.Start(harness.Options{ListenAddr: addr, WebDir: webDir})
	if err != nil {
		log.Fatal(err)
	}
	defer h.Close()
	seed, err := h.SeedUI()
	if err != nil {
		log.Fatal(err)
	}
	users := map[string]any{}
	for k, u := range seed.Users {
		ck := seed.Cookies[k]
		users[k] = map[string]string{"id": u.ID, "cookieName": ck.Name, "cookieValue": ck.Value, "idpSub": "sub-" + k}
	}
	info := map[string]any{"baseURL": h.URL, "mode": "real", "users": users, "recipes": seed.Recipes, "token": seed.Token,
		"idp": map[string]string{"issuer": h.IdP.Issuer, "control": strings.TrimSuffix(h.IdP.Issuer, "/") + "/control/next"}}
	b, _ := json.Marshal(info)
	fmt.Println(string(b))
	fmt.Fprintln(os.Stderr, "uiharness (real) listening on", h.URL)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
}

func localesFS(webDir string) fs.FS {
	if webDir != "" {
		return os.DirFS(webDir + "/../locales")
	}
	return locales.FS
}
