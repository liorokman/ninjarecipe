package web

import (
	"fmt"
	"html/template"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/liorokman/ninjarecipe/internal/domain"
	"github.com/liorokman/ninjarecipe/internal/i18n"
)

// FuncCtx is the request-scoped data template funcs close over.
type FuncCtx struct {
	Loc          *i18n.Loc
	AssetVersion string
	Now          time.Time
}

// Funcs builds the template FuncMap of contract 4.2 bound to a request.
func Funcs(fc FuncCtx) template.FuncMap {
	loc := fc.Loc
	return template.FuncMap{
		"T":   func(id string, kv ...any) string { return loc.T(id, kv...) },
		"url": func(name string, args ...any) (string, error) { return buildURL(name, args) },
		"urlq": func(name string, args ...any) (s string, err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("%v", r)
				}
			}()
			return URLQ(name, args...), nil
		},
		"asset":            func(p string) string { return "/static/" + fc.AssetVersion + "/" + strings.TrimPrefix(p, "/") },
		"fmtDuration":      func(s any) string { return i18n.FmtDuration(toInt(s)) },
		"fmtDurationWords": func(s any) string { return loc.DurationWords(toInt(s)) },
		"fmtTemp":          func(t any) string { return i18n.FmtTemp(toInt(t)) },
		"fmtQty": func(a any, u ...any) string {
			unit := ""
			if len(u) > 0 {
				unit, _ = u[0].(string)
			}
			f, ok := toFloat(a)
			if !ok {
				return ""
			}
			return loc.FmtQtyUnit(f, unit)
		},
		"fmtUnit": func(a any, unit string) string {
			f, ok := toFloat(a)
			if !ok {
				return unit
			}
			_, u := i18n.ConvertUnit(f, unit)
			return u
		},
		"deref": deref,
		// the fixed optional throw-away reasons of the inbox dialog (30 §5a), in display order
		"rejectReasons": func() []domain.RejectReason { return domain.RejectReasons },
		"fmtDate":       func(t any) string { return loc.FmtDate(toTime(t), fc.Now) },
		"fmtAgo":        func(t any) string { return loc.FmtAgo(toTime(t), fc.Now) },
		"fmtDateTime":   func(t any) string { return loc.FmtDateTime(toTime(t), fc.Now) },
		"fieldID":       FieldID,
		"secH":          func(s any) int { return toInt(s) / 3600 },
		"secM":          func(s any) int { return toInt(s) % 3600 / 60 },
		"secS":          func(s any) int { return toInt(s) % 60 },
		"dirFor":        DirFor,
		"langFor":       LangFor,
		"seq": func(from, to int) []int {
			var out []int
			for i := from; i <= to; i++ {
				out = append(out, i)
			}
			return out
		},
		"dict": func(kv ...any) (map[string]any, error) {
			if len(kv)%2 != 0 {
				return nil, fmt.Errorf("dict: odd argument count")
			}
			m := map[string]any{}
			for i := 0; i < len(kv); i += 2 {
				k, ok := kv[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict: key %v is not a string", kv[i])
				}
				m[k] = kv[i+1]
			}
			return m, nil
		},
		"join": func(l []string, sep string) string { return strings.Join(l, sep) },
		"contains": func(l []string, s string) bool {
			for _, v := range l {
				if v == s {
					return true
				}
			}
			return false
		},
		"hasKey": func(m map[string][]string, k string) bool { _, ok := m[k]; return ok },
		"host":   HostOf,
		"stars": func(n any) []bool {
			v := toInt(n)
			out := make([]bool, 5)
			for i := range out {
				out[i] = i < v
			}
			return out
		},
		"add":  func(a, b int) int { return a + b },
		"sub":  func(a, b int) int { return a - b },
		"list": func(v ...any) []any { return v },
	}
}

func toInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int32:
		return int(x)
	case int64:
		return int(x)
	case float64:
		return int(x)
	case *int:
		if x != nil {
			return *x
		}
	case *float64:
		if x != nil {
			return int(*x)
		}
	}
	return 0
}

func toTime(v any) time.Time {
	switch x := v.(type) {
	case time.Time:
		return x
	case *time.Time:
		if x != nil {
			return *x
		}
	}
	return time.Time{}
}

// FieldID converts a form field name into a DOM id (D23).
func FieldID(name string) string {
	if !strings.HasPrefix(name, "stage[") {
		return "f-" + name
	}
	r := strings.NewReplacer("[", "-", "]", "", ".", "-")
	return r.Replace(name)
}

// HostOf returns the host of a URL ("" on error).
func HostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Hostname(), "www.")
}

// DirFor maps a recipe language to a dir attribute.
func DirFor(lang string) string {
	switch lang {
	case "he":
		return "rtl"
	case "en":
		return "ltr"
	}
	return "auto"
}

// LangFor maps a recipe language to a lang attribute ("" for und).
func LangFor(lang string) string {
	if lang == "en" || lang == "he" {
		return lang
	}
	return ""
}

// HasHebrew reports whether s contains Hebrew letters.
func HasHebrew(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Hebrew, r) {
			return true
		}
	}
	return false
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case *float64:
		if x != nil {
			return *x, true
		}
	case float64:
		return x, true
	case int:
		return float64(x), true
	}
	return 0, false
}

// deref returns the value behind a pointer (nil -> zero value).
func deref(v any) any {
	switch x := v.(type) {
	case *int:
		if x != nil {
			return *x
		}
		return 0
	case *int64:
		if x != nil {
			return *x
		}
		return int64(0)
	case *float64:
		if x != nil {
			return *x
		}
		return 0.0
	case *string:
		if x != nil {
			return *x
		}
		return ""
	}
	return v
}
