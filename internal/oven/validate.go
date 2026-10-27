package oven

import (
	"sort"
	"strings"

	"cooking/internal/domain"
)

// Normalize applies the silent rules: smoker => woodfire, accessory sort/dedupe, trims.
func Normalize(s *domain.OvenStage) {
	if m, ok := Lookup(s.Mode); ok && m.Woodfire == WoodForced {
		s.Woodfire = true
	}
	s.PelletNote = strings.TrimSpace(s.PelletNote)
	s.Notes = strings.TrimSpace(s.Notes)
	seen := map[string]bool{}
	acc := make([]string, 0, len(s.Accessories))
	for _, a := range s.Accessories {
		if a != "" && !seen[a] {
			seen[a] = true
			acc = append(acc, a)
		}
	}
	sort.Strings(acc)
	s.Accessories = acc
}

func iss(field, code string, kv ...any) Issue {
	p := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		p[kv[i].(string)] = kv[i+1]
	}
	if len(p) == 0 {
		p = nil
	}
	return Issue{Field: field, Code: code, Params: p}
}

// Validate interprets the table. Errors block saving; warnings never do.
func Validate(s domain.OvenStage) (errs, warns []Issue) {
	m, ok := Lookup(s.Mode)
	if !ok {
		if s.Mode == "" {
			return []Issue{iss("mode", "required")}, nil
		}
		return []Issue{iss("mode", "invalid")}, nil
	}
	// preset
	if s.Mode == domain.ModePizza {
		if s.PizzaPreset == "" {
			errs = append(errs, iss("pizza_preset", "required"))
		} else if !domain.Contains(domain.PizzaPresets, s.PizzaPreset) {
			errs = append(errs, iss("pizza_preset", "invalid"))
		}
	} else if s.PizzaPreset != "" {
		errs = append(errs, iss("pizza_preset", "forbidden", "mode", s.Mode))
	}

	// temperature
	tkind := m.Temperature.Kind
	if tkind == ByPreset {
		if s.PizzaPreset == "custom" {
			tkind = Required
		} else {
			tkind = Forbidden
		}
	}
	e, w := checkNum("temperature_c", s.TemperatureC, tkind, m.Temperature, tkind == Required && m.Temperature.Kind == ByPreset, m.Key, "c")
	errs, warns = append(errs, e...), append(warns, w...)
	e, w = checkNum("duration_seconds", s.DurationSeconds, m.Duration.Kind, m.Duration, false, m.Key, "s")
	errs, warns = append(errs, e...), append(warns, w...)

	// stage 2
	if m.Temperature2 != nil {
		e, w = checkNum("temperature2_c", s.Temperature2C, m.Temperature2.Kind, *m.Temperature2, false, m.Key, "c")
		errs, warns = append(errs, e...), append(warns, w...)
		e, w = checkNum("duration2_seconds", s.Duration2Seconds, m.Duration2.Kind, *m.Duration2, false, m.Key, "s")
		errs, warns = append(errs, e...), append(warns, w...)
	} else {
		if s.Temperature2C != nil {
			errs = append(errs, iss("temperature2_c", "forbidden", "mode", m.Key))
		}
		if s.Duration2Seconds != nil {
			errs = append(errs, iss("duration2_seconds", "forbidden", "mode", m.Key))
		}
	}

	// woodfire
	if m.Woodfire == WoodForbidden && s.Woodfire {
		errs = append(errs, iss("woodfire", "forbidden", "mode", m.Key))
	}
	if !s.Woodfire && m.Woodfire != WoodForced {
		if s.PelletBlend != "" {
			errs = append(errs, iss("pellet_blend", "forbidden", "mode", m.Key))
		}
		if s.PelletNote != "" {
			errs = append(errs, iss("pellet_note", "forbidden", "mode", m.Key))
		}
	}
	if s.PelletBlend != "" && !domain.Contains(domain.PelletBlends, s.PelletBlend) {
		errs = append(errs, iss("pellet_blend", "invalid"))
	}
	if len([]rune(s.PelletNote)) > domain.MaxPelletNote {
		errs = append(errs, iss("pellet_note", "invalid", "max", domain.MaxPelletNote))
	}

	// preheat
	if s.PreheatMinutes != nil {
		if !m.Preheat {
			errs = append(errs, iss("preheat_minutes", "forbidden", "mode", m.Key))
		} else if *s.PreheatMinutes < 0 || *s.PreheatMinutes > PreheatMinutesMax {
			errs = append(errs, iss("preheat_minutes", "out_of_range", "min", 0, "max", PreheatMinutesMax, "unit", "min"))
		}
	}
	if s.TargetCoreTempC != nil && (*s.TargetCoreTempC < CoreTempMin || *s.TargetCoreTempC > CoreTempMax) {
		errs = append(errs, iss("target_core_temp_c", "out_of_range", "min", CoreTempMin, "max", CoreTempMax, "unit", "c"))
	}
	for _, a := range s.Accessories {
		if !domain.Contains(domain.Accessories, a) {
			errs = append(errs, iss("accessories", "invalid", "value", a))
			break
		}
	}
	if len([]rune(s.Notes)) > domain.MaxStageNotes {
		errs = append(errs, iss("notes", "invalid", "max", domain.MaxStageNotes))
	}
	return errs, warns
}

// checkNum validates one numeric field against its rule.
// warnRecommended is false for preset-driven pizza temperature handled by the caller (still warns for custom).
func checkNum(field string, v *int, kind string, f Field, _ bool, mode, unit string) (errs, warns []Issue) {
	switch kind {
	case Forbidden:
		if v != nil {
			errs = append(errs, iss(field, "forbidden", "mode", mode))
		}
		return
	case Required:
		if v == nil {
			errs = append(errs, iss(field, "required"))
			return
		}
	case Optional:
		if v == nil {
			return
		}
	}
	if v == nil || f.Accepted == nil {
		return
	}
	if *v < f.Accepted[0] || *v > f.Accepted[1] {
		errs = append(errs, iss(field, "out_of_range", "min", f.Accepted[0], "max", f.Accepted[1], "unit", unit))
		return
	}
	if f.Recommended != nil && (*v < f.Recommended[0] || *v > f.Recommended[1]) {
		warns = append(warns, iss(field, "out_of_range", "min", f.Recommended[0], "max", f.Recommended[1], "mode", mode, "unit", unit))
	}
	return
}

// ValidateStages validates a list, prefixing field names with oven_stages[i]. and enforcing <=10.
func ValidateStages(stages []domain.OvenStage, prefix string) (errs, warns []Issue) {
	if prefix == "" {
		prefix = "oven_stages"
	}
	if len(stages) > domain.MaxStages {
		errs = append(errs, iss(prefix, "too_many", "max", domain.MaxStages))
	}
	for i := range stages {
		e, w := Validate(stages[i])
		p := prefix + "[" + itoa(i) + "]."
		for _, x := range e {
			x.Field = p + x.Field
			errs = append(errs, x)
		}
		for _, x := range w {
			x.Field = p + x.Field
			warns = append(warns, x)
		}
	}
	return
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
