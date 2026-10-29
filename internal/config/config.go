// Package config parses the environment into a typed struct (20 §12). Invalid or missing required values are
// reported together; the caller exits with code 2.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Config is the effective configuration.
type Config struct {
	BaseURL        string
	ListenAddr     string
	DataDir        string
	DefaultLang    string
	TrustedProxies []netip.Prefix
	Env            string
	TemplatesDir   string

	OIDCIssuer        string
	OIDCClientID      string
	OIDCClientSecret  string
	OIDCScopes        []string
	OIDCGroupsClaim   string
	OIDCAllowedGroups []string
	OIDCAdminGroup    string

	SessionIdleTTL time.Duration
	SessionMaxTTL  time.Duration

	MaxImageMB          int
	MaxImagesPerRecipe  int
	ImageMaxPixels      int64
	ImageWorkers        int
	APIMaxBodyMB        int
	APIRateLimitPerMin  int
	ImageFetchTimeout   time.Duration
	ImageFetchAllowHTTP bool

	LogLevel  string
	LogFormat string

	BackupAuto bool
	BackupAt   string // HH:MM
	BackupKeep int

	Categorizer          string // noop|localai
	LocalAIURL           string
	LocalAIKey           string
	LocalAIModel         string
	CategorizerThreshold float64
	CategorizerTimeout   time.Duration
	TZ                   *time.Location
	TZName               string
}

// Dev reports development mode.
func (c *Config) Dev() bool { return c.Env == "development" }

// Secure reports whether APP_BASE_URL is https.
func (c *Config) Secure() bool { return strings.HasPrefix(c.BaseURL, "https://") }

// Lookup abstracts os.LookupEnv.
type Lookup func(string) (string, bool)

// OSLookup reads the process environment.
func OSLookup(k string) (string, bool) { return os.LookupEnv(k) }

// Load parses configuration. environ lists "K=V" pairs for typo detection (may be nil).
func Load(get Lookup, environ []string) (*Config, []string, error) {
	var errs []error
	var warns []string
	str := func(k, def string) string {
		if v, ok := get(k); ok && v != "" {
			return v
		}
		return def
	}
	bad := func(k, msg string) { errs = append(errs, fmt.Errorf("%s: %s", k, msg)) }
	integer := func(k string, def, min int) int {
		v := str(k, "")
		if v == "" {
			return def
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < min {
			bad(k, fmt.Sprintf("must be an integer >= %d", min))
			return def
		}
		return n
	}
	boolean := func(k string, def bool) bool {
		v := str(k, "")
		if v == "" {
			return def
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			bad(k, "must be true or false")
			return def
		}
		return b
	}
	dur := func(k, def string) time.Duration {
		d, err := time.ParseDuration(str(k, def))
		if err != nil || d <= 0 {
			bad(k, "must be a positive duration like 720h")
			d, _ = time.ParseDuration(def)
		}
		return d
	}
	list := func(k string) []string {
		var out []string
		for _, p := range strings.Split(str(k, ""), ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}

	c := &Config{}
	c.BaseURL = strings.TrimRight(str("APP_BASE_URL", ""), "/")
	if c.BaseURL == "" {
		bad("APP_BASE_URL", "is required")
	} else if u, err := url.Parse(c.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		bad("APP_BASE_URL", "must be an http(s) URL like https://recipes.example.com")
	}
	c.ListenAddr = str("APP_LISTEN_ADDR", ":8080")
	c.DataDir = str("APP_DATA_DIR", "/data")
	c.DefaultLang = str("APP_DEFAULT_LANG", "en")
	if c.DefaultLang != "en" && c.DefaultLang != "he" {
		bad("APP_DEFAULT_LANG", "must be en or he")
	}
	for _, p := range list("APP_TRUSTED_PROXIES") {
		if !strings.Contains(p, "/") {
			a, err := netip.ParseAddr(p)
			if err != nil {
				bad("APP_TRUSTED_PROXIES", "invalid CIDR "+p)
				continue
			}
			c.TrustedProxies = append(c.TrustedProxies, netip.PrefixFrom(a, a.BitLen()))
			continue
		}
		pf, err := netip.ParsePrefix(p)
		if err != nil {
			bad("APP_TRUSTED_PROXIES", "invalid CIDR "+p)
			continue
		}
		c.TrustedProxies = append(c.TrustedProxies, pf)
	}
	c.Env = str("APP_ENV", "production")
	if c.Env != "production" && c.Env != "development" {
		bad("APP_ENV", "must be production or development")
	}
	c.TemplatesDir = str("APP_TEMPLATES_DIR", "")

	c.OIDCIssuer = str("OIDC_ISSUER_URL", "")
	if c.OIDCIssuer == "" {
		bad("OIDC_ISSUER_URL", "is required")
	}
	c.OIDCClientID = str("OIDC_CLIENT_ID", "")
	if c.OIDCClientID == "" {
		bad("OIDC_CLIENT_ID", "is required")
	}
	sec, hasSec := get("OIDC_CLIENT_SECRET")
	secFile, hasFile := get("OIDC_CLIENT_SECRET_FILE")
	hasSec, hasFile = hasSec && sec != "", hasFile && secFile != ""
	switch {
	case hasSec && hasFile:
		bad("OIDC_CLIENT_SECRET", "set both OIDC_CLIENT_SECRET and OIDC_CLIENT_SECRET_FILE; use exactly one")
	case hasSec:
		c.OIDCClientSecret = sec
	case hasFile:
		b, err := os.ReadFile(secFile)
		if err != nil {
			bad("OIDC_CLIENT_SECRET_FILE", "cannot read file: "+err.Error())
		}
		c.OIDCClientSecret = strings.TrimSpace(string(b))
	default:
		bad("OIDC_CLIENT_SECRET", "OIDC_CLIENT_SECRET or OIDC_CLIENT_SECRET_FILE is required")
	}
	c.OIDCScopes = strings.Fields(str("OIDC_SCOPES", "openid email profile"))
	c.OIDCGroupsClaim = str("OIDC_GROUPS_CLAIM", "groups")
	c.OIDCAllowedGroups = list("OIDC_ALLOWED_GROUPS")
	if v, ok := get("OIDC_ADMIN_GROUP"); ok {
		c.OIDCAdminGroup = strings.TrimSpace(v)
	} else {
		c.OIDCAdminGroup = "recipes-admins"
	}

	c.SessionIdleTTL = dur("SESSION_IDLE_TTL", "720h")
	c.SessionMaxTTL = dur("SESSION_MAX_TTL", "8760h")
	c.MaxImageMB = integer("MAX_IMAGE_MB", 15, 1)
	c.MaxImagesPerRecipe = integer("MAX_IMAGES_PER_RECIPE", 10, 1)
	c.ImageMaxPixels = int64(integer("IMAGE_MAX_PIXELS", 50_000_000, 1))
	c.ImageWorkers = integer("IMAGE_WORKERS", 1, 1)
	c.APIMaxBodyMB = integer("API_MAX_BODY_MB", 40, 1)
	c.APIRateLimitPerMin = integer("API_RATE_LIMIT_PER_MIN", 60, 1)
	c.ImageFetchTimeout = dur("IMAGE_FETCH_TIMEOUT", "15s")
	c.ImageFetchAllowHTTP = boolean("IMAGE_FETCH_ALLOW_HTTP", false)

	c.LogLevel = str("LOG_LEVEL", "info")
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		bad("LOG_LEVEL", "must be debug|info|warn|error")
	}
	c.LogFormat = str("LOG_FORMAT", "json")
	if c.LogFormat != "json" && c.LogFormat != "text" {
		bad("LOG_FORMAT", "must be json or text")
	}
	if c.Dev() && str("LOG_FORMAT", "") == "" {
		c.LogFormat = "text"
	}
	c.BackupAuto = boolean("BACKUP_AUTO", true)
	c.BackupAt = str("BACKUP_AT", "03:30")
	if _, err := time.Parse("15:04", c.BackupAt); err != nil {
		bad("BACKUP_AT", "must be HH:MM")
	}
	c.BackupKeep = integer("BACKUP_KEEP", 7, 1)
	c.Categorizer = str("CATEGORIZER", "noop")
	if c.Categorizer != "noop" && c.Categorizer != "localai" {
		bad("CATEGORIZER", "must be noop or localai")
	}
	// LOCALAI_* are defaults for the admin page (70 §4.10); the stored admin settings override them.
	c.LocalAIURL = strings.TrimRight(str("LOCALAI_URL", ""), "/")
	if c.LocalAIURL != "" || c.Categorizer == "localai" {
		if u, err := url.Parse(c.LocalAIURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			bad("LOCALAI_URL", "must be an http(s) URL (required with CATEGORIZER=localai)")
		}
	}
	keyFile, hasKeyFile := get("LOCALAI_API_KEY_FILE")
	switch key, hasKey := get("LOCALAI_API_KEY"); {
	case hasKey && key != "" && hasKeyFile && keyFile != "":
		bad("LOCALAI_API_KEY", "set both LOCALAI_API_KEY and LOCALAI_API_KEY_FILE; use exactly one")
	case hasKey && key != "":
		c.LocalAIKey = key
	case hasKeyFile && keyFile != "":
		b, err := os.ReadFile(keyFile)
		if err != nil {
			bad("LOCALAI_API_KEY_FILE", "cannot read file: "+err.Error())
		}
		c.LocalAIKey = strings.TrimSpace(string(b))
	}
	c.LocalAIModel = str("LOCALAI_MODEL", "laya-llama-cpp")
	c.CategorizerThreshold = 0.5
	if v := str("CATEGORIZER_THRESHOLD", ""); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f <= 0 || f >= 1 {
			bad("CATEGORIZER_THRESHOLD", "must be a number between 0 and 1 (exclusive)")
		} else {
			c.CategorizerThreshold = f
		}
	}
	c.CategorizerTimeout = dur("CATEGORIZER_TIMEOUT", "60s")
	c.TZName = str("TZ", "UTC")
	loc, err := time.LoadLocation(c.TZName)
	if err != nil {
		bad("TZ", "unknown time zone "+c.TZName)
		loc = time.UTC
	}
	c.TZ = loc

	// unknown APP_*/OIDC_* variables (typos)
	known := map[string]bool{}
	for _, k := range []string{"APP_BASE_URL", "APP_LISTEN_ADDR", "APP_DATA_DIR", "APP_DEFAULT_LANG", "APP_TRUSTED_PROXIES", "APP_ENV",
		"APP_TEMPLATES_DIR", "OIDC_ISSUER_URL", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET", "OIDC_CLIENT_SECRET_FILE", "OIDC_SCOPES",
		"OIDC_GROUPS_CLAIM", "OIDC_ALLOWED_GROUPS", "OIDC_ADMIN_GROUP"} {
		known[k] = true
	}
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if (strings.HasPrefix(k, "APP_") || strings.HasPrefix(k, "OIDC_")) && !known[k] {
			warns = append(warns, "unknown environment variable "+k+" (typo?)")
		}
	}
	sort.Strings(warns)
	if len(errs) > 0 {
		return nil, warns, errors.Join(errs...)
	}
	return c, warns, nil
}

// LogValue implements slog.LogValuer: the effective non-secret config.
func (c *Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("base_url", c.BaseURL), slog.String("listen", c.ListenAddr), slog.String("data_dir", c.DataDir),
		slog.String("env", c.Env), slog.String("default_lang", c.DefaultLang), slog.String("oidc_issuer", c.OIDCIssuer),
		slog.String("oidc_client_id", c.OIDCClientID), slog.String("oidc_client_secret", "[redacted]"),
		slog.Any("allowed_groups", c.OIDCAllowedGroups), slog.String("admin_group", c.OIDCAdminGroup),
		slog.Duration("session_idle", c.SessionIdleTTL), slog.Duration("session_max", c.SessionMaxTTL),
		slog.Int("max_image_mb", c.MaxImageMB), slog.Int("image_workers", c.ImageWorkers), slog.String("tz", c.TZName),
		slog.Bool("backup_auto", c.BackupAuto), slog.String("backup_at", c.BackupAt), slog.Int("backup_keep", c.BackupKeep),
	)
}
