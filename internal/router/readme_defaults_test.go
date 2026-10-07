package router

import (
	"fmt"
	"os"
	"regexp"
	"testing"
)

// TestReadmeSaysTheDefaultsTheCodeApplies keeps README's router.json
// field table and example on the values defaults() fills in: the table
// once said review.timeout_ms was 15000 while the code applies 5000.
func TestReadmeSaysTheDefaultsTheCodeApplies(t *testing.T) {
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(b)
	var c Config
	c.defaults()
	want := map[string]string{
		"classify_timeout_ms":              fmt.Sprint(c.ClassifyTimeoutMs),
		"thresholds.tier_min":              fmt.Sprint(c.Thresholds.TierMin),
		"thresholds.dissatisfied_min":      fmt.Sprint(c.Thresholds.DissatisfiedMin),
		"thresholds.review_min":            fmt.Sprint(c.Thresholds.ReviewMin),
		"thresholds.review_confidence_min": fmt.Sprint(c.Thresholds.ReviewConfidenceMin),
		"review.mode":                      c.Review.Mode,
		"review.timeout_ms":                fmt.Sprint(c.Review.TimeoutMs),
		"review.max_answer_chars":          fmt.Sprint(c.Review.MaxAnswerChars),
		"escalate_turns":                   fmt.Sprint(c.EscalateTurns),
		"cache_ttl_seconds":                fmt.Sprint(c.CacheTTLSeconds),
	}
	for field, v := range want {
		row := regexp.MustCompile("\\| `" + regexp.QuoteMeta(field) + "` \\| `([^`]*)` \\|").FindStringSubmatch(readme)
		if row == nil {
			t.Errorf("README has no table row for %s", field)
			continue
		}
		if row[1] != v {
			t.Errorf("README says %s defaults to %s; the code applies %s", field, row[1], v)
		}
	}
	ex := regexp.MustCompile(`"review": \{ "mode": "([^"]+)", "timeout_ms": (\d+), "max_answer_chars": (\d+) \}`).FindStringSubmatch(readme)
	if ex == nil {
		t.Fatal("README's router.json example has no review line")
	}
	if ex[1] != c.Review.Mode || ex[2] != fmt.Sprint(c.Review.TimeoutMs) || ex[3] != fmt.Sprint(c.Review.MaxAnswerChars) {
		t.Errorf("README's example review = %v; the code applies %+v", ex[1:], c.Review)
	}
}
