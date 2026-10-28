package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/liorokman/ninjarecipe/internal/db"
)

func runCLI(t *testing.T, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, func(k string) string { return env[k] }, nil)
	return code, out.String(), errb.String()
}

func TestVersionAndUnknown(t *testing.T) {
	code, out, _ := runCLI(t, nil, "version")
	if code != 0 || !strings.HasPrefix(out, "recipes ") {
		t.Fatal(code, out)
	}
	if code, _, _ := runCLI(t, nil, "frobnicate"); code != 1 {
		t.Fatal("unknown command")
	}
	if code, out, _ := runCLI(t, nil, "help"); code != 0 || !strings.Contains(out, "backup") {
		t.Fatal("help")
	}
}

func TestMigrateReindexBackupUser(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{"APP_DATA_DIR": dir}
	if code, out, e := runCLI(t, env, "migrate", "up"); code != 0 {
		t.Fatal(code, out, e)
	}
	code, out, _ := runCLI(t, env, "migrate", "status")
	if code != 0 || !strings.Contains(out, "0001_initial_schema.sql") || !strings.Contains(out, "0002_seed_categories.sql") {
		t.Fatalf("status: %s", out)
	}
	if code, _, _ := runCLI(t, env, "migrate", "down-to", "0"); code != 1 {
		t.Fatal("down-to is not offered")
	}
	if code, out, _ := runCLI(t, env, "reindex"); code != 0 || !strings.Contains(out, "reindexed 0") {
		t.Fatal(out)
	}
	bdir := t.TempDir()
	code, out, e := runCLI(t, env, "backup", "--out", bdir)
	if code != 0 || !strings.HasPrefix(strings.TrimSpace(out), bdir) {
		t.Fatal(code, out, e)
	}
	// users
	d, _ := db.Open(dir)
	_, _ = d.Writer.Exec(`INSERT INTO users (id, oidc_issuer, oidc_subject, email, display_name) VALUES ('u1','i','s','a@e.com','Alice')`)
	_, _ = d.Writer.Exec(`INSERT INTO sessions (id_hash, user_id, expires_at) VALUES ('h','u1','2999-01-01T00:00:00.000Z')`)
	_, _ = d.Writer.Exec(`INSERT INTO recipes (id, owner_id, title) VALUES ('r1','u1','Mine')`)
	d.Close()
	if code, out, _ := runCLI(t, env, "user", "list"); code != 0 || !strings.Contains(out, "Alice") || !strings.Contains(out, "a@e.com") {
		t.Fatal(out)
	}
	if code, out, _ := runCLI(t, env, "user", "revoke-sessions", "A@E.com"); code != 0 || !strings.Contains(out, "revoked 1") {
		t.Fatal(out)
	}
	if code, _, _ := runCLI(t, env, "user", "delete", "nobody", "--yes"); code != 1 {
		t.Fatal("unknown user")
	}
	code, out, _ = runCLI(t, env, "user", "delete", "u1", "--yes")
	if code != 0 || !strings.Contains(out, "1 private/inbox recipes") || !strings.Contains(out, "deleted") {
		t.Fatal(code, out)
	}
	if _, out, _ := runCLI(t, env, "user", "list"); strings.Contains(out, "Alice") {
		t.Fatal("user still listed")
	}
}

func TestNewerDatabaseExitCode3(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{"APP_DATA_DIR": dir}
	runCLI(t, env, "migrate", "up")
	d, _ := db.Open(dir)
	_, _ = d.Writer.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (99, 1)`)
	d.Close()
	if code, _, e := runCLI(t, env, "migrate", "up"); code != exitNewer || !strings.Contains(e, "newer") {
		t.Fatal(code, e)
	}
	if code, _, _ := runCLI(t, env, "reindex"); code != exitNewer {
		t.Fatal(code)
	}
}

func TestServeConfigErrorExit2(t *testing.T) {
	var out, errb bytes.Buffer
	if code := serve(&out, &errb, nil); code != exitConfig {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errb.String(), "APP_BASE_URL") {
		t.Fatal(errb.String())
	}
}

func TestHealthcheck(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.Write([]byte("ok"))
			return
		}
		w.WriteHeader(500)
	})}
	go srv.Serve(ln)
	defer srv.Shutdown(context.Background())
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	var errb bytes.Buffer
	if healthcheck(":"+port, &errb) != 0 {
		t.Fatal("healthy")
	}
	if healthcheck("0.0.0.0:"+port, &errb) != 0 {
		t.Fatal("wildcard host maps to 127.0.0.1")
	}
	if healthcheck("127.0.0.1:1", &errb) != 1 {
		t.Fatal("down")
	}
	if healthcheck("nonsense", &errb) != 1 {
		t.Fatal("bad addr")
	}
}
