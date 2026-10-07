package router

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// testCriteria mirrors Config.Tiers with criteria only.
var testCriteria = map[Tier]string{
	TierFast:        "quick edits, lookups, small answers",
	TierBalanced:    "regular coding work",
	TierPerformance: "hard reasoning, planning, big refactors",
}

func testConfig(classifier string) Config {
	return Config{
		Classifier:        classifier,
		ClassifyTimeoutMs: 200,
		Tiers: map[Tier]TierCfg{
			TierFast:        {Criteria: testCriteria[TierFast]},
			TierBalanced:    {Criteria: testCriteria[TierBalanced]},
			TierPerformance: {Criteria: testCriteria[TierPerformance]},
		},
	}
}

// askFunc adapts a plain function to the ask callback.
type askFunc func(ctx context.Context, model, body string) (string, error)

func (f askFunc) ask(ctx context.Context, model, body string) (string, error) {
	return f(ctx, model, body)
}

func TestJevClassify(t *testing.T) {
	var gotModel, gotBody string
	f := askFunc(func(_ context.Context, model, body string) (string, error) {
		gotModel, gotBody = model, body
		return `{"answers":{"tier":{"choice":"balanced","confidence":0.83},"dissatisfied":{"noul":0.2}}}`, nil
	})
	cfg := testConfig("typesafe/jev-latest")
	v, err := NewClassifier(cfg, f.ask).Classify(context.Background(), Question{
		Message:      "fix the flaky test",
		PreviousTier: TierBalanced,
		Agent:        "main",
		Criteria:     testCriteria,
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.Tier != TierBalanced || v.TierConfidence != 0.83 || v.Dissatisfied != 0.2 {
		t.Fatalf("verdict: %+v", v)
	}
	if gotModel != "typesafe/jev-latest" {
		t.Fatalf("model %q", gotModel)
	}

	// The request body §5.3 requires: previous_tier in state, and the
	// "carries on" sentence in the tier instructions.
	if !strings.Contains(gotBody, `"previous_tier":"balanced"`) {
		t.Fatalf("no previous_tier in body: %s", gotBody)
	}
	if !strings.Contains(gotBody, "carries on") {
		t.Fatalf("no carries-on sentence in body: %s", gotBody)
	}
	// State carries message and agent; criteria map is keyed by tier.
	var req struct {
		Model     string            `json:"model"`
		State     map[string]string `json:"state"`
		Questions map[string]struct {
			Type         string            `json:"type"`
			Instructions string            `json:"instructions"`
			Criteria     map[string]string `json:"criteria"`
		} `json:"questions"`
	}
	if err := json.Unmarshal([]byte(gotBody), &req); err != nil {
		t.Fatalf("body not json: %v", err)
	}
	if req.Model != "jev-latest" {
		t.Fatalf("body model %q", req.Model)
	}
	if req.State["message"] != "fix the flaky test" || req.State["agent"] != "main" {
		t.Fatalf("state: %+v", req.State)
	}
	if req.Questions["tier"].Type != "choice" || req.Questions["dissatisfied"].Type != "noul" {
		t.Fatalf("questions: %+v", req.Questions)
	}
	if req.Questions["tier"].Criteria["fast"] != testCriteria[TierFast] {
		t.Fatalf("criteria: %+v", req.Questions["tier"].Criteria)
	}
}

func TestJevClassifyFirstTurn(t *testing.T) {
	var gotBody string
	f := askFunc(func(_ context.Context, _, body string) (string, error) {
		gotBody = body
		return `{"answers":{"tier":{"choice":"fast","confidence":0.9},"dissatisfied":{"noul":0}}}`, nil
	})
	cfg := testConfig("typesafe/jev-latest")
	_, err := NewClassifier(cfg, f.ask).Classify(context.Background(), Question{
		Message:  "hello",
		Criteria: testCriteria,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gotBody, "previous_tier") {
		t.Fatalf("first turn body mentions previous_tier: %s", gotBody)
	}
	if strings.Contains(gotBody, "carries on") {
		t.Fatalf("first turn body has carries-on sentence: %s", gotBody)
	}
}

func TestJevClassifyUnparseable(t *testing.T) {
	f := askFunc(func(_ context.Context, _, _ string) (string, error) {
		return `garbage`, nil
	})
	cfg := testConfig("typesafe/jev-latest")
	if _, err := NewClassifier(cfg, f.ask).Classify(context.Background(), Question{
		Message:  "hello",
		Criteria: testCriteria,
	}); err == nil {
		t.Fatal("unparseable jev reply accepted")
	}
}

func TestPlainClassify(t *testing.T) {
	var bodies []string
	f := askFunc(func(_ context.Context, model, body string) (string, error) {
		bodies = append(bodies, body)
		if len(bodies) == 1 {
			return "2", nil
		}
		return "yes", nil
	})
	cfg := testConfig("qwen/qwen3-32b")
	v, err := NewClassifier(cfg, f.ask).Classify(context.Background(), Question{
		Message:      "this is broken, you did not do what I asked",
		PreviousTier: TierFast,
		Criteria:     testCriteria,
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.Tier != TierBalanced || v.TierConfidence != 1 || v.Dissatisfied != 1 {
		t.Fatalf("verdict: %+v", v)
	}
	if len(bodies) != 2 {
		t.Fatalf("%d asks, want 2", len(bodies))
	}
	// Both asks use the chat-completions shape with the cfg model.
	for _, b := range bodies {
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			MaxTokens int `json:"max_tokens"`
		}
		if err := json.Unmarshal([]byte(b), &req); err != nil {
			t.Fatalf("body not json: %v", err)
		}
		if req.Model != "qwen/qwen3-32b" || req.MaxTokens != 400 || len(req.Messages) != 1 || req.Messages[0].Role != "user" {
			t.Fatalf("req: %+v", req)
		}
	}
	if !strings.Contains(bodies[0], "1. "+testCriteria[TierFast]) ||
		!strings.Contains(bodies[0], "3. "+testCriteria[TierPerformance]) {
		t.Fatalf("tier prompt: %s", bodies[0])
	}
}

func TestPlainClassifyNo(t *testing.T) {
	n := 0
	f := askFunc(func(_ context.Context, _, _ string) (string, error) {
		n++
		if n == 1 {
			return "1", nil
		}
		return "no", nil
	})
	cfg := testConfig("qwen/qwen3-32b")
	v, err := NewClassifier(cfg, f.ask).Classify(context.Background(), Question{
		Message:      "ok, keep going",
		PreviousTier: TierFast,
		Criteria:     testCriteria,
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.Tier != TierFast || v.TierConfidence != 1 || v.Dissatisfied != 0 {
		t.Fatalf("verdict: %+v", v)
	}
}

func TestPlainClassifyGarbageTier(t *testing.T) {
	f := askFunc(func(_ context.Context, _, _ string) (string, error) {
		return "I don't know", nil
	})
	cfg := testConfig("qwen/qwen3-32b")
	v, err := NewClassifier(cfg, f.ask).Classify(context.Background(), Question{
		Message:      "hi",
		PreviousTier: TierBalanced,
		Criteria:     testCriteria,
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.TierConfidence != 0 {
		t.Fatalf("garbage tier confidence %v", v.TierConfidence)
	}
}

func TestPlainClassifyFirstTurnSkipsDissatisfied(t *testing.T) {
	n := 0
	f := askFunc(func(_ context.Context, _, _ string) (string, error) {
		n++
		return "3", nil
	})
	cfg := testConfig("qwen/qwen3-32b")
	v, err := NewClassifier(cfg, f.ask).Classify(context.Background(), Question{
		Message:  "plan the migration",
		Criteria: testCriteria,
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.Tier != TierPerformance || v.Dissatisfied != 0 {
		t.Fatalf("verdict: %+v", v)
	}
	if n != 1 {
		t.Fatalf("%d asks on first turn, want 1", n)
	}
}

func TestClassifyTimeout(t *testing.T) {
	f := askFunc(func(ctx context.Context, _, _ string) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
			return "1", nil
		}
	})
	cfg := testConfig("typesafe/jev-latest")
	if _, err := NewClassifier(cfg, f.ask).Classify(context.Background(), Question{
		Message:  "hi",
		Criteria: testCriteria,
	}); err == nil {
		t.Fatal("no timeout error")
	}
}

func TestEmptyClassifierAlwaysErrors(t *testing.T) {
	f := askFunc(func(_ context.Context, _, _ string) (string, error) {
		t.Fatal("ask called")
		return "", nil
	})
	cfg := testConfig("")
	if _, err := NewClassifier(cfg, f.ask).Classify(context.Background(), Question{
		Message:  "hi",
		Criteria: testCriteria,
	}); err == nil {
		t.Fatal("empty classifier accepted")
	}
}

// SP7 §4: the fallback prompt's verdicts are marked #plain so calibration
// can keep them apart from structured-output ones.
func TestPlainVerdictSourceMarksPlainPrompt(t *testing.T) {
	f := askFunc(func(_ context.Context, _, _ string) (string, error) { return "2", nil })
	v, err := NewClassifier(testConfig("qwen/qwen3-32b"), f.ask).Classify(context.Background(), Question{
		Message: "hello", PreviousTier: TierFast, Criteria: testCriteria,
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.Source != "qwen/qwen3-32b#plain" {
		t.Fatalf("source = %q, want qwen/qwen3-32b#plain", v.Source)
	}
}
