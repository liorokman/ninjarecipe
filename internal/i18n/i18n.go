// Package i18n loads the EN/HE catalogs and offers per-request localizers and formatting helpers.
package i18n

import (
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

// Files loaded per language (D14): A owns <lang>.toml, B owns ui.<lang>.toml. Missing files are skipped.
var Files = []string{"%s.toml", "ui.%s.toml"}

// Langs are the supported UI languages.
var Langs = []string{"en", "he"}

var matcher = language.NewMatcher([]language.Tag{language.English, language.Hebrew})

// Catalog holds the go-i18n bundle plus raw messages (for client strings and key checks).
type Catalog struct {
	bundle *goi18n.Bundle
	raw    map[string]map[string]map[string]string // lang -> id -> form -> text
	warned sync.Map
}

// Load parses every catalog file present in fsys.
func Load(fsys fs.FS) (*Catalog, error) {
	c := &Catalog{bundle: goi18n.NewBundle(language.English), raw: map[string]map[string]map[string]string{}}
	c.bundle.RegisterUnmarshalFunc("toml", toml.Unmarshal)
	for _, lang := range Langs {
		c.raw[lang] = map[string]map[string]string{}
		for _, pat := range Files {
			name := fmt.Sprintf(pat, lang)
			data, err := fs.ReadFile(fsys, name)
			if err != nil {
				continue
			}
			if _, err := c.bundle.ParseMessageFileBytes(data, lang+".toml"); err != nil {
				return nil, fmt.Errorf("i18n: %s: %w", name, err)
			}
			var m map[string]any
			if err := toml.Unmarshal(data, &m); err != nil {
				return nil, fmt.Errorf("i18n: %s: %w", name, err)
			}
			for id, v := range m {
				forms := map[string]string{}
				switch t := v.(type) {
				case string:
					forms["other"] = t
				case map[string]any:
					for k, x := range t {
						if s, ok := x.(string); ok {
							forms[k] = s
						}
					}
				}
				c.raw[lang][id] = forms
			}
		}
	}
	return c, nil
}

// Keys returns the sorted message ids of a language.
func (c *Catalog) Keys(lang string) []string {
	var ks []string
	for k := range c.raw[lang] {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// Has reports whether the id exists in the language.
func (c *Catalog) Has(lang, id string) bool { _, ok := c.raw[lang][id]; return ok }

// Forms returns the raw plural forms of an id.
func (c *Catalog) Forms(lang, id string) map[string]string { return c.raw[lang][id] }

// Client returns the "other" strings of every key with one of the prefixes (for #client-i18n).
func (c *Catalog) Client(lang string, prefixes ...string) map[string]string {
	out := map[string]string{}
	for id, forms := range c.raw[lang] {
		for _, p := range prefixes {
			if strings.HasPrefix(id, p) {
				out[id] = forms["other"]
				break
			}
		}
	}
	return out
}

// Loc is a request-scoped localizer.
type Loc struct {
	Lang string
	TZ   *time.Location
	c    *Catalog
	l    *goi18n.Localizer
}

// Localizer returns a localizer for lang ("en" or "he"; anything else = "en") in tz.
func (c *Catalog) Localizer(lang string, tz *time.Location) *Loc {
	if lang != "he" {
		lang = "en"
	}
	if tz == nil {
		tz = time.UTC
	}
	return &Loc{Lang: lang, TZ: tz, c: c, l: goi18n.NewLocalizer(c.bundle, lang, "en")}
}

// Dir returns "rtl" for Hebrew.
func (l *Loc) Dir() string {
	if l.Lang == "he" {
		return "rtl"
	}
	return "ltr"
}

// T translates id. kv are alternating name,value pairs; a "Count" pair selects the plural form.
// A missing key renders the key itself and is logged once.
func (l *Loc) T(id string, kv ...any) string {
	cfg := &goi18n.LocalizeConfig{MessageID: id}
	if len(kv) > 0 {
		data := map[string]any{}
		for i := 0; i+1 < len(kv); i += 2 {
			if k, ok := kv[i].(string); ok {
				data[k] = kv[i+1]
			}
		}
		cfg.TemplateData = data
		if n, ok := data["Count"]; ok {
			cfg.PluralCount = n
		}
	}
	s, err := l.l.Localize(cfg)
	if err != nil || s == "" {
		if _, seen := l.c.warned.LoadOrStore(l.Lang+id, true); !seen {
			slog.Warn("i18n: missing key", "lang", l.Lang, "key", id)
		}
		return id
	}
	return s
}

// ResolveLang implements the order of 20 §10: user, cookie, Accept-Language, default.
func ResolveLang(userLang string, r *http.Request, def string) string {
	if userLang == "en" || userLang == "he" {
		return userLang
	}
	if ck, err := r.Cookie("lang"); err == nil && (ck.Value == "en" || ck.Value == "he") {
		return ck.Value
	}
	if al := r.Header.Get("Accept-Language"); al != "" {
		tag, _, _ := matcher.Match(mustParse(al))
		if b, _ := tag.Base(); b.String() == "he" {
			return "he"
		} else if b.String() == "en" {
			return "en"
		}
	}
	if def == "he" {
		return "he"
	}
	return "en"
}

func mustParse(al string) language.Tag {
	tags, _, err := language.ParseAcceptLanguage(al)
	if err != nil || len(tags) == 0 {
		return language.English
	}
	return tags[0]
}

// ---- formatting helpers ----

// FmtDuration: m:ss below 1 h, h:mm from 1 h.
func FmtDuration(sec int) string {
	if sec < 0 {
		sec = 0
	}
	if sec < 3600 {
		return fmt.Sprintf("%d:%02d", sec/60, sec%60)
	}
	return fmt.Sprintf("%d:%02d", sec/3600, (sec%3600)/60)
}

// DurationWords: "1 h 30 min" using time.h / time.min / time.s keys.
func (l *Loc) DurationWords(sec int) string {
	if sec <= 0 {
		return "0 " + l.T("time.min")
	}
	h, m, s := sec/3600, (sec%3600)/60, sec%60
	var parts []string
	if h > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", h, l.T("time.h")))
	}
	if m > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", m, l.T("time.min")))
	}
	if s > 0 && h == 0 {
		parts = append(parts, fmt.Sprintf("%d %s", s, l.T("time.s")))
	}
	return strings.Join(parts, " ")
}

// FmtTemp: "220 °C".
func FmtTemp(c int) string { return fmt.Sprintf("%d °C", c) }

var fractions = []struct {
	v float64
	s string
}{{0.25, "¼"}, {1.0 / 3, "⅓"}, {0.5, "½"}, {2.0 / 3, "⅔"}, {0.75, "¾"}}

// FmtQty formats the number part of an ingredient amount: fractions and locale decimal separator.
func (l *Loc) FmtQty(amount float64) string {
	if amount <= 0 {
		return ""
	}
	whole := math.Floor(amount + 1e-9)
	frac := amount - whole
	if frac < 0.02 {
		return fmt.Sprintf("%d", int(whole))
	}
	if frac > 0.98 {
		return fmt.Sprintf("%d", int(whole)+1)
	}
	for _, f := range fractions {
		if math.Abs(frac-f.v) < 0.02 {
			if whole == 0 {
				return f.s
			}
			return fmt.Sprintf("%d %s", int(whole), f.s)
		}
	}
	s := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", amount), "0"), ".")
	if l.Lang == "he" {
		return s
	}
	return s
}

// FmtDate: "3 Oct" / "3.10" style localized date (year added when not the current year).
func (l *Loc) FmtDate(t time.Time, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	t = t.In(l.TZ)
	if l.Lang == "he" {
		if t.Year() != now.In(l.TZ).Year() {
			return t.Format("2.1.2006")
		}
		return t.Format("2.1")
	}
	if t.Year() != now.In(l.TZ).Year() {
		return t.Format("2 Jan 2006")
	}
	return t.Format("2 Jan")
}

// FmtDateTime: date plus HH:MM.
func (l *Loc) FmtDateTime(t time.Time, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	return l.FmtDate(t, now) + " " + t.In(l.TZ).Format("15:04")
}

// FmtAgo: "3 weeks ago" with CLDR plurals from time.ago.* keys.
func (l *Loc) FmtAgo(t time.Time, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return l.T("time.just_now")
	case d < time.Hour:
		return l.T("time.ago.minute", "Count", int(d/time.Minute))
	case d < 24*time.Hour:
		return l.T("time.ago.hour", "Count", int(d/time.Hour))
	case d < 7*24*time.Hour:
		return l.T("time.ago.day", "Count", int(d/(24*time.Hour)))
	case d < 30*24*time.Hour:
		return l.T("time.ago.week", "Count", int(d/(7*24*time.Hour)))
	case d < 365*24*time.Hour:
		return l.T("time.ago.month", "Count", int(d/(30*24*time.Hour)))
	}
	return l.T("time.ago.year", "Count", int(d/(365*24*time.Hour)))
}

// ConvertUnit applies the g->kg / ml->l conversion at >= 1000 and returns the new amount and unit key.
func ConvertUnit(amount float64, unit string) (float64, string) {
	switch unit {
	case "g":
		if amount >= 1000 {
			return amount / 1000, "kg"
		}
	case "ml":
		if amount >= 1000 {
			return amount / 1000, "l"
		}
	}
	return amount, unit
}

// FmtQtyUnit formats the number text of an ingredient amount for its unit (after conversion): decimals (max 2)
// for g/kg/ml/l, common fractions for everything else.
func (l *Loc) FmtQtyUnit(amount float64, unit string) string {
	amount, unit = ConvertUnit(amount, unit)
	switch unit {
	case "g", "kg", "ml", "l":
		if amount <= 0 {
			return ""
		}
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", amount), "0"), ".")
	}
	return l.FmtQty(amount)
}
