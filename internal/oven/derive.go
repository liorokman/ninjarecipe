package oven

import "github.com/liorokman/ninjarecipe/internal/domain"

func val(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// StageSeconds = duration + duration2 (25 §5).
func StageSeconds(s domain.OvenStage) int { return val(s.DurationSeconds) + val(s.Duration2Seconds) }

// TotalSeconds sums stage times, preheat excluded.
func TotalSeconds(stages []domain.OvenStage) int {
	t := 0
	for _, s := range stages {
		t += StageSeconds(s)
	}
	return t
}

// UsesWoodfire: any stage woodfire or smoker.
func UsesWoodfire(stages []domain.OvenStage) bool {
	for _, s := range stages {
		if s.Woodfire || s.Mode == domain.ModeSmoker {
			return true
		}
	}
	return false
}

// PrimaryMode is the mode of the stage with the longest stage time (ties: lowest position).
// Returns the stage too (nil when there are none).
func PrimaryStage(stages []domain.OvenStage) *domain.OvenStage {
	var best *domain.OvenStage
	for i := range stages {
		if best == nil || StageSeconds(stages[i]) > StageSeconds(*best) {
			best = &stages[i]
		}
	}
	return best
}

// PrimaryMode returns "" when there are no stages.
func PrimaryMode(stages []domain.OvenStage) string {
	if p := PrimaryStage(stages); p != nil {
		return p.Mode
	}
	return ""
}

// FrameLevel: top for broil, else bottom.
func FrameLevel(mode string) string {
	if m, ok := Lookup(mode); ok {
		return m.FrameLevel
	}
	return "bottom"
}

// Preheat reports whether the mode preheats.
func Preheat(mode string) bool {
	m, ok := Lookup(mode)
	return ok && m.Preheat
}

// MaxTemp returns the highest temperature in a stage (both phases).
func MaxTemp(s domain.OvenStage) int {
	a, b := val(s.TemperatureC), val(s.Temperature2C)
	if a > b {
		return a
	}
	return b
}
