package categorize

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// maxInput caps the text sent to the model (the decisions endpoint accepts at most 64 KiB).
const maxInput = 6000

// LocalAI asks a LocalAI decision model (POST /v1/decisions, typed answers, nothing to parse) one yes/no
// question per known category and keeps the categories whose probability reaches Threshold.
type LocalAI struct {
	BaseURL   string
	APIKey    string
	Model     string
	Threshold float64 // 0..1, default 0.5
	HTTP      *http.Client
}

type decisionQuestion struct {
	Type         string `json:"type"`
	Name         string `json:"name"`
	Instructions string `json:"instructions"`
}

type decisionRequest struct {
	Model     string             `json:"model"`
	Input     string             `json:"input"`
	Questions []decisionQuestion `json:"questions"`
}

type decisionAnswer struct {
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	Probability float64 `json:"probability"`
}

type decisionResponse struct {
	Model   string           `json:"model"`
	Answers []decisionAnswer `json:"answers"`
}

// Suggest implements Categorizer. Only categories are suggested (decisions are typed over a fixed set);
// tags are left to people. The "other" category is a catch-all and is never asked about.
func (l *LocalAI) Suggest(ctx context.Context, r RecipeSnapshot, knownCategories []CategoryInfo, _ []string) (Suggestion, error) {
	var qs []decisionQuestion
	for _, c := range knownCategories {
		if c.Slug == "other" {
			continue
		}
		name := c.Name
		if name == "" {
			name = strings.ReplaceAll(c.Slug, "_", " ")
		}
		ins := fmt.Sprintf("This recipe belongs in the %q category of a family cookbook", name)
		if c.Description != "" {
			ins += ": " + c.Description
		}
		qs = append(qs, decisionQuestion{Type: "predicate", Name: c.Slug, Instructions: ins})
	}
	if len(qs) == 0 {
		return Suggestion{}, nil
	}
	body, err := json.Marshal(decisionRequest{Model: l.Model, Input: snapshotText(r), Questions: qs})
	if err != nil {
		return Suggestion{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(l.BaseURL, "/")+"/v1/decisions", bytes.NewReader(body))
	if err != nil {
		return Suggestion{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if l.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+l.APIKey)
	}
	hc := l.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Suggestion{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Suggestion{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Suggestion{}, fmt.Errorf("localai decisions: %s: %s", resp.Status, errorMessage(raw))
	}
	var out decisionResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return Suggestion{}, fmt.Errorf("localai decisions: bad response: %w", err)
	}
	th := l.Threshold
	if th <= 0 || th >= 1 {
		th = 0.5
	}
	known := map[string]bool{}
	for _, q := range qs {
		known[q.Name] = true
	}
	s := Suggestion{Model: out.Model}
	if s.Model == "" {
		s.Model = l.Model
	}
	type scored struct {
		slug string
		p    float64
	}
	var hits []scored
	for _, a := range out.Answers {
		if a.Type == "refusal" {
			return Suggestion{}, fmt.Errorf("localai decisions: model refused")
		}
		if known[a.Name] && a.Probability >= th {
			hits = append(hits, scored{a.Name, a.Probability})
		}
	}
	// strongest first, so a caller that must cap the number of categories keeps the best ones
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].p > hits[j].p })
	for _, h := range hits {
		s.Categories = append(s.Categories, h.slug)
	}
	if len(hits) > 0 {
		s.Confidence = hits[0].p
	}
	return s, nil
}

func snapshotText(r RecipeSnapshot) string {
	var b strings.Builder
	b.WriteString(r.Title)
	if r.Description != "" {
		b.WriteString("\n" + r.Description)
	}
	if len(r.Ingredients) > 0 {
		b.WriteString("\nIngredients: " + strings.Join(r.Ingredients, "; "))
	}
	if len(r.OvenModes) > 0 {
		b.WriteString("\nOven modes: " + strings.Join(r.OvenModes, ", "))
	}
	if len(r.Steps) > 0 {
		b.WriteString("\nSteps: " + strings.Join(r.Steps, " "))
	}
	s := b.String()
	if len(s) > maxInput {
		s = strings.ToValidUTF8(s[:maxInput], "")
	}
	return s
}

func errorMessage(raw []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
		msg = e.Error.Message
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return msg
}
