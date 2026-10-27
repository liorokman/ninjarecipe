package i18n

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"cooking/locales"
)

func load(t *testing.T) *Catalog {
	t.Helper()
	c, err := Load(locales.FS)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestBackendCatalogParity(t *testing.T) {
	// only the backend-owned files: the UI files are owned by the other package
	c, err := Load(fstest.MapFS{"en.toml": {Data: mustRead(t, "en.toml")}, "he.toml": {Data: mustRead(t, "he.toml")}})
	if err != nil {
		t.Fatal(err)
	}
	if ps := Check(c, ""); len(ps) != 0 {
		t.Fatalf("backend catalogs: %v", ps)
	}
	for _, k := range []string{"oven.mode.gourmet_roast", "oven.preset.chicago", "oven.pellet.robust", "oven.accessory.own_pan", "oven.frame.top",
		"flash.recipe_saved", "flash.photo_failed", "issue.required", "issue.warn.temperature", "unit.pinch", "err.heic"} {
		if !c.Has("en", k) || !c.Has("he", k) {
			t.Errorf("missing %s", k)
		}
	}
}

func mustRead(t *testing.T, name string) []byte {
	b, err := locales.FS.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestFullCatalogUnion(t *testing.T) {
	c := load(t)
	if ps := Check(c, "../.."); len(ps) != 0 {
		t.Fatalf("i18ncheck problems:\n%v", ps)
	}
}

func TestTranslateAndPlurals(t *testing.T) {
	c := load(t)
	en, he := c.Localizer("en", nil), c.Localizer("he", nil)
	if en.T("oven.mode.max_roast") != "Max Roast" || he.T("oven.mode.max_roast") != "צלייה מקס (Max Roast)" {
		t.Fatal("mode labels (Hebrew with English in parentheses)")
	}
	if he.T("oven.preset.new_york") != "New York Style" || he.T("oven.preset.artisan") != "פיצה ביתית (Artisan)" {
		t.Fatal("preset labels")
	}
	if got := en.T("time.ago.week", "Count", 1); got != "1 week ago" {
		t.Fatalf("one: %q", got)
	}
	if got := en.T("time.ago.week", "Count", 3); got != "3 weeks ago" {
		t.Fatalf("other: %q", got)
	}
	if got := he.T("time.ago.week", "Count", 3); got != "לפני 3 שבועות" {
		t.Fatalf("he other: %q", got)
	}
	if he.T("time.ago.week", "Count", 1) != "לפני שבוע" || he.T("time.ago.week", "Count", 2) != "לפני שבועיים" {
		t.Fatal("he one/two")
	}
	if got := en.T("no.such.key"); got != "no.such.key" {
		t.Fatalf("missing key renders the key: %q", got)
	}
	if en.Dir() != "ltr" || he.Dir() != "rtl" || c.Localizer("fr", nil).Lang != "en" {
		t.Fatal("dir/lang")
	}
	client := c.Client("en", "oven.mode.")
	if len(client) != 8 || client["oven.mode.pizza"] != "Pizza" {
		t.Fatalf("client strings: %v", client)
	}
}

func TestFormatters(t *testing.T) {
	c := load(t)
	en, he := c.Localizer("en", nil), c.Localizer("he", nil)
	for in, want := range map[int]string{0: "0:00", 59: "0:59", 600: "10:00", 3599: "59:59", 3600: "1:00", 5400: "1:30", 36000: "10:00", -5: "0:00"} {
		if got := FmtDuration(in); got != want {
			t.Errorf("FmtDuration(%d) = %q want %q", in, got, want)
		}
	}
	if FmtTemp(220) != "220 °C" {
		t.Fatal("temp")
	}
	if got := en.DurationWords(5400); got != "1 h 30 min" {
		t.Fatalf("words: %q", got)
	}
	if got := he.DurationWords(600); got != "10 דק'" {
		t.Fatalf("words he: %q", got)
	}
	for in, want := range map[float64]string{1: "1", 0.5: "½", 1.5: "1 ½", 0.25: "¼", 0.75: "¾", 1.0 / 3: "⅓", 2.0 / 3: "⅔", 2.333: "2 ⅓", 0.1: "0.1", 2.75: "2 ¾", 1.999: "2", 12.5: "12 ½", 0.15: "0.15"} {
		if got := en.FmtQty(in); got != want {
			t.Errorf("FmtQty(%v) = %q want %q", in, got, want)
		}
	}
	if en.FmtQty(0) != "" {
		t.Fatal("zero")
	}
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	for d, want := range map[time.Duration]string{30 * time.Second: "just now", 5 * time.Minute: "5 minutes ago", 3 * time.Hour: "3 hours ago",
		48 * time.Hour: "2 days ago", 21 * 24 * time.Hour: "3 weeks ago", 100 * 24 * time.Hour: "3 months ago", 800 * 24 * time.Hour: "2 years ago"} {
		if got := en.FmtAgo(now.Add(-d), now); got != want {
			t.Errorf("FmtAgo(%v) = %q want %q", d, got, want)
		}
	}
	if got := he.FmtAgo(now.Add(-21*24*time.Hour), now); got != "לפני 3 שבועות" {
		t.Fatalf("he ago: %q", got)
	}
	d := time.Date(2026, 10, 3, 14, 5, 0, 0, time.UTC)
	if en.FmtDate(d, now) != "3 Oct" || he.FmtDate(d, now) != "3.10" || en.FmtDate(d.AddDate(-1, 0, 0), now) != "3 Oct 2025" {
		t.Fatalf("dates: %q %q %q", en.FmtDate(d, now), he.FmtDate(d, now), en.FmtDate(d.AddDate(-1, 0, 0), now))
	}
	tz, _ := time.LoadLocation("Asia/Jerusalem")
	if got := c.Localizer("en", tz).FmtDateTime(d, now); got != "3 Oct 17:05" {
		t.Fatalf("datetime in TZ: %q", got)
	}
	if en.FmtDate(time.Time{}, now) != "" || en.FmtAgo(time.Time{}, now) != "" {
		t.Fatal("zero time")
	}
}

func TestResolveLang(t *testing.T) {
	mk := func(cookie, accept string) *httptest.ResponseRecorder { return nil }
	_ = mk
	r := httptest.NewRequest("GET", "/", nil)
	if ResolveLang("he", r, "en") != "he" {
		t.Fatal("user language wins")
	}
	r.AddCookie(&http.Cookie{Name: "lang", Value: "he"})
	if ResolveLang("", r, "en") != "he" || ResolveLang("en", r, "he") != "en" {
		t.Fatal("cookie after user")
	}
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.Header.Set("Accept-Language", "he-IL,he;q=0.9,en;q=0.8")
	if ResolveLang("", r2, "en") != "he" {
		t.Fatal("accept-language he")
	}
	r2.Header.Set("Accept-Language", "fr-FR,en;q=0.5")
	if ResolveLang("", r2, "he") != "en" {
		t.Fatal("accept-language falls to supported en")
	}
	r2.Header.Set("Accept-Language", "fr-FR")
	if got := ResolveLang("", r2, "he"); got != "en" && got != "he" {
		t.Fatal("unsupported")
	}
	if ResolveLang("", httptest.NewRequest("GET", "/", nil), "he") != "he" || ResolveLang("", httptest.NewRequest("GET", "/", nil), "en") != "en" {
		t.Fatal("default")
	}
	r3 := httptest.NewRequest("GET", "/", nil)
	r3.AddCookie(&http.Cookie{Name: "lang", Value: "xx"})
	if ResolveLang("", r3, "en") != "en" {
		t.Fatal("bad cookie ignored")
	}
}

func TestCheckFindsProblems(t *testing.T) {
	c, err := Load(fstest.MapFS{
		"en.toml": {Data: []byte("[\"a.only_en\"]\nother = \"x\"\n[\"a.ph\"]\nother = \"Hi {{.Name}}\"\n[\"a.token\"]\nother = \"Your token\"\n")},
		"he.toml": {Data: []byte("[\"a.ph\"]\nother = \"שלום\"\n[\"a.token\"]\nother = \"x\"\n[\"a.only_he\"]\nother = \"y\"\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, p := range Check(c, "") {
		kinds[p.Kind] = true
	}
	for _, k := range []string{"missing-he", "missing-en", "placeholders", "forbidden-word"} {
		if !kinds[k] {
			t.Errorf("expected problem kind %s", k)
		}
	}
}
