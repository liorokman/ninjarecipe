package categorize

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fake(t *testing.T, status int, probs map[string]float64, got *decisionRequest, auth *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/decisions" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if auth != nil {
			*auth = r.Header.Get("Authorization")
		}
		var req decisionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if got != nil {
			*got = req
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
			return
		}
		var ans []decisionAnswer
		for _, q := range req.Questions {
			ans = append(ans, decisionAnswer{Name: q.Name, Type: "predicate", Probability: probs[q.Name]})
		}
		_ = json.NewEncoder(w).Encode(decisionResponse{Model: req.Model, Answers: ans})
	}))
}

func TestLocalAISuggest(t *testing.T) {
	var req decisionRequest
	var auth string
	srv := fake(t, 200, map[string]float64{"fish_seafood": 0.93, "pizza": 0.2, "vegetables": 0.6, "other": 0.99}, &req, &auth)
	defer srv.Close()
	l := &LocalAI{BaseURL: srv.URL + "/", APIKey: "k", Model: "m", Threshold: 0.5}
	s, err := l.Suggest(context.Background(), RecipeSnapshot{Title: "Smoked salmon", Ingredients: []string{"salmon", "dill"}},
		[]CategoryInfo{{Slug: "pizza", Name: "Pizza"}, {Slug: "vegetables", Name: "Vegetables"}, {Slug: "fish_seafood", Name: "Fish & Seafood", Description: "anything from the sea"}, {Slug: "other", Name: "Other"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.Categories, ","); got != "fish_seafood,vegetables" {
		t.Fatalf("categories = %q (strongest first, below-threshold and 'other' excluded)", got)
	}
	if s.Confidence != 0.93 || s.Model != "m" {
		t.Fatalf("suggestion %+v", s)
	}
	if auth != "Bearer k" || req.Model != "m" || !strings.Contains(req.Input, "Smoked salmon") || !strings.Contains(req.Input, "dill") {
		t.Fatalf("request: auth=%q %+v", auth, req)
	}
	if len(req.Questions) != 3 || req.Questions[0].Type != "predicate" {
		t.Fatalf("questions %+v", req.Questions)
	}
	if q := req.Questions[2]; q.Name != "fish_seafood" || !strings.Contains(q.Instructions, "Fish & Seafood") || !strings.HasSuffix(q.Instructions, ": anything from the sea") {
		t.Fatalf("instructions must carry the name and the admin's description: %+v", q)
	}
}

func TestLocalAIErrors(t *testing.T) {
	srv := fake(t, 400, nil, nil, nil)
	defer srv.Close()
	l := &LocalAI{BaseURL: srv.URL, Model: "m"}
	if _, err := l.Suggest(context.Background(), RecipeSnapshot{Title: "x"}, []CategoryInfo{{Slug: "pizza"}}, nil); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want error carrying the server message, got %v", err)
	}
	// nothing to ask: no request at all
	l = &LocalAI{BaseURL: "http://127.0.0.1:1"}
	if s, err := l.Suggest(context.Background(), RecipeSnapshot{Title: "x"}, []CategoryInfo{{Slug: "other"}}, nil); err != nil || len(s.Categories) != 0 {
		t.Fatalf("got %+v, %v", s, err)
	}
}

func TestSnapshotTextIsCapped(t *testing.T) {
	r := RecipeSnapshot{Title: "t", Steps: []string{strings.Repeat("é", 20000)}}
	if got := snapshotText(r); len(got) > maxInput || !strings.HasPrefix(got, "t") {
		t.Fatalf("len %d", len(got))
	}
}
