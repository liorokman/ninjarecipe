package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) Lookup {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func minimal() map[string]string {
	return map[string]string{"APP_BASE_URL": "https://recipes.example.com/", "OIDC_ISSUER_URL": "https://auth.example.com/application/o/recipes/",
		"OIDC_CLIENT_ID": "cid", "OIDC_CLIENT_SECRET": "sec"}
}

func TestDefaults(t *testing.T) {
	c, warns, err := Load(env(minimal()), nil)
	if err != nil || len(warns) != 0 {
		t.Fatalf("%v %v", err, warns)
	}
	if c.BaseURL != "https://recipes.example.com" || c.ListenAddr != ":8080" || c.DataDir != "/data" || c.DefaultLang != "en" || c.Env != "production" ||
		c.SessionIdleTTL != 720*time.Hour || c.SessionMaxTTL != 8760*time.Hour || c.MaxImageMB != 15 || c.MaxImagesPerRecipe != 10 ||
		c.ImageMaxPixels != 50_000_000 || c.ImageWorkers != 1 || c.APIMaxBodyMB != 40 || c.APIRateLimitPerMin != 60 || c.ImageFetchTimeout != 15*time.Second ||
		c.ImageFetchAllowHTTP || c.LogLevel != "info" || c.LogFormat != "json" || !c.BackupAuto || c.BackupAt != "03:30" || c.BackupKeep != 7 ||
		c.Categorizer != "noop" || c.TZName != "UTC" || c.OIDCGroupsClaim != "groups" || c.OIDCAdminGroup != "recipes-admins" ||
		strings.Join(c.OIDCScopes, " ") != "openid email profile" || len(c.OIDCAllowedGroups) != 0 {
		t.Fatalf("defaults: %+v", c)
	}
	if !c.Secure() || c.Dev() {
		t.Fatal("secure/dev")
	}
}

func TestRequiredAndInvalidAreReportedTogether(t *testing.T) {
	_, _, err := Load(env(map[string]string{"APP_DEFAULT_LANG": "fr", "MAX_IMAGE_MB": "zero", "SESSION_IDLE_TTL": "soon", "BACKUP_AT": "25:99", "TZ": "Mars/Base", "LOG_LEVEL": "loud"}), nil)
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{"APP_BASE_URL", "OIDC_ISSUER_URL", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET", "APP_DEFAULT_LANG", "MAX_IMAGE_MB", "SESSION_IDLE_TTL", "BACKUP_AT", "TZ", "LOG_LEVEL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s: %v", want, err)
		}
	}
	m := minimal()
	m["APP_BASE_URL"] = "recipes.example.com"
	if _, _, err := Load(env(m), nil); err == nil {
		t.Fatal("base url without scheme accepted")
	}
}

func TestSecretFileHandling(t *testing.T) {
	f := filepath.Join(t.TempDir(), "secret")
	_ = os.WriteFile(f, []byte("from-file\n"), 0o600)
	m := minimal()
	delete(m, "OIDC_CLIENT_SECRET")
	m["OIDC_CLIENT_SECRET_FILE"] = f
	c, _, err := Load(env(m), nil)
	if err != nil || c.OIDCClientSecret != "from-file" {
		t.Fatalf("%v %+v", err, c)
	}
	m["OIDC_CLIENT_SECRET"] = "both"
	if _, _, err := Load(env(m), nil); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("both set must fail: %v", err)
	}
	m2 := minimal()
	delete(m2, "OIDC_CLIENT_SECRET")
	m2["OIDC_CLIENT_SECRET_FILE"] = "/nonexistent/secret"
	if _, _, err := Load(env(m2), nil); err == nil {
		t.Fatal("unreadable secret file")
	}
}

func TestProxiesGroupsAndUnknownVars(t *testing.T) {
	m := minimal()
	m["APP_TRUSTED_PROXIES"] = "10.0.0.0/8, 192.168.1.5 ,fd00::/8"
	m["OIDC_ALLOWED_GROUPS"] = "family, friends"
	m["OIDC_ADMIN_GROUP"] = ""
	m["APP_ENV"] = "development"
	c, warns, err := Load(env(m), []string{"APP_BASE_URL=x", "APP_TRUST_PROXY=true", "OIDC_CLIENT_SECRT=oops", "PATH=/bin"})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.TrustedProxies) != 3 || c.TrustedProxies[1].Bits() != 32 || c.OIDCAdminGroup != "" || !c.Dev() || c.LogFormat != "text" {
		t.Fatalf("%+v", c)
	}
	if len(c.OIDCAllowedGroups) != 2 || c.OIDCAllowedGroups[1] != "friends" {
		t.Fatalf("groups %v", c.OIDCAllowedGroups)
	}
	if len(warns) != 2 || !strings.Contains(warns[0], "APP_TRUST_PROXY") || !strings.Contains(warns[1], "OIDC_CLIENT_SECRT") {
		t.Fatalf("typo warnings: %v", warns)
	}
	m["APP_TRUSTED_PROXIES"] = "10.0.0.0/33"
	if _, _, err := Load(env(m), nil); err == nil {
		t.Fatal("bad CIDR")
	}
}

func TestSecretIsRedactedInLogs(t *testing.T) {
	c, _, _ := Load(env(minimal()), nil)
	s := c.LogValue().String()
	if strings.Contains(s, "sec") && !strings.Contains(s, "redacted") {
		t.Fatalf("secret leaked: %s", s)
	}
	if strings.Contains(s, "=sec ") || strings.Contains(s, "=sec]") {
		t.Fatalf("secret leaked: %s", s)
	}
}
